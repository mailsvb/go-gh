<script lang="ts">
  import { onMount } from "svelte";
  import EventPanel from "./lib/EventPanel.svelte";
  import CodeWindow from "./lib/CodeWindow.svelte";
  import Header from "./lib/Header.svelte";
  import Login from "./lib/Login.svelte";
  import NotificationBanner from "./lib/NotificationBanner.svelte";
  import RepoList from "./lib/RepoList.svelte";
  import Settings from "./lib/Settings.svelte";
  import { app, init } from "./lib/store.svelte";

  // The same bundle serves the small login-code window (see main.go).
  const codeView = new URLSearchParams(location.search).get("view") === "code";

  let tab = $state<"repos" | "events">("repos");
  let showSettings = $state(false);

  onMount(() => {
    init();
  });

  const ready = $derived(app.state?.phase === "ready");

  function openEvents() {
    tab = "events";
    app.unread = 0;
  }

  $effect(() => {
    if (tab === "events" && app.unread > 0) app.unread = 0;
  });
</script>

{#if codeView}
  <CodeWindow />
{:else}
  <main>
    {#if !ready}
      <Login />
    {:else if showSettings}
      <Settings onClose={() => (showSettings = false)} />
    {:else}
      <Header onSettings={() => (showSettings = true)} />
      <nav>
        <button class:active={tab === "repos"} onclick={() => (tab = "repos")}>
          Repositories <span class="count">{app.repos.length}</span>
        </button>
        <button class:active={tab === "events"} onclick={openEvents}>
          Notifications
          {#if app.unread > 0}<span class="count new">{app.unread}</span>{:else}<span class="count">{app.events.length}</span>{/if}
        </button>
      </nav>
      <NotificationBanner />
      {#if app.state?.error}
        <p class="error banner">{app.state.error}</p>
      {/if}
      <section class="content">
        {#if tab === "repos"}
          <RepoList />
        {:else}
          <EventPanel />
        {/if}
      </section>
    {/if}
  </main>
{/if}

<style>
  main {
    height: 100%;
    display: flex;
    flex-direction: column;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    overflow: hidden;
  }
  nav {
    display: flex;
    border-bottom: 1px solid var(--border);
  }
  nav button {
    flex: 1;
    border: none;
    border-radius: 0;
    background: none;
    padding: 8px;
    color: var(--fg-muted);
    border-bottom: 2px solid transparent;
  }
  nav button.active {
    color: var(--fg);
    font-weight: 600;
    border-bottom-color: var(--accent);
  }
  .count {
    font-size: 11px;
    font-weight: 500;
    padding: 0 6px;
    border-radius: 10px;
    background: var(--bg-hover);
    margin-left: 4px;
  }
  .count.new {
    background: var(--accent);
    color: var(--accent-fg);
  }
  .banner {
    margin: 0;
    padding: 6px 12px;
    font-size: 12px;
    border-bottom: 1px solid var(--border);
  }
  .content {
    flex: 1;
    min-height: 0;
  }
</style>
