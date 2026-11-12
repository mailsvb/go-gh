<script lang="ts">
  import { Browser } from "@wailsio/runtime";
  import { NgrokService } from "../../bindings/go-gh/internal/services";
  import { app } from "./store.svelte";

  // compact: the short variant at the bottom of the login screen.
  let { compact = false }: { compact?: boolean } = $props();

  let token = $state("");
  let saving = $state(false);
  let clearing = $state(false);
  let err = $state("");

  const s = $derived(app.state);
  const tunnel = $derived(s?.tunnel ?? { status: "off", url: "", error: "" });
  const statusText = $derived(
    tunnel.status === "online"
      ? "Connected"
      : tunnel.status === "connecting"
        ? "Connecting…"
        : tunnel.status === "error"
          ? "Connection failed"
          : "Disconnected",
  );
  const host = $derived(tunnel.url.replace(/^https?:\/\//, ""));

  async function save() {
    saving = true;
    err = "";
    try {
      await NgrokService.SetToken(token);
      token = "";
    } catch (e) {
      err = String(e);
    } finally {
      saving = false;
    }
  }

  async function clear() {
    clearing = true;
    err = "";
    try {
      await NgrokService.ClearToken();
    } catch (e) {
      err = String(e);
    } finally {
      clearing = false;
    }
  }

  function openDashboard() {
    Browser.OpenURL("https://dashboard.ngrok.com/get-started/your-authtoken");
  }
</script>

<section class="ngrok" class:compact>
  {#if !compact}<h2>ngrok</h2>{/if}

  {#if s?.ngrokSet}
    <div class="status">
      <span class="dot {tunnel.status}"></span>
      <span class="label">
        <strong class={tunnel.status}>{statusText}</strong>
        {#if tunnel.status === "online" && host}<span class="muted mono" title={tunnel.url}>{host}</span>{/if}
      </span>
      <button onclick={clear} disabled={clearing} title="Delete the token and disconnect from ngrok">
        {clearing ? "Clearing…" : "Clear token"}
      </button>
    </div>
    {#if tunnel.status === "error" && tunnel.error}
      <p class="error small">{tunnel.error}</p>
    {/if}
  {:else}
    <p class="intro">
      Add your ngrok token for receiving Events
      <button class="link" onclick={openDashboard} title="Open the ngrok dashboard">Get a token</button>
    </p>
  {/if}

  {#if !s?.ngrokSet || !compact}
    <form
      onsubmit={(e) => {
        e.preventDefault();
        save();
      }}
    >
      <input type="password" placeholder={s?.ngrokSet ? "Replace token" : "ngrok authtoken"} bind:value={token} />
      <button class="primary" type="submit" disabled={saving || !token.trim()}>Save</button>
    </form>
  {/if}
  {#if !compact && !s?.ngrokSet}
    <p class="muted small">The token is stored in your system keychain.</p>
  {/if}
  {#if err}<p class="error small">{err}</p>{/if}
</section>

<style>
  .ngrok {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .compact {
    gap: 6px;
  }
  h2 {
    font-size: 13px;
    margin: 0;
  }
  p {
    margin: 0;
  }
  .intro {
    display: flex;
    gap: 8px;
    align-items: baseline;
    flex-wrap: wrap;
  }
  .status {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .label {
    flex: 1;
    min-width: 0;
    display: flex;
    gap: 6px;
    align-items: baseline;
  }
  .label .mono {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    flex: none;
    background: var(--fg-muted);
  }
  .dot.online {
    background: var(--ok);
  }
  .dot.connecting {
    background: var(--warn);
  }
  .dot.error {
    background: var(--danger);
  }
  strong.online {
    color: var(--ok);
  }
  strong.connecting {
    color: var(--warn);
  }
  strong.error {
    color: var(--danger);
  }
  form {
    display: flex;
    gap: 6px;
  }
  .mono {
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    font-size: 12px;
  }
  .small {
    font-size: 12px;
  }
  .error {
    color: var(--danger);
  }
  .link {
    border: none;
    background: none;
    padding: 0;
    color: var(--accent);
    font-size: 12px;
  }
  .link:hover {
    text-decoration: underline;
    background: none;
  }
</style>
