<script lang="ts">
  import { Browser } from "@wailsio/runtime";
  import Icon from "./Icon.svelte";
  import { app, relativeTime } from "./store.svelte";

  // Re-render relative times every 30 s.
  let now = $state(Date.now());
  $effect(() => {
    const t = setInterval(() => (now = Date.now()), 30_000);
    return () => clearInterval(t);
  });

  const ngrokOff = $derived(app.state?.tunnel.status !== "online");
</script>

<div class="wrap">
  {#if ngrokOff}
    <p class="hint muted">
      {#if app.state?.tunnel.status === "connecting"}
        Connecting to ngrok…
      {:else}
        Live notifications are off. Add your ngrok authtoken in Settings to receive webhooks.
      {/if}
      History below comes from the GitHub Events API and may lag a few minutes.
    </p>
  {/if}
  {#if app.events.length === 0}
    <p class="muted empty">No push, tag, branch or release events yet.</p>
  {:else}
    <ul>
      {#each app.events as e (e.key)}
        <li>
          <button onclick={() => e.url && Browser.OpenURL(e.url)} title="Open on GitHub">
            <span class="kind {e.kind}"><Icon name={e.kind} size={14} /></span>
            <span class="text">
              <span class="top">
                <strong class="repo">{e.repo}</strong>
                <span class="time muted" data-now={now}>{relativeTime(e.time)}</span>
              </span>
              <span class="title">{e.title}</span>
              {#if e.detail}<span class="detail muted">{e.detail}</span>{/if}
            </span>
          </button>
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .wrap {
    display: flex;
    flex-direction: column;
    height: 100%;
    min-height: 0;
  }
  .hint {
    margin: 0;
    padding: 8px 12px;
    font-size: 12px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-subtle);
  }
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
    overflow-y: auto;
    flex: 1;
  }
  li {
    border-bottom: 1px solid var(--border);
  }
  li button {
    display: flex;
    gap: 10px;
    width: 100%;
    text-align: left;
    border: none;
    border-radius: 0;
    background: none;
    padding: 8px 12px;
  }
  li button:hover {
    background: var(--bg-hover);
  }
  .kind {
    flex: none;
    width: 26px;
    height: 26px;
    border-radius: 50%;
    display: grid;
    place-items: center;
    background: var(--bg-subtle);
    border: 1px solid var(--border);
  }
  .kind.push {
    color: var(--push);
  }
  .kind.tag {
    color: var(--tag);
  }
  .kind.branch {
    color: var(--branch);
  }
  .kind.release {
    color: var(--release);
  }
  .text {
    display: flex;
    flex-direction: column;
    min-width: 0;
    flex: 1;
  }
  .top {
    display: flex;
    gap: 8px;
    justify-content: space-between;
  }
  .repo,
  .title,
  .detail {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .time {
    flex: none;
    font-size: 11px;
  }
  .detail {
    font-size: 12px;
  }
  .empty {
    padding: 24px;
    text-align: center;
  }
</style>
