<script lang="ts">
  // Content of the small "code" window shown while the device flow runs.
  // The main popup is hidden meanwhile (see main.go).
  import { Browser, Clipboard } from "@wailsio/runtime";
  import { onMount } from "svelte";
  import { AuthService } from "../../bindings/go-gh/internal/services";
  import { app, init } from "./store.svelte";

  let copied = $state(false);
  let now = $state(Date.now());

  onMount(() => {
    init();
    const t = setInterval(() => (now = Date.now()), 1000);
    return () => clearInterval(t);
  });

  const code = $derived(app.state?.deviceCode ?? null);
  const minutesLeft = $derived(
    code ? Math.max(0, Math.ceil((new Date(code.expiresAt).getTime() - now) / 60000)) : 0,
  );

  // Login.svelte copied the code already; reset the hint for a new code.
  let lastCode = "";
  $effect(() => {
    if (code && code.userCode !== lastCode) {
      lastCode = code.userCode;
      copied = true;
    }
  });

  async function copy() {
    if (!code) return;
    await Clipboard.SetText(code.userCode);
    copied = true;
  }
</script>

<div class="code-window">
  {#if code}
    <p class="muted">Enter this code on GitHub</p>
    <button class="code" onclick={copy} title="Copy code">{code.userCode}</button>
    <p class="muted small">
      {copied ? "Copied to clipboard · " : ""}Waiting for you to authorize go-gh{minutesLeft
        ? ` · expires in ${minutesLeft} min`
        : ""}
    </p>
    <div class="row">
      <button class="primary" onclick={() => Browser.OpenURL(code.verificationUri)}>Open GitHub again</button>
      <button onclick={() => AuthService.CancelLogin()}>Cancel</button>
    </div>
  {:else}
    <p class="muted">No login in progress.</p>
  {/if}
</div>

<style>
  .code-window {
    height: 100%;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 10px;
    padding: 16px;
    text-align: center;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    /* Frameless window: drag it anywhere except on the buttons. */
    --wails-draggable: drag;
  }
  button {
    --wails-draggable: no-drag;
  }
  p {
    margin: 0;
  }
  .small {
    font-size: 12px;
  }
  .code {
    font: 600 26px/1.2 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    letter-spacing: 3px;
    padding: 8px 18px;
    -webkit-user-select: all;
    user-select: all;
  }
  .row {
    display: flex;
    gap: 8px;
  }
</style>
