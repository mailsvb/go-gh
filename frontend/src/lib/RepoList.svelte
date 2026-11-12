<script lang="ts">
  import { Browser } from "@wailsio/runtime";
  import Icon from "./Icon.svelte";
  import { app } from "./store.svelte";

  let filter = $state("");

  const repos = $derived.by(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return app.repos;
    return app.repos.filter(
      (r) => r.nameWithOwner.toLowerCase().includes(q) || r.description.toLowerCase().includes(q),
    );
  });
</script>

<div class="wrap">
  <div class="filter">
    <input type="search" placeholder="Filter {app.repos.length} repositories" bind:value={filter} />
  </div>
  {#if app.state?.app.slug}
    <p class="note muted">
      Live events need go-gh installed on the repository's account.
      <button class="link" onclick={() => app.state && Browser.OpenURL(app.state.app.installUrl)}>
        {app.state.app.installed ? "Choose repositories" : "Install on your account"}
      </button>
    </p>
  {/if}
  {#if app.state?.reposLoading && app.repos.length === 0}
    <p class="muted empty">Loading repositories…</p>
  {:else if repos.length === 0}
    <p class="muted empty">{filter ? "No repositories match." : "No repositories found."}</p>
  {:else}
    <ul>
      {#each repos as r (r.nameWithOwner)}
        <li>
          {#if r.imageUrl}
            <img src={r.imageUrl} alt="" loading="lazy" referrerpolicy="no-referrer" />
          {:else}
            <span class="img-ph"></span>
          {/if}
          <div class="text">
            <div class="name">
              <span class="owner">{r.owner}/</span><strong>{r.name}</strong>
              {#if r.private}<span class="badge" title="Private"><Icon name="lock" size={11} /></span>{/if}
              {#if r.archived}<span class="badge">archived</span>{/if}
            </div>
            {#if r.description}
              <div class="desc muted">{r.description}</div>
            {/if}
          </div>
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
  .filter {
    padding: 8px 10px;
    border-bottom: 1px solid var(--border);
  }
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
    overflow-y: auto;
    flex: 1;
  }
  li {
    display: flex;
    gap: 10px;
    padding: 8px 12px;
    border-bottom: 1px solid var(--border);
    align-items: flex-start;
  }
  img,
  .img-ph {
    flex: none;
    width: 64px;
    height: 32px;
    border-radius: 4px;
    object-fit: cover;
    background: var(--bg-subtle);
    border: 1px solid var(--border);
  }
  .text {
    min-width: 0;
  }
  .name {
    display: flex;
    align-items: center;
    gap: 2px;
    white-space: nowrap;
    overflow: hidden;
  }
  .name strong {
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .owner {
    color: var(--fg-muted);
  }
  .badge {
    margin-left: 6px;
    font-size: 10px;
    color: var(--fg-muted);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 0 5px;
    line-height: 16px;
    display: inline-flex;
    align-items: center;
  }
  .desc {
    font-size: 12px;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  .note {
    margin: 0;
    padding: 6px 12px;
    font-size: 12px;
    border-bottom: 1px solid var(--border);
  }
  .link {
    border: none;
    background: none;
    padding: 0;
    color: var(--accent);
  }
  .link:hover {
    background: none;
    text-decoration: underline;
  }
  .empty {
    padding: 24px;
    text-align: center;
  }
</style>
