# go-gh

A small tray app for macOS (arm64) and Windows (x86_64). It logs into GitHub,
receives **push, tag, branch and release** events as webhooks through an
[ngrok](https://ngrok.com) tunnel, and shows them as OS notifications and in a
notification panel. The main window shows your account and the repositories
where its GitHub App is installed.

Built with Go 1.27, [Wails v3](https://v3.wails.io) (Svelte + TypeScript UI),
[go-github](https://github.com/google/go-github) v93,
[githubv4](https://github.com/shurcooL/githubv4) and
[ngrok-go](https://github.com/ngrok/ngrok-go) v2.

## How it works

| Piece | Implementation |
|---|---|
| Tray | Wails system tray with an attached popup window. On macOS it is a menu-bar app with no Dock icon (`LSUIElement`, accessory activation policy). |
| Login | A **GitHub App** with the **device flow**. Only the app's client ID (and its URL name for install links) is embedded in the binary. User tokens expire after 8 hours and are refreshed automatically, without a client secret. The token and refresh token are stored in the OS keychain, and the refresh token is saved again after every refresh because GitHub rotates it. |
| Access | go-gh only sees accounts and repositories where its GitHub App is **installed**. Settings shows an *Install*, *Request* or *Repositories* button per organization, and *Authorize SSO* for SAML orgs. |
| Webhooks | Created by go-gh through the API, not the GitHub App's own webhook (which has one fixed URL for all users). An org webhook where you are owner and the app is installed, otherwise a webhook on each repo you admin. They subscribe to `push`, `create` and `release`, and every delivery is HMAC-verified. |
| ngrok | An **ephemeral** URL per launch. On start the app re-points its own hooks (path `/hook/<installId>`) to the new URL. On quit it deactivates them. |
| History | The last 100 events are kept **in memory only**. On every start, and after the system wakes, they are rebuilt from several GitHub APIs (see [Where the data comes from](#where-the-data-comes-from)), with in-memory ETag caching. |
| Repo list | GraphQL `viewer.repositories` (limited to the app's installations), with each repo's social-preview image (`openGraphImageUrl`) as its logo. |

Only non-secret settings are written to disk: install ID, org toggles and the
personal-repo toggle. They live in `config.json` in the OS config dir:
`~/Library/Application Support/go-gh` on macOS, `%AppData%\go-gh` on Windows.
Secrets (the GitHub token, the ngrok authtoken and the webhook secret) go into
the macOS Keychain or the Windows Credential Manager.

## Where the data comes from

**Live events** arrive as webhooks (`push`, `create`, `release`) through the
ngrok tunnel while go-gh runs. Webhook payloads are complete, so nothing else
is fetched for them.

**Synced history** (on start, after wake-up, on refresh) fills in what
happened while go-gh was not running. Every source maps to the same event
keys as the webhooks, so a change seen both live and synced shows once.

| Information | API | Permission |
|---|---|---|
| Pushes (incl. force pushes, pull request and merge queue merges) and new branches | `GET /repos/{owner}/{repo}/activity`, for the 30 most recently pushed repositories that get webhooks | Contents: read |
| Commit count and head commit message of a synced push | `GET /repos/{owner}/{repo}/compare/{before}...{head}`, plus `GET /repos/{owner}/{repo}/commits/{sha}` for ranges over 100 commits; cached for the app's lifetime | Contents: read |
| New tags, published releases | Events API: `GET /users/{login}/events` (your own activity), `GET /users/{login}/events/orgs/{org}` (each installed organization), `GET /repos/{owner}/{repo}/events` (repositories outside those organizations, up to 50) | Account / Organization Events: read, Metadata: read |
| Pushes of accounts without Contents: read, and of repositories beyond the 30 | the same Events API feeds | as above |

The Events API is only a fallback for pushes: GitHub delivers its events
with a delay of up to several hours, and a pull request merge appears there
only as a `PullRequestEvent`, not as a push. Accounts whose installation has
not approved *Contents: read* are listed under Settings → History.

**Everything else:**

| Information | API | Permission |
|---|---|---|
| Your account (header) | `GET /user` | none |
| Repository list, logos, last push time | GraphQL `viewer.repositories` (`openGraphImageUrl`, `pushedAt`, `viewerPermission`) | Metadata: read |
| Organizations and your role | `GET /user/memberships/orgs`, plus your public memberships (`GET /users/{login}/orgs`), the owners of your repositories and the app installations | Members: read |
| Where go-gh is installed, and with which repository selection | `GET /user/installations` | none |
| Webhooks | `GET`/`POST`/`PATCH /orgs/{org}/hooks` and `/repos/{owner}/{repo}/hooks` | Organization / Repository Webhooks: read & write |
| Login | device flow: `POST github.com/login/device/code`, `POST github.com/login/oauth/access_token` (also for token refresh) | none |

## One-time setup

### 1. Create the GitHub App

Go to GitHub → Settings → Developer settings → GitHub Apps → *New GitHub App*.
To own it as an organization, use the organization's settings instead.

| Field | Value |
|---|---|
| GitHub App name | e.g. `go-gh` (its URL name, the **slug**, appears in `https://github.com/apps/<slug>`) |
| Homepage URL | any URL, e.g. this repository |
| Callback URL | leave empty (the device flow doesn't use it) |
| Expire user authorization tokens | **on** (go-gh refreshes them) |
| Enable Device Flow | **on** |
| Webhook → Active | **off** (go-gh creates its own webhooks per repo/org) |
| Where can this app be installed? | *Any account* if people outside your account should use it |

Permissions (nothing else is needed). go-gh never writes code; *Contents:
read* lets it read the push log and commit messages, which GitHub only
exposes with that permission:

| Scope | Permission | Access | Used for |
|---|---|---|---|
| Repository | Metadata | Read | repo list, repo event history (mandatory) |
| Repository | Contents | Read | push history incl. pull request merges (`/activity`), commit count and message of synced pushes (`/compare`) |
| Repository | Webhooks | Read & write | per-repo webhooks |
| Organization | Webhooks | Read & write | org webhooks (owners only) |
| Organization | Events | Read | org event history |
| Organization | Members | Read | your org memberships and roles |
| Account | Events | Read | your private event history |

Adding *Contents: read* to an existing app asks every installation to
approve the change (on the account's or organization's *Installed GitHub
Apps* page). Until then go-gh falls back to the Events API for that account,
which can be hours late and misses pull request merges, and Settings →
History says so.

After creating it, note the **Client ID** (on the app's *General* page, not
the App ID) and the **slug**. No client secret or private key is needed.

### 2. Install it

Install the app on your account and on each organization you want covered
(`https://github.com/apps/<slug>/installations/new`), choosing all or selected
repositories. Org members who aren't owners can request the installation, and
an owner approves it. go-gh links to these pages from Settings.

### 3. Configure builds and ngrok

- **CI:** add the repository *variables* `GH_APP_CLIENT_ID` and `GH_APP_SLUG`
  (Settings → Secrets and variables → Actions → Variables). Neither is secret.
- **ngrok:** get an authtoken from https://dashboard.ngrok.com/get-started/your-authtoken and paste it into the ngrok section of the login screen (or Settings, once logged in).

## Development

Prerequisites: Go 1.27+, Node LTS, and the Wails CLI:

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0
```

Run in dev mode with hot reload. The app settings can come from the environment:

```bash
GOGH_GITHUB_CLIENT_ID=<client id> GOGH_GITHUB_APP_SLUG=<slug> wails3 dev
```

Or run a packaged build with them:

```bash
open --env GOGH_GITHUB_CLIENT_ID=<client id> --env GOGH_GITHUB_APP_SLUG=<slug> bin/go-gh.app
```

Run the tests:

```bash
go test ./... && (cd frontend && npm run check)
```

Package for the current OS. This produces `bin/go-gh.app` on macOS, and on Windows `bin/go-gh.exe` plus the NSIS installer:

```bash
wails3 task package EXTRA_LDFLAGS="-X main.githubClientID=<client id> -X main.githubAppSlug=<slug> -X main.version=dev"
```

Or let `make` fill these in: `make build` takes the client ID and slug from
`GH_APP_CLIENT_ID` / `GH_APP_SLUG` in the environment, or else from the
repository variables via the `gh` CLI, and uses `git describe` as the version.
`make run` opens the result; `make dev`, `make test` and `make clean` do what
their names say.

## CI

`.github/workflows/build.yml` builds on `macos-latest` (arm64) and
`windows-latest` (x86_64) on every push, PR and manual run. It runs `go vet`,
`go test` and `svelte-check`, then uploads these artifacts to the run summary:

- `go-gh-macos-arm64`: `go-gh-macos-arm64.zip`, an ad-hoc-signed `.app`
- `go-gh-windows-amd64`: `go-gh-windows-amd64.exe` and a per-user NSIS installer

### Running the unsigned macOS build

The CI build is ad-hoc signed, not notarized. After unzipping, clear the
quarantine flag once:

```bash
xattr -dr com.apple.quarantine go-gh.app
```

Developer ID signing and notarization are planned for a later version.

## Layout

```
main.go                  tray, popup and login-code windows, notifications, service wiring
internal/auth            GitHub App device flow, token refresh and storage
internal/config          non-secret settings (installId, toggles)
internal/secrets         OS keychain
internal/gh              REST/GraphQL client, repos, orgs, hooks, Events API backfill
internal/events          event model, payload normalization, 100-event in-memory store
internal/webhook         HMAC-verified webhook receiver
internal/tunnel          ngrok endpoint
internal/core            orchestration and state
internal/services        Wails-bound services (App, Auth, Ngrok, Webhook, Notify)
frontend/                Svelte 5 + TypeScript UI
```
