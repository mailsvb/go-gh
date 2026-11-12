<script lang="ts">
  import { Browser, Clipboard } from "@wailsio/runtime";
  import { AuthService } from "../../bindings/go-gh/internal/services";
  import { app } from "./store.svelte";

  let busy = $state(false);
  let localError = $state("");
  let copied = $state(false);

  const s = $derived(app.state);
  const code = $derived(s?.deviceCode ?? null);

  async function login() {
    busy = true;
    localError = "";
    try {
      const dc = await AuthService.StartLogin();
      await Clipboard.SetText(dc.userCode);
      copied = true;
      await Browser.OpenURL(dc.verificationUri);
    } catch (e) {
      localError = String(e);
    } finally {
      busy = false;
    }
  }

  async function copy() {
    if (!code) return;
    await Clipboard.SetText(code.userCode);
    copied = true;
  }
</script>

<div class="login">
  <svg class="mark" viewBox="0 0 48 48" aria-hidden="true">
    <circle cx="24" cy="24" r="22" fill="var(--bg-subtle)" stroke="var(--border)" />
    <path
      d="M17 31v-3.5M31 31v-3.5M15 22a9 9 0 0 1 18 0v4H15z"
      fill="none"
      stroke="var(--accent)"
      stroke-width="2.5"
      stroke-linecap="round"
    />
    <circle cx="20.5" cy="21.5" r="1.4" fill="var(--accent)" />
    <circle cx="27.5" cy="21.5" r="1.4" fill="var(--accent)" />
  </svg>
  <h1>go-gh</h1>
  <p class="muted">GitHub push, tag, branch and release events in your tray.</p>

  {#if !s}
    <p class="muted">Starting…</p>
  {:else if s.phase === "starting"}
    <p class="muted">Checking your GitHub session…</p>
  {:else if code}
    <p>Enter this code on GitHub:</p>
    <button class="code" onclick={copy} title="Copy code">{code.userCode}</button>
    <p class="muted small">{copied ? "Copied to clipboard. " : ""}Waiting for you to authorize go-gh…</p>
    <div class="row">
      <button onclick={() => Browser.OpenURL(code.verificationUri)}>Open GitHub again</button>
      <button onclick={() => AuthService.CancelLogin()}>Cancel</button>
    </div>
  {:else}
    {#if !s.clientIdSet}
      <p class="error">This build has no GitHub App client ID. See the README for how to set it.</p>
    {/if}
    <button class="primary big" onclick={login} disabled={busy || !s.clientIdSet}>
      {busy ? "Contacting GitHub…" : "Log in with GitHub"}
    </button>
    {#if s.error}
      <p class="error small">{s.error}</p>
      {#if s.canRetry}<button onclick={() => AuthService.Retry()}>Retry</button>{/if}
    {:else if localError}
      <p class="error small">{localError}</p>
    {/if}
    <p class="muted small">
      go-gh is a GitHub App. It reads repository metadata and events and manages webhooks, only on accounts and
      repositories where you install it. It never gets access to your code.
    </p>
  {/if}
</div>

<style>
  .login {
    height: 100%;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 10px;
    padding: 32px;
    text-align: center;
  }
  .mark {
    width: 64px;
    height: 64px;
  }
  h1 {
    margin: 0;
    font-size: 20px;
  }
  p {
    margin: 0;
    max-width: 320px;
  }
  .small {
    font-size: 12px;
  }
  .big {
    padding: 8px 20px;
    font-weight: 600;
  }
  .code {
    font: 600 24px/1.2 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    letter-spacing: 3px;
    padding: 10px 18px;
    -webkit-user-select: all;
    user-select: all;
  }
  .row {
    display: flex;
    gap: 8px;
  }
</style>
