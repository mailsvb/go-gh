// Package core orchestrates login, repository listing, event history,
// the ngrok tunnel and webhook management. The Wails services in package
// services are thin wrappers around it.
package core

import (
	"context"
	"errors"
	"fmt"
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
	// maxActivityRepos bounds the per-repository activity requests on each
	// refresh; the most recently pushed repositories are read.
	maxActivityRepos = 30
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
	OK     int `json:"ok"`
	Failed int `json:"failed"`
	// Skipped repositories belong to accounts where the GitHub App is not
	// installed (e.g. public repos of other orgs); hooks there would fail.
	Skipped []string        `json:"skipped"`
	Results []gh.HookResult `json:"results"`
}

// NotificationState is the OS permission for notifications.
type NotificationState struct {
	Checked bool   `json:"checked"`
	Allowed bool   `json:"allowed"`
	Error   string `json:"error"`
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
	// NoContentsAccess lists accounts (lowercase logins) whose go-gh
	// installation lacks "Contents: read": their pushes come only from the
	// Events API, which lags and misses pull request merges.
	NoContentsAccess []string          `json:"noContentsAccess"`
	Notifications    NotificationState `json:"notifications"`
	Error            string            `json:"error"`
	// CanRetry is set when a stored session exists but GitHub was unreachable.
	CanRetry bool `json:"canRetry"`
}

// Snapshot is the complete UI data at one point in time.
type Snapshot struct {
	State  State          `json:"state"`
	Repos  []gh.Repo      `json:"repos"`
	Events []events.Event `json:"events"`
}

// Notifier shows OS notifications for live events.
type Notifier interface {
	Notify(e events.Event)
	// Allowed reports whether the OS lets the app show notifications.
	Allowed() (bool, error)
	// Request asks the OS for permission. The OS prompts only if the user
	// was never asked; otherwise it reports the current setting.
	Request() (bool, error)
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
	// StartTunnel connects the webhook tunnel; nil means ngrok.
	StartTunnel TunnelStarter
}

// Core is the application backend.
type Core struct {
	opts  Options
	log   *slog.Logger
	store *events.Store

	mu     sync.Mutex
	state  State
	client *gh.Client
	repos  []gh.Repo
	// installsKnown is false when listing app installations failed; hooks
	// are then attempted everywhere instead of being filtered.
	installsKnown bool
	lastRefresh   time.Time
	run           *tunnelRun // current tunnel loop, nil when no token
	tun           Tunnel     // current online tunnel
	retryDelay    time.Duration
	session       context.Context // cancelled on logout/shutdown
	cancel        context.CancelFunc
	login         context.CancelFunc
	hookRuns      sync.Mutex // serializes EnsureHooks/Deactivate
}

// New creates a Core. Call Start once the UI is up.
func New(o Options) *Core {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Emit == nil {
		o.Emit = func(string, any) {}
	}
	if o.StartTunnel == nil {
		o.StartTunnel = startNgrok
	}
	c := &Core{opts: o, log: o.Log, store: events.NewStore(events.DefaultCapacity), retryDelay: tunnelRetry}
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
	s.Hooks.Skipped = slices.Clone(s.Hooks.Skipped)
	s.BackfillErrors = slices.Clone(s.BackfillErrors)
	s.NoContentsAccess = slices.Clone(s.NoContentsAccess)
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

// Snapshot returns state, repositories and history together, so the UI can
// initialize without racing the change events.
func (c *Core) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Snapshot{
		State:  c.snapshotLocked(),
		Repos:  slices.Clone(c.repos),
		Events: c.store.List(),
	}
}

// Events returns the in-memory history, newest first.
func (c *Core) Events() []events.Event { return c.store.List() }

// Repos returns the last loaded repository list.
func (c *Core) Repos() []gh.Repo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.repos)
}

// Start connects ngrok if an authtoken is stored and activates the stored
// GitHub session, if any. Both are independent of each other.
func (c *Core) Start() {
	c.connectTunnel()
	c.resume()
}

// resume loads the stored GitHub token and, if present, activates the session.
func (c *Core) resume() {
	stored, err := c.opts.Secrets.Get(secrets.GitHubToken)
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

// Logout deactivates webhooks and forgets the GitHub token. The ngrok
// tunnel stays connected.
func (c *Core) Logout() {
	ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
	c.teardown(ctx)
	done()
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
		s.Hooks, s.BackfillErrors, s.NoContentsAccess, s.Error = HookSummary{}, nil, nil, ""
	})
}

// Shutdown deactivates webhooks and closes the tunnel, bounded by a short timeout.
func (c *Core) Shutdown() {
	ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	c.teardown(ctx)
	c.stopTunnel(ctx)
}

// teardown ends the session and deactivates its webhooks.
func (c *Core) teardown(ctx context.Context) {
	c.mu.Lock()
	cancel, client := c.cancel, c.client
	c.cancel = nil
	results := slices.Clone(c.state.Hooks.Results)
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if client != nil {
		c.hookRuns.Lock()
		client.DeactivateHooks(ctx, results, apiConcurrency)
		c.hookRuns.Unlock()
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
	c.mu.Lock()
	c.lastRefresh = time.Now()
	c.mu.Unlock()
	c.update(func(s *State) { s.ReposLoading = true })

	var (
		orgs       []gh.Org
		publicOrgs []gh.Org
		repos      []gh.Repo
		insts      []gh.Installation
		orgErr     error
		reposErr   error
		instErr    error
		wg         sync.WaitGroup
	)
	wg.Go(func() { orgs, orgErr = client.Orgs(ctx) })
	wg.Go(func() {
		var err error
		if publicOrgs, err = client.PublicOrgs(ctx, user.Login); err != nil {
			c.log.Warn("listing public organizations", "err", err)
		}
	})
	wg.Go(func() { repos, reposErr = client.Repos(ctx) })
	wg.Go(func() { insts, instErr = client.Installations(ctx) })
	wg.Wait()
	if ctx.Err() != nil {
		return
	}

	c.mu.Lock()
	if reposErr == nil {
		c.repos = repos
	} else {
		repos = c.repos // keep the last good list
	}
	c.mu.Unlock()
	if reposErr == nil {
		c.opts.Emit(EvtRepos, repos)
	}

	app, orgStates := c.installState(user, orgSources{
		members: orgs, public: publicOrgs, repos: repos, installs: insts,
	})
	c.mu.Lock()
	c.installsKnown = instErr == nil
	c.mu.Unlock()
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
	c.applyHooks()
}

// orgSources are the places organizations are learned from. With a GitHub
// App token the membership list only contains organizations where the app is
// installed, so the others come from public memberships, the owners of the
// user's repositories and the installations themselves.
type orgSources struct {
	members  []gh.Org // role known
	public   []gh.Org
	repos    []gh.Repo
	installs []gh.Installation
}

// installState combines the organization sources with app installations.
func (c *Core) installState(user gh.User, src orgSources) (AppState, []OrgState) {
	byAccount := map[string]gh.Installation{}
	for _, i := range src.installs {
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

	// Merge, preferring the source with the most information (memberships).
	orgs := map[string]gh.Org{}
	add := func(o gh.Org) {
		k := strings.ToLower(o.Login)
		if k == "" || k == strings.ToLower(user.Login) {
			return
		}
		cur, ok := orgs[k]
		switch {
		case !ok:
			orgs[k] = o
		case o.RoleKnown && !cur.RoleKnown:
			orgs[k] = o
		default:
			if cur.ID == 0 {
				cur.ID = o.ID
			}
			if cur.AvatarURL == "" {
				cur.AvatarURL = o.AvatarURL
			}
			orgs[k] = cur
		}
	}
	for _, o := range src.members {
		add(o)
	}
	for _, o := range src.public {
		add(o)
	}
	for _, r := range src.repos {
		if r.OwnerIsOrg {
			add(gh.Org{ID: r.OwnerID, Login: r.Owner, AvatarURL: r.OwnerAvatar})
		}
	}
	for _, i := range src.installs {
		if i.IsOrg {
			add(gh.Org{ID: i.AccountID, Login: i.Account, AvatarURL: i.Avatar})
		}
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
	// Installed first, then alphabetically.
	slices.SortFunc(out, func(a, b OrgState) int {
		if a.Installed != b.Installed {
			if a.Installed {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Login), strings.ToLower(b.Login))
	})
	return app, out
}

// backfill rebuilds the history from the Events API.
func (c *Core) backfill(ctx context.Context, client *gh.Client, user gh.User, orgs []OrgState, repos []gh.Repo) {
	// Read exactly the repositories and organizations that get webhooks, so
	// the synced history and live events cover the same ground.
	st := c.State()
	cfg := c.opts.Config.Get()
	c.mu.Lock()
	known := c.installsKnown
	c.mu.Unlock()
	targets, _ := hookTargets(targetInput{
		orgs: orgs, repos: repos, userLogin: user.Login,
		userInstalled: st.App.Installed, installsKnown: known, repoHooks: cfg.HookPersonalRepos,
	})

	src := gh.BackfillSources{Login: user.Login}
	feedOrgs := map[string]bool{}
	for _, o := range orgs {
		if o.Installed {
			src.Orgs = append(src.Orgs, o.Login)
			feedOrgs[strings.ToLower(o.Login)] = true
		}
	}
	for _, t := range targets {
		if t.Org != "" || feedOrgs[strings.ToLower(t.Owner)] {
			continue // covered by the org feed
		}
		if len(src.Repos) == maxBackfillRepos {
			break
		}
		src.Repos = append(src.Repos, t)
	}
	src.Activity = activityRepos(targets, repos, maxActivityRepos)
	evts, errs := client.Backfill(ctx, src, apiConcurrency)
	if ctx.Err() != nil {
		return
	}
	if known {
		evts = inScope(evts, targets)
	}
	evts = newest(evts, events.DefaultCapacity)
	// The Events API has no commit details; fetch them so synced pushes read
	// like webhook pushes.
	evts = client.EnrichPushes(ctx, evts, apiConcurrency)
	if ctx.Err() != nil {
		return
	}
	c.store.Add(evts...)
	c.opts.Emit(EvtEventsSet, c.store.List())

	noContents := client.NoContentsAccess()
	if len(noContents) > 0 {
		c.log.Info("history limited: go-gh lacks Contents: read", "accounts", noContents)
	}
	c.update(func(s *State) {
		s.NoContentsAccess = noContents
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

func (c *Core) ensureHooks(ctx context.Context, client *gh.Client, spec gh.HookSpec) {
	c.hookRuns.Lock()
	defer c.hookRuns.Unlock()
	if ctx.Err() != nil {
		return
	}
	cfg := c.opts.Config.Get()

	c.mu.Lock()
	in := targetInput{
		orgs:          slices.Clone(c.state.Orgs),
		repos:         slices.Clone(c.repos),
		installsKnown: c.installsKnown,
		userInstalled: c.state.App.Installed,
		repoHooks:     cfg.HookPersonalRepos,
	}
	if c.state.User != nil {
		in.userLogin = c.state.User.Login
	}
	previous := slices.Clone(c.state.Hooks.Results)
	c.mu.Unlock()

	targets, skipped := hookTargets(in)

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
	failed := 0
	for _, r := range results {
		if !r.OK {
			failed++
			c.log.Warn("webhook failed", "target", r.Target.String(), "problem", r.Problem, "err", r.Error)
		}
	}
	c.log.Info("webhooks applied", "ok", len(results)-failed, "failed", failed, "url", spec.PublicURL)
	if ctx.Err() != nil {
		return
	}
	if len(skipped) > 0 {
		c.log.Info("skipped webhooks where go-gh is not installed", "repos", skipped)
	}
	sum := HookSummary{Results: results, Skipped: skipped}
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

func (c *Core) onWebhookEvent(e events.Event) {
	added := c.store.Add(e)
	for _, ev := range added {
		c.opts.Emit(EvtNewEvent, ev)
		if c.opts.Notifier != nil {
			c.opts.Notifier.Notify(ev)
		}
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

// RefreshIfStale refreshes when the last refresh is older than maxAge, e.g.
// when the window opens after the user installed the app on GitHub.
func (c *Core) RefreshIfStale(maxAge time.Duration) {
	c.mu.Lock()
	stale := time.Since(c.lastRefresh) > maxAge
	c.mu.Unlock()
	if stale {
		c.Refresh()
	}
}

// OnWake refreshes the history after the system resumed from sleep.
func (c *Core) OnWake() { c.Refresh() }

// Retry re-reads the stored token and tries to activate the session again
// (e.g. after GitHub was unreachable at startup).
func (c *Core) Retry() { c.resume() }

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

// CheckNotifications re-reads the OS notification permission (e.g. after the
// user changed it in the system settings).
func (c *Core) CheckNotifications() NotificationState {
	if c.opts.Notifier == nil {
		return NotificationState{}
	}
	ok, err := c.opts.Notifier.Allowed()
	st := NotificationState{Checked: true, Allowed: ok && err == nil}
	if err != nil {
		st.Error = err.Error()
	}
	c.update(func(s *State) { s.Notifications = st })
	return st
}

// EnableNotifications asks the OS for notification permission and returns
// the resulting state. If the user denied it before, no prompt appears and
// the state stays "not allowed"; the UI then opens the system settings.
func (c *Core) EnableNotifications() NotificationState {
	if c.opts.Notifier == nil {
		return NotificationState{}
	}
	if _, err := c.opts.Notifier.Request(); err != nil {
		c.log.Warn("requesting notification permission", "err", err)
	}
	return c.CheckNotifications()
}

// TestNotification shows a sample notification.
func (c *Core) TestNotification() {
	if c.opts.Notifier == nil {
		return
	}
	c.opts.Notifier.Notify(events.Event{
		Key:    fmt.Sprintf("test:%d", time.Now().UnixNano()),
		Kind:   events.KindRelease,
		Repo:   "go-gh",
		Title:  "Notifications are working",
		Detail: "New pushes, tags, branches and releases will show up like this.",
		URL:    "https://github.com/notifications",
		Time:   time.Now(),
	})
}

type targetInput struct {
	orgs          []OrgState
	repos         []gh.Repo
	userLogin     string
	userInstalled bool
	installsKnown bool
	repoHooks     bool // per-repository hooks enabled
}

// hookTargets decides where webhooks go: one org hook per enabled org, plus
// a repo hook for every administered, non-archived repo that is not covered
// by an org hook. Repos on accounts without the GitHub App installed (public
// repos of other orgs) are skipped, because GitHub rejects hooks there.
func hookTargets(in targetInput) (targets []gh.HookTarget, skipped []string) {
	installed := map[string]bool{}
	if in.userInstalled && in.userLogin != "" {
		installed[strings.ToLower(in.userLogin)] = true
	}
	orgHooked := map[string]bool{}
	for _, o := range in.orgs {
		if o.Installed {
			installed[strings.ToLower(o.Login)] = true
		}
		if o.Enabled {
			targets = append(targets, gh.HookTarget{Org: o.Login})
			orgHooked[o.Login] = true
		}
	}
	if !in.repoHooks {
		return targets, nil
	}
	for _, r := range in.repos {
		if !r.Admin || r.Archived || (r.OwnerIsOrg && orgHooked[r.Owner]) {
			continue
		}
		if in.installsKnown && !installed[strings.ToLower(r.Owner)] {
			skipped = append(skipped, r.NameWithOwner)
			continue
		}
		targets = append(targets, gh.HookTarget{Owner: r.Owner, Repo: r.Name})
	}
	return targets, skipped
}

// activityRepos returns up to n repositories covered by targets (repo
// targets, and the repositories of org targets) that were pushed to, most
// recently pushed first.
func activityRepos(targets []gh.HookTarget, repos []gh.Repo, n int) []gh.HookTarget {
	orgs, direct := map[string]bool{}, map[string]bool{}
	for _, t := range targets {
		if t.Org != "" {
			orgs[strings.ToLower(t.Org)] = true
		} else {
			direct[strings.ToLower(t.Owner+"/"+t.Repo)] = true
		}
	}
	var in []gh.Repo
	for _, r := range repos {
		if r.PushedAt.IsZero() {
			continue
		}
		if orgs[strings.ToLower(r.Owner)] || direct[strings.ToLower(r.NameWithOwner)] {
			in = append(in, r)
		}
	}
	slices.SortStableFunc(in, func(a, b gh.Repo) int { return b.PushedAt.Compare(a.PushedAt) })
	out := make([]gh.HookTarget, 0, min(n, len(in)))
	for _, r := range in[:min(n, len(in))] {
		out = append(out, gh.HookTarget{Owner: r.Owner, Repo: r.Name})
	}
	return out
}

// inScope keeps events from repositories that get webhooks: repo targets
// directly, and every repository of an org target.
func inScope(evts []events.Event, targets []gh.HookTarget) []events.Event {
	orgs, repos := map[string]bool{}, map[string]bool{}
	for _, t := range targets {
		if t.Org != "" {
			orgs[strings.ToLower(t.Org)] = true
		} else {
			repos[strings.ToLower(t.Owner+"/"+t.Repo)] = true
		}
	}
	var out []events.Event
	for _, e := range evts {
		owner, _, _ := strings.Cut(strings.ToLower(e.Repo), "/")
		if repos[strings.ToLower(e.Repo)] || orgs[owner] {
			out = append(out, e)
		}
	}
	return out
}

// newest returns at most n events, newest first, without duplicate keys.
func newest(evts []events.Event, n int) []events.Event {
	out := slices.Clone(evts)
	slices.SortStableFunc(out, func(a, b events.Event) int { return b.Time.Compare(a.Time) })
	seen := map[string]bool{}
	out = slices.DeleteFunc(out, func(e events.Event) bool {
		if seen[e.Key] {
			return true
		}
		seen[e.Key] = true
		return false
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}
