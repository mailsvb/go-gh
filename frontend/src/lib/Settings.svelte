<script lang="ts">
  import { Browser } from "@wailsio/runtime";
  import { SettingsService } from "../../bindings/go-gh/internal/services";
  import Icon from "./Icon.svelte";
  import { app } from "./store.svelte";

  let { onClose }: { onClose: () => void } = $props();

  let token = $state("");
  let saving = $state(false);
  let err = $state("");

  const s = $derived(app.state);
  const failed = $derived((s?.hooks.results ?? []).filter((r) => !r.ok));

  async function save() {
    saving = true;
    err = "";
    try {
      await SettingsService.SetNgrokToken(token);
      token = "";
    } catch (e) {
      err = String(e);
    } finally {
      saving = false;
    }
  }

  async function clear() {
    err = "";
    try {
      await SettingsService.ClearNgrokToken();
    } catch (e) {
      err = String(e);
    }
  }

  function problemText(p: string): string {
    switch (p) {
      case "sso":
        return "SAML SSO authorization required";
      case "notInstalled":
        return "go-gh is not installed here, or lacks a permission";
      case "forbidden":
        return "No access";
      case "error":
        return "Error";
    }
    return "";
  }
</script>

<div class="settings">
  <div class="bar">
    <button class="icon" onclick={onClose} title="Back"><Icon name="back" /></button>
    <strong>Settings</strong>
  </div>

  <div class="body">
    <section>
      <h2>GitHub App</h2>
      {#if !s?.app.slug}
        <p class="error small">This build has no GitHub App name (slug), so install links are unavailable.</p>
      {:else if s.app.installed}
        <p class="muted">
          Installed on your account for {s.app.selection === "all" ? "all repositories" : "selected repositories"}.
        </p>
        <div class="row">
          <button onclick={() => Browser.OpenURL(s.app.installUrl)}>Choose repositories</button>
          <button onclick={() => Browser.OpenURL(s.app.anyInstallUrl)}>Install on another account</button>
        </div>
      {:else}
        <p class="muted">
          go-gh only sees repositories where its GitHub App is installed. Install it on your account to include your
          personal repositories.
        </p>
        <div class="row">
          <button class="primary" onclick={() => Browser.OpenURL(s.app.installUrl)}>Install on my account</button>
          <button onclick={() => Browser.OpenURL(s.app.anyInstallUrl)}>Install on another account</button>
        </div>
      {/if}
    </section>

    <section>
      <h2>ngrok</h2>
      {#if s?.ngrokSet}
        <p>
          Status: <strong class={s.tunnel.status}>{s.tunnel.status}</strong>
          {#if s.tunnel.url}<br /><span class="muted mono">{s.tunnel.url}</span>{/if}
          {#if s.tunnel.error}<br /><span class="error">{s.tunnel.error}</span>{/if}
        </p>
      {:else}
        <p class="muted">
          go-gh receives webhooks through an ngrok tunnel. Paste your authtoken from the
          <button class="link" onclick={() => Browser.OpenURL("https://dashboard.ngrok.com/get-started/your-authtoken")}
            >ngrok dashboard</button
          >. It is stored in your system keychain.
        </p>
      {/if}
      <form
        onsubmit={(e) => {
          e.preventDefault();
          save();
        }}
      >
        <input type="password" placeholder={s?.ngrokSet ? "Replace authtoken" : "ngrok authtoken"} bind:value={token} />
        <button class="primary" type="submit" disabled={saving || !token.trim()}>Save</button>
      </form>
      {#if s?.ngrokSet}
        <button onclick={clear}>Remove token and stop webhooks</button>
      {/if}
      {#if err}<p class="error">{err}</p>{/if}
    </section>

    <section>
      <h2>Webhooks</h2>
      <p class="muted">
        {s?.hooks.ok ?? 0} active, {s?.hooks.failed ?? 0} failed. Webhooks are deactivated when you quit go-gh.
      </p>
      <label class="check">
        <input
          type="checkbox"
          checked={s?.hookPersonalRepos ?? true}
          onchange={(e) => SettingsService.SetHookPersonalRepos(e.currentTarget.checked)}
        />
        Add webhooks to repositories I administer outside of hooked organizations
      </label>
      {#if failed.length}
        <details>
          <summary>{failed.length} failed</summary>
          <ul class="fails">
            {#each failed as f}
              <li>
                <strong>{f.target.org ? `org:${f.target.org}` : `${f.target.owner}/${f.target.repo}`}</strong>
                <span class="muted">{problemText(f.problem ?? "") || f.error}</span>
                {#if f.ssoUrl}<button class="link" onclick={() => Browser.OpenURL(f.ssoUrl!)}>Authorize SSO</button>{/if}
              </li>
            {/each}
          </ul>
        </details>
      {/if}
    </section>

    <section>
      <h2>Organizations</h2>
      {#if !s?.orgs?.length}
        <p class="muted">You are not a member of any organization.</p>
      {:else}
        <ul class="orgs">
          {#each s.orgs as o (o.login)}
            <li>
              <img src={o.avatarUrl} alt="" width="24" height="24" />
              <div class="org-text">
                <strong>{o.login}</strong>
                <span class="muted small">
                  {#if !o.installed}
                    go-gh is not installed{o.owner ? "" : ". Ask an owner, or request it"}
                  {:else if o.owner}
                    {o.hooked ? "Organization webhook active" : o.enabled ? "Organization webhook pending" : "Organization webhook off"}
                  {:else}
                    Member: webhooks on repositories you administer
                  {/if}
                </span>
                {#if o.problem}
                  <span class="error small">{problemText(o.problem)}</span>
                {/if}
              </div>
              <div class="org-actions">
                {#if o.ssoUrl}
                  <button onclick={() => Browser.OpenURL(o.ssoUrl)}>Authorize SSO</button>
                {/if}
                {#if o.installUrl}
                  <button onclick={() => Browser.OpenURL(o.installUrl)}>
                    {o.installed ? "Repositories" : o.owner ? "Install" : "Request"}
                  </button>
                {/if}
                {#if o.owner && o.installed}
                  <input
                    type="checkbox"
                    title="Organization webhook"
                    checked={o.enabled}
                    onchange={(e) => SettingsService.SetOrgEnabled(o.login, e.currentTarget.checked)}
                  />
                {/if}
              </div>
            </li>
          {/each}
        </ul>
        <p class="muted small">
          Organization owners install go-gh and choose its repositories. Members can request the installation, and an
          owner approves it on GitHub. Click Refresh after installing.
        </p>
      {/if}
    </section>

    {#if s?.backfillErrors?.length}
      <section>
        <h2>History</h2>
        <details>
          <summary class="muted">{s.backfillErrors.length} event feeds could not be read</summary>
          <ul class="fails">
            {#each s.backfillErrors as e}<li class="muted small">{e}</li>{/each}
          </ul>
        </details>
      </section>
    {/if}

    <section>
      <h2>Session</h2>
      <p class="muted small">
        Logging out removes the token from this computer. To revoke go-gh's access completely, use
        <button class="link" onclick={() => s && Browser.OpenURL(s.app.authorizedAppsUrl)}
          >GitHub → Settings → Applications → Authorized GitHub Apps</button
        >.
      </p>
    </section>
  </div>
</div>

<style>
  .settings {
    display: flex;
    flex-direction: column;
    height: 100%;
  }
  .bar {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 10px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-subtle);
  }
  .body {
    overflow-y: auto;
    padding: 0 14px 14px;
    -webkit-user-select: text;
    user-select: text;
  }
  section {
    padding: 12px 0;
    border-bottom: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  section:last-child {
    border-bottom: none;
  }
  h2 {
    font-size: 13px;
    margin: 0;
  }
  p {
    margin: 0;
  }
  form {
    display: flex;
    gap: 6px;
  }
  .mono {
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    font-size: 12px;
  }
  .online {
    color: var(--ok);
  }
  .connecting {
    color: var(--warn);
  }
  .error {
    color: var(--danger);
  }
  .small {
    font-size: 12px;
  }
  .check {
    display: flex;
    gap: 8px;
    align-items: flex-start;
  }
  .link {
    border: none;
    background: none;
    padding: 0;
    color: var(--accent);
  }
  .link:hover {
    text-decoration: underline;
    background: none;
  }
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .orgs li {
    display: flex;
    gap: 10px;
    align-items: center;
    padding: 6px 0;
  }
  .orgs img {
    border-radius: 6px;
    flex: none;
  }
  .org-text {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-width: 0;
  }
  .org-actions {
    display: flex;
    gap: 8px;
    align-items: center;
  }
  .row {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }
  .fails li {
    display: flex;
    flex-direction: column;
    padding: 4px 0;
    align-items: flex-start;
  }
</style>
