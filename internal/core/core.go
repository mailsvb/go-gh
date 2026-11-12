// Package core orchestrates login, repository listing, event history,
// the ngrok tunnel and webhook management. The Wails services in package
// services are thin wrappers around it.
package core

import (
	"context"
	"errors"
	"golang.org/x/oauth2"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"go-gh/internal/auth"
	"go-gh/internal/config"
	"go-gh/internal/events"
	"go-gh/internal/gh"
	"go-gh/internal/secrets"
	"go-gh/internal/tunnel"
	"go-gh/internal/webhook"
)

// Event names emitted to the frontend.
const (
	EvtState     = "state:changed" // payload: State
	EvtNewEvent  = "events:new"    // payload: events.Event
	EvtEventsSet = "events:reset"  // payload: []events.Event
	EvtRepos     = "repos:changed" // payload: []gh.Repo
)

const (
	apiConcurrency   = 8
	maxBackfillRepos = 50
	tunnelRetry      = 30 * time.Second
)

// Phase of the session.
type Phase string

const (
	PhaseStarting  Phase = "starting"
	PhaseLoggedOut Phase = "loggedOut"
	PhaseLoggingIn Phase = "loggingIn"
	PhaseReady     Phase = "ready"
)

// TunnelState describes the ngrok connection.
type TunnelState struct {
	Status string `json:"status"` // off | connecting | online | error
	URL    string `json:"url"`
	Error  string `json:"error"`
}

// OrgState is an organization with its access status.
type OrgState struct {
	gh.Org
	Installed bool             `json:"installed"` // the GitHub App is installed on the org
	Selection string           `json:"selection"` // "all" or "selected" repositories
	Enabled   bool             `json:"enabled"`   // org webhook wanted (owner, installed, not disabled)
	Hooked    bool             `json:"hooked"`    // org webhook is active
	Problem   gh.AccessProblem `json:"problem"`
	SSOURL    string           `json:"ssoUrl"`
	// InstallURL installs the app on the org (or requests it, for non-owners),
	// or configures its repository selection when already installed.
	InstallURL string `json:"installUrl"`
}

// AppState describes the GitHub App installation on the user's own account.
type AppState struct {
	Slug       string `json:"slug"`
	Installed  bool   `json:"installed"`  // installed on the personal account
	Selection  string `json:"selection"`  // "all" or "selected"
	InstallURL string `json:"installUrl"` // install, or configure repository selection
	// AnyInstallURL lets the user pick any account to install on.
	AnyInstallURL string `json:"anyInstallUrl"`
	// AuthorizedAppsURL is where the user can revoke go-gh's authorization.
	AuthorizedAppsURL string `json:"authorizedAppsUrl"`
}

// HookSummary aggregates webhook results.
type HookSummary struct {
	OK      int             `json:"ok"`
	Failed  int             `json:"failed"`
	Results []gh.HookResult `json:"results"`
}

// State is everything the UI needs to render chrome and status.
type State struct {
	Phase             Phase            `json:"phase"`
	User              *gh.User         `json:"user"`
	App               AppState         `json:"app"`
	DeviceCode        *auth.DeviceCode `json:"deviceCode"`
	ClientIDSet       bool             `json:"clientIdSet"`
	NgrokSet          bool             `json:"ngrokSet"`
	HookPersonalRepos bool             `json:"hookPersonalRepos"`
	Tunnel            TunnelState      `json:"tunnel"`
	Hooks             HookSummary      `json:"hooks"`
	Orgs              []OrgState       `json:"orgs"`
	ReposLoading      bool             `json:"reposLoading"`
	BackfillErrors    []string         `json:"backfillErrors"`
	Error             string           `json:"error"`
	// CanRetry is set when a stored session exists but GitHub was unreachable.
	CanRetry bool `json:"canRetry"`
}

// Notifier shows an OS notification for a live event.
type Notifier interface {
	Notify(e events.Event)
}

// Emitter sends an event to the frontend.
type Emitter func(name string, data any)

// Options configures a Core.
type Options struct {
	ClientID string
	AppSlug  string // GitHub App URL name, used for install links
	Secrets  secrets.Store
	Config   *config.Manager
	Notifier Notifier
	Emit     Emitter
	Log      *slog.Logger
}

// Core is the application backend.
type Core struct {
	opts  Options
	log   *slog.Logger
	store *events.Store

	mu       sync.Mutex
	state    State
	client   *gh.Client
	repos    []gh.Repo
	tun      *tunnel.Tunnel
	session  context.Context // cancelled on logout/shutdown
	cancel   context.CancelFunc
	login    context.CancelFunc
	hookRuns sync.Mutex // serializes EnsureHooks/Deactivate
}

// New creates a Core. Call Start once the UI is up.
func New(o Options) *Core {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Emit == nil {
		o.Emit = func(string, any) {}
	}
	c := &Core{opts: o, log: o.Log, store: events.NewStore(events.DefaultCapacity)}
	c.state = State{
		Phase:       PhaseStarting,
		ClientIDSet: o.ClientID != "",
		Tunnel:      TunnelState{Status: "off"},
		App:         c0AppState(o.AppSlug),
	}
	return c
}

// State returns a snapshot of the current state.
func (c *Core) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked()
}

func (c *Core) snapshotLocked() State {
	s := c.state
	s.Orgs = slices.Clone(s.Orgs)
	s.Hooks.Results = slices.Clone(s.Hooks.Results)
	s.BackfillErrors = slices.Clone(s.BackfillErrors)
	cfg := c.opts.Config.Get()
	s.HookPersonalRepos = cfg.HookPersonalRepos
	return s
}

func (c *Core) update(fn func(*State)) {
	c.mu.Lock()
	fn(&c.state)
	s := c.snapshotLocked()
	c.mu.Unlock()
	c.opts.Emit(EvtState, s)
}

// Events returns the in-memory history, newest first.
func (c *Core) Events() []events.Event { return c.store.List() }

// Repos returns the last loaded repository list.
func (c *Core) Repos() []gh.Repo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.repos)
}

// Start loads the stored token and, if present, activates the session.
func (c *Core) Start() {
	ngrokTok, _ := c.opts.Secrets.Get(secrets.NgrokToken)
	stored, err := c.opts.Secrets.Get(secrets.GitHubToken)
	c.update(func(s *State) { s.NgrokSet = ngrokTok != "" })
	if err != nil || stored == "" {
		if err != nil {
			c.log.Warn("reading token from keychain", "err", err)
		}
		c.update(func(s *State) { s.Phase = PhaseLoggedOut })
		return
	}
	tok, err := auth.Unmarshal(stored)
	if err != nil {
		c.log.Warn("stored token is invalid", "err", err)
		_ = c.opts.Secrets.Delete(secrets.GitHubToken)
		c.update(func(s *State) { s.Phase = PhaseLoggedOut })
		return
	}
	go c.activate(tok)
}

// StartLogin begins the device flow and returns the code to show.
func (c *Core) StartLogin() (auth.DeviceCode, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	flow, code, err := auth.Start(ctx, c.opts.ClientID)
	if err != nil {
		cancel()
		c.update(func(s *State) { s.Error, s.CanRetry = err.Error(), false })
		return auth.DeviceCode{}, err
	}
	c.mu.Lock()
	if c.login != nil {
		c.login()
	}
	c.login = cancel
	c.mu.Unlock()
	c.update(func(s *State) {
		s.Phase = PhaseLoggingIn
		s.DeviceCode = &code
		s.Error, s.CanRetry = "", false
	})
	go func() {
		defer cancel()
		tok, err := flow.Wait(ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				c.update(func(s *State) {
					s.Phase, s.DeviceCode, s.Error = PhaseLoggedOut, nil, "Login failed: "+err.Error()
					s.CanRetry = false
				})
			}
			return
		}
		c.saveToken(tok)
		c.update(func(s *State) { s.DeviceCode = nil })
		c.activate(tok)
	}()
	return code, nil
}

// CancelLogin aborts a pending device flow.
func (c *Core) CancelLogin() {
	c.mu.Lock()
	if c.login != nil {
		c.login()
		c.login = nil
	}
	c.mu.Unlock()
	c.update(func(s *State) { s.Phase, s.DeviceCode = PhaseLoggedOut, nil })
}

// Logout deactivates webhooks, stops the tunnel and forgets the token.
func (c *Core) Logout() {
	c.teardown()
	_ = c.opts.Secrets.Delete(secrets.GitHubToken)
	c.store.Clear()
	c.mu.Lock()
	c.repos = nil
	c.client = nil
	c.mu.Unlock()
	c.opts.Emit(EvtEventsSet, []events.Event{})
	c.opts.Emit(EvtRepos, []gh.Repo{})
	c.update(func(s *State) {
		s.Phase, s.User, s.Orgs = PhaseLoggedOut, nil, nil
		s.App = c0AppState(c.opts.AppSlug)
		s.Hooks, s.BackfillErrors, s.Error = HookSummary{}, nil, ""
		s.Tunnel = TunnelState{Status: "off"}
	})
}

// Shutdown deactivates webhooks (bounded by a short timeout) and closes the tunnel.
func (c *Core) Shutdown() { c.teardown() }

func (c *Core) teardown() {
	c.mu.Lock()
	cancel, client, tun := c.cancel, c.client, c.tun
	c.cancel, c.tun = nil, nil
	results := slices.Clone(c.state.Hooks.Results)
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	if client != nil {
		c.hookRuns.Lock()
		client.DeactivateHooks(ctx, results, apiConcurrency)
		c.hookRuns.Unlock()
	}
	if tun != nil {
		_ = tun.Close(ctx)
	}
}

// activate validates the token and loads everything for a logged-in session.
func (c *Core) activate(tok *oauth2.Token) {
	// Expiring GitHub App user tokens are refreshed transparently; every
	// refresh rotates the refresh token, so the new token is saved.
	client, err := gh.New(auth.TokenSource(c.opts.ClientID, tok, c.saveToken))
	if err != nil {
		c.update(func(s *State) { s.Phase, s.Error = PhaseLoggedOut, err.Error() })
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	user, err := client.Me(ctx)
	if err != nil {
		cancel()
		if gh.IsUnauthorized(err) || auth.IsRefreshFailure(err) {
			_ = c.opts.Secrets.Delete(secrets.GitHubToken)
			c.update(func(s *State) { s.Phase, s.Error = PhaseLoggedOut, "Your GitHub session expired. Please log in again." })
			return
		}
		// Offline or GitHub unavailable: stay logged in, let the user retry.
		c.update(func(s *State) {
			s.Phase, s.Error, s.CanRetry = PhaseLoggedOut, "Could not reach GitHub: "+err.Error(), true
		})
		return
	}
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.client, c.session, c.cancel = client, ctx, cancel
	c.mu.Unlock()
	c.update(func(s *State) {
		s.Phase, s.User, s.Error, s.CanRetry = PhaseReady, &user, "", false
	})
	c.refresh(ctx, client, user)
}

// Refresh reloads orgs, repos and history and re-applies webhooks.
func (c *Core) Refresh() {
	c.mu.Lock()
	ctx, client := c.session, c.client
	var user gh.User
	if c.state.User != nil {
		user = *c.state.User
	}
	c.mu.Unlock()
	if client == nil || ctx == nil || ctx.Err() != nil {
		return
	}
	go c.refresh(ctx, client, user)
}

func (c *Core) refresh(ctx context.Context, client *gh.Client, user gh.User) {
	c.update(func(s *State) { s.ReposLoading = true })

	var (
		orgs     []gh.Org
		repos    []gh.Repo
		insts    []gh.Installation
		orgErr   error
		reposErr error
		instErr  error
		wg       sync.WaitGroup
	)
	wg.Go(func() { orgs, orgErr = client.Orgs(ctx) })
	wg.Go(func() { repos, reposErr = client.Repos(ctx) })
	wg.Go(func() { insts, instErr = client.Installations(ctx) })
	wg.Wait()
	if ctx.Err() != nil {
		return
	}

	c.mu.Lock()
	if reposErr == nil {
		c.repos = repos
	}
	c.mu.Unlock()
	if reposErr == nil {
		c.opts.Emit(EvtRepos, repos)
	}

	app, orgStates := c.installState(user, orgs, insts)
	c.update(func(s *State) {
		s.ReposLoading = false
		s.Orgs = orgStates
		s.App = app
		var msgs []string
		if instErr != nil {
			msgs = append(msgs, "Loading GitHub App installations failed: "+instErr.Error())
		}
		if orgErr != nil {
			msgs = append(msgs, "Loading organizations failed: "+orgErr.Error())
		}
		if reposErr != nil {
			msgs = append(msgs, "Loading repositories failed: "+reposErr.Error())
		}
		s.Error = strings.Join(msgs, "\n")
	})

	c.backfill(ctx, client, user, orgStates, repos)
	c.ensureTunnelAndHooks(ctx, client)
}

// installState combines memberships and app installations.
func (c *Core) installState(user gh.User, orgs []gh.Org, insts []gh.Installation) (AppState, []OrgState) {
	byAccount := map[string]gh.Installation{}
	for _, i := range insts {
		if !i.Suspended {
			byAccount[strings.ToLower(i.Account)] = i
		}
	}
	app := c0AppState(c.opts.AppSlug)
	if i, ok := byAccount[strings.ToLower(user.Login)]; ok {
		app.Installed, app.Selection, app.InstallURL = true, i.Selection, i.URL
	} else {
		app.InstallURL = gh.InstallURL(c.opts.AppSlug, user.ID)
	}

	cfg := c.opts.Config.Get()
	out := make([]OrgState, 0, len(orgs))
	for _, o := range orgs {
		st := OrgState{Org: o, InstallURL: gh.InstallURL(c.opts.AppSlug, o.ID)}
		if i, ok := byAccount[strings.ToLower(o.Login)]; ok {
			st.Installed, st.Selection, st.InstallURL = true, i.Selection, i.URL
		}
		st.Enabled = o.Owner && st.Installed && !slices.Contains(cfg.DisabledOrgs, o.Login)
		out = append(out, st)
	}
	return app, out
}

// backfill rebuilds the history from the Events API.
func (c *Core) backfill(ctx context.Context, client *gh.Client, user gh.User, orgs []OrgState, repos []gh.Repo) {
	src := gh.BackfillSources{Login: user.Login}
	for _, o := range orgs {
		if o.Installed {
			src.Orgs = append(src.Orgs, o.Login)
		}
	}
	for _, r := range repos {
		if r.OwnerIsOrg || r.Archived {
			continue // covered by the org feed
		}
		if len(src.Repos) == maxBackfillRepos {
			break
		}
		src.Repos = append(src.Repos, gh.HookTarget{Owner: r.Owner, Repo: r.Name})
	}
	evts, errs := client.Backfill(ctx, src, apiConcurrency)
	if ctx.Err() != nil {
		return
	}
	c.store.Add(evts...)
	c.opts.Emit(EvtEventsSet, c.store.List())

	c.update(func(s *State) {
		s.BackfillErrors = nil
		for name, err := range errs {
			s.BackfillErrors = append(s.BackfillErrors, err.Error())
			org, ok := strings.CutPrefix(name, "org:")
			if !ok {
				continue
			}
			for i := range s.Orgs {
				if s.Orgs[i].Login == org {
					c.setOrgProblem(&s.Orgs[i], err)
				}
			}
		}
		slices.Sort(s.BackfillErrors)
	})
}

func (c *Core) setOrgProblem(o *OrgState, err error) {
	o.Problem, o.SSOURL = gh.ClassifyAccess(err)
}

// ensureTunnelAndHooks starts ngrok (if configured) and points our webhooks at it.
func (c *Core) ensureTunnelAndHooks(ctx context.Context, client *gh.Client) {
	ngrokTok, _ := c.opts.Secrets.Get(secrets.NgrokToken)
	if ngrokTok == "" {
		c.update(func(s *State) { s.NgrokSet, s.Tunnel = false, TunnelState{Status: "off"} })
		return
	}
	secret, err := secrets.EnsureWebhookSecret(c.opts.Secrets)
	if err != nil {
		c.update(func(s *State) { s.Tunnel = TunnelState{Status: "error", Error: "webhook secret: " + err.Error()} })
		return
	}
	cfg := c.opts.Config.Get()

	c.mu.Lock()
	tun := c.tun
	c.mu.Unlock()
	if tun == nil {
		c.update(func(s *State) { s.NgrokSet, s.Tunnel = true, TunnelState{Status: "connecting"} })
		h := &webhook.Handler{
			Path:    gh.HookPath(cfg.InstallID),
			Secret:  []byte(secret),
			OnEvent: c.onWebhookEvent,
			Log:     c.log,
		}
		tctx, tcancel := context.WithTimeout(ctx, 30*time.Second)
		tun, err = tunnel.Start(tctx, ngrokTok, h)
		tcancel()
		if err != nil {
			c.update(func(s *State) { s.Tunnel = TunnelState{Status: "error", Error: err.Error()} })
			c.retryTunnel(ctx, client)
			return
		}
		c.mu.Lock()
		if ctx.Err() != nil {
			c.mu.Unlock()
			_ = tun.Close(context.Background())
			return
		}
		c.tun = tun
		c.mu.Unlock()
		go c.watchTunnel(ctx, client, tun)
		c.update(func(s *State) { s.Tunnel = TunnelState{Status: "online", URL: tun.URL()} })
	}

	c.ensureHooks(ctx, client, gh.HookSpec{InstallID: cfg.InstallID, PublicURL: tun.URL(), Secret: secret})
}

func (c *Core) ensureHooks(ctx context.Context, client *gh.Client, spec gh.HookSpec) {
	c.hookRuns.Lock()
	defer c.hookRuns.Unlock()
	if ctx.Err() != nil {
		return
	}
	cfg := c.opts.Config.Get()

	c.mu.Lock()
	orgs := slices.Clone(c.state.Orgs)
	repos := slices.Clone(c.repos)
	previous := slices.Clone(c.state.Hooks.Results)
	c.mu.Unlock()

	var targets []gh.HookTarget
	orgHooked := map[string]bool{}
	for _, o := range orgs {
		if o.Enabled {
			targets = append(targets, gh.HookTarget{Org: o.Login})
			orgHooked[o.Login] = true
		}
	}
	if cfg.HookPersonalRepos {
		for _, r := range repos {
			if r.Admin && !r.Archived && !(r.OwnerIsOrg && orgHooked[r.Owner]) {
				targets = append(targets, gh.HookTarget{Owner: r.Owner, Repo: r.Name})
			}
		}
	}

	// Deactivate hooks we no longer want (org disabled, personal repos off).
	var stale []gh.HookResult
	for _, p := range previous {
		if !slices.Contains(targets, p.Target) {
			stale = append(stale, p)
		}
	}
	if len(stale) > 0 {
		client.DeactivateHooks(ctx, stale, apiConcurrency)
	}

	results := client.EnsureHooks(ctx, spec, targets, apiConcurrency)
	if ctx.Err() != nil {
		return
	}
	sum := HookSummary{Results: results}
	for _, r := range results {
		if r.OK {
			sum.OK++
		} else {
			sum.Failed++
		}
	}
	c.update(func(s *State) {
		s.Hooks = sum
		for i := range s.Orgs {
			s.Orgs[i].Hooked = false
			for _, r := range results {
				if r.Target.Org != s.Orgs[i].Login {
					continue
				}
				s.Orgs[i].Hooked = r.OK
				if !r.OK {
					s.Orgs[i].Problem, s.Orgs[i].SSOURL = r.Problem, r.SSOURL
				}
			}
		}
	})
}

func (c *Core) watchTunnel(ctx context.Context, client *gh.Client, tun *tunnel.Tunnel) {
	select {
	case <-ctx.Done():
		return
	case err := <-tun.Done():
		c.mu.Lock()
		if c.tun == tun {
			c.tun = nil
		}
		c.mu.Unlock()
		msg := "tunnel closed"
		if err != nil {
			msg = err.Error()
		}
		c.update(func(s *State) { s.Tunnel = TunnelState{Status: "error", Error: msg} })
		c.retryTunnel(ctx, client)
	}
}

func (c *Core) retryTunnel(ctx context.Context, client *gh.Client) {
	go func() {
		select {
		case <-ctx.Done():
		case <-time.After(tunnelRetry):
			c.ensureTunnelAndHooks(ctx, client)
		}
	}()
}

func (c *Core) onWebhookEvent(e events.Event) {
	added := c.store.Add(e)
	for _, ev := range added {
		c.opts.Emit(EvtNewEvent, ev)
		if c.opts.Notifier != nil {
			c.opts.Notifier.Notify(ev)
		}
	}
}

// SetNgrokToken stores the ngrok authtoken and (re)starts the tunnel.
func (c *Core) SetNgrokToken(tok string) error {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return c.ClearNgrokToken()
	}
	if err := c.opts.Secrets.Set(secrets.NgrokToken, tok); err != nil {
		return err
	}
	c.restartTunnel()
	return nil
}

// ClearNgrokToken removes the ngrok authtoken, deactivates hooks and stops the tunnel.
func (c *Core) ClearNgrokToken() error {
	if err := c.opts.Secrets.Delete(secrets.NgrokToken); err != nil {
		return err
	}
	c.mu.Lock()
	tun, client := c.tun, c.client
	c.tun = nil
	results := slices.Clone(c.state.Hooks.Results)
	c.mu.Unlock()
	ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	if client != nil {
		client.DeactivateHooks(ctx, results, apiConcurrency)
	}
	if tun != nil {
		_ = tun.Close(ctx)
	}
	c.update(func(s *State) { s.NgrokSet, s.Tunnel, s.Hooks = false, TunnelState{Status: "off"}, HookSummary{} })
	return nil
}

func (c *Core) restartTunnel() {
	c.mu.Lock()
	tun, client, ctx := c.tun, c.client, c.session
	c.tun = nil
	c.mu.Unlock()
	if tun != nil {
		_ = tun.Close(context.Background())
	}
	c.update(func(s *State) { s.NgrokSet = true })
	if client != nil && ctx != nil && ctx.Err() == nil {
		go c.ensureTunnelAndHooks(ctx, client)
	}
}

// SetOrgEnabled toggles the org webhook for an organization.
func (c *Core) SetOrgEnabled(org string, enabled bool) error {
	err := c.opts.Config.Update(func(cfg *config.Config) {
		cfg.DisabledOrgs = slices.DeleteFunc(cfg.DisabledOrgs, func(s string) bool { return s == org })
		if !enabled {
			cfg.DisabledOrgs = append(cfg.DisabledOrgs, org)
		}
	})
	if err != nil {
		return err
	}
	c.update(func(s *State) {
		for i := range s.Orgs {
			if s.Orgs[i].Login == org && s.Orgs[i].Owner && s.Orgs[i].Installed {
				s.Orgs[i].Enabled = enabled
			}
		}
	})
	c.reapplyHooks()
	return nil
}

// SetHookPersonalRepos toggles per-repo webhooks.
func (c *Core) SetHookPersonalRepos(on bool) error {
	if err := c.opts.Config.Update(func(cfg *config.Config) { cfg.HookPersonalRepos = on }); err != nil {
		return err
	}
	c.update(func(*State) {})
	c.reapplyHooks()
	return nil
}

func (c *Core) reapplyHooks() {
	c.mu.Lock()
	ctx, client, tun := c.session, c.client, c.tun
	c.mu.Unlock()
	if client == nil || tun == nil || ctx == nil || ctx.Err() != nil {
		return
	}
	secret, err := secrets.EnsureWebhookSecret(c.opts.Secrets)
	if err != nil {
		return
	}
	cfg := c.opts.Config.Get()
	go c.ensureHooks(ctx, client, gh.HookSpec{InstallID: cfg.InstallID, PublicURL: tun.URL(), Secret: secret})
}

// OnWake refreshes the history after the system resumed from sleep.
func (c *Core) OnWake() { c.Refresh() }

// Retry re-reads the stored token and tries to activate the session again
// (e.g. after GitHub was unreachable at startup).
func (c *Core) Retry() { c.Start() }

func c0AppState(slug string) AppState {
	return AppState{
		Slug:              slug,
		AnyInstallURL:     gh.InstallURL(slug, 0),
		AuthorizedAppsURL: "https://github.com/settings/apps/authorizations",
	}
}

// saveToken persists a (possibly refreshed) token in the keychain.
func (c *Core) saveToken(t *oauth2.Token) {
	v, err := auth.Marshal(t)
	if err == nil {
		err = c.opts.Secrets.Set(secrets.GitHubToken, v)
	}
	if err != nil {
		c.log.Warn("saving token to keychain", "err", err)
	}
}
