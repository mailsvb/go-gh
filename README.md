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
| History | The last 100 events are kept **in memory only**. On every start, and after the system wakes, they are rebuilt from the GitHub Events API using in-memory ETag caching. |
| Repo list | GraphQL `viewer.repositories` (limited to the app's installations), with each repo's social-preview image (`openGraphImageUrl`) as its logo. |

Only non-secret settings are written to disk: install ID, org toggles and the
personal-repo toggle. They live in `config.json` in the OS config dir:
`~/Library/Application Support/go-gh` on macOS, `%AppData%\go-gh` on Windows.
Secrets (the GitHub token, the ngrok authtoken and the webhook secret) go into
the macOS Keychain or the Windows Credential Manager.

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

Permissions (nothing else is needed, and go-gh never gets access to code):

| Scope | Permission | Access | Used for |
|---|---|---|---|
| Repository | Metadata | Read | repo list, repo event history (mandatory) |
| Repository | Webhooks | Read & write | per-repo webhooks |
| Organization | Webhooks | Read & write | org webhooks (owners only) |
| Organization | Events | Read | org event history |
| Organization | Members | Read | your org memberships and roles |
| Account | Events | Read | your private event history |

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
- **ngrok:** get an authtoken from https://dashboard.ngrok.com/get-started/your-authtoken and paste it into the app's Settings.

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
main.go                  tray, popup window, notifications, service wiring
internal/auth            GitHub App device flow, token refresh and storage
internal/config          non-secret settings (installId, toggles)
internal/secrets         OS keychain
internal/gh              REST/GraphQL client, repos, orgs, hooks, Events API backfill
internal/events          event model, payload normalization, 100-event in-memory store
internal/webhook         HMAC-verified webhook receiver
internal/tunnel          ngrok endpoint
internal/core            orchestration and state
internal/services        Wails-bound services used by the frontend
frontend/                Svelte 5 + TypeScript UI
```
