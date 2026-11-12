<script lang="ts">
  import { NotifyService } from "../../bindings/go-gh/internal/services";
  import Icon from "./Icon.svelte";
  import { app } from "./store.svelte";
  import { openNotificationSettings } from "./system";

  let busy = $state(false);

  const n = $derived(app.state?.notifications);
  const show = $derived(!!n?.checked && !n.allowed && !app.notifyBannerHidden);

  async function enable() {
    busy = true;
    try {
      // Shows the OS prompt if go-gh was never asked. If it was denied
      // before, only the system settings can turn it on.
      const st = await NotifyService.Enable();
      if (!st.allowed) openNotificationSettings();
    } finally {
      busy = false;
    }
  }
</script>

{#if show}
  <div class="banner" role="status">
    <Icon name="bell-off" />
    <span class="text">Notifications are off. New events only show up in go-gh.</span>
    <button class="primary" onclick={enable} disabled={busy}>Enable</button>
    <button class="icon" title="Hide until next start" onclick={() => (app.notifyBannerHidden = true)}>
      <Icon name="close" />
    </button>
  </div>
{/if}

<style>
  .banner {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 6px 6px 12px;
    font-size: 12px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-subtle);
    color: var(--warn);
  }
  .text {
    flex: 1;
    color: var(--fg);
  }
  .primary {
    padding: 2px 10px;
  }
</style>
