<script lang="ts">
  import { Browser } from "@wailsio/runtime";
  import { AppService, AuthService } from "../../bindings/go-gh/internal/services";
  import Icon from "./Icon.svelte";
  import { app } from "./store.svelte";

  let { onSettings }: { onSettings: () => void } = $props();

  const s = $derived(app.state);
  const user = $derived(s?.user ?? null);
  const tunnel = $derived(s?.tunnel.status ?? "off");
  const dotTitle = $derived(
    tunnel === "online"
      ? `Receiving webhooks via ${s?.tunnel.url} (${s?.hooks.ok} ok, ${s?.hooks.failed} failed)`
      : tunnel === "connecting"
        ? "Connecting to ngrok…"
        : tunnel === "error"
          ? `ngrok error: ${s?.tunnel.error}`
          : "Webhooks are off: add your ngrok authtoken in Settings",
  );
</script>

<header>
  {#if user}
    <button class="user" onclick={() => Browser.OpenURL(user.htmlUrl)} title="Open profile on GitHub">
      <img src={user.avatarUrl} alt="" width="32" height="32" />
      <span class="names">
        <strong>{user.name || user.login}</strong>
        {#if user.name}<span class="muted">@{user.login}</span>{/if}
      </span>
    </button>
  {/if}
  <span class="spacer"></span>
  <span class="dot {tunnel}" title={dotTitle}></span>
  <button class="icon" title="Refresh" onclick={() => AppService.Refresh()}><Icon name="refresh" /></button>
  <button class="icon" title="Settings" onclick={onSettings}><Icon name="gear" /></button>
  <button class="icon" title="Log out" onclick={() => AuthService.Logout()}><Icon name="logout" /></button>
</header>

<style>
  header {
    display: flex;
    align-items: center;
    gap: 2px;
    padding: 10px 10px 10px 12px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-subtle);
  }
  .user {
    display: flex;
    align-items: center;
    gap: 10px;
    border: none;
    background: none;
    padding: 0;
    text-align: left;
    min-width: 0;
  }
  .user:hover {
    background: none;
  }
  img {
    border-radius: 50%;
    flex: none;
  }
  .names {
    display: flex;
    flex-direction: column;
    line-height: 1.25;
    min-width: 0;
  }
  .names > * {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .spacer {
    flex: 1;
  }
  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    margin: 0 8px;
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
</style>
