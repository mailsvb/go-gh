package gh

import (
	"context"
	"net/url"
	"sync"

	"github.com/google/go-github/v93/github"
)

// HookEvents are the webhook events the app subscribes to.
var HookEvents = []string{"push", "create", "release"}

// HookPath returns the webhook path for an installation.
func HookPath(installID string) string { return "/hook/" + installID }

// IsOurHook reports whether a webhook URL belongs to this installation. Only
// the path is compared because the ngrok host changes on every launch.
func IsOurHook(hookURL, installID string) bool {
	u, err := url.Parse(hookURL)
	if err != nil {
		return false
	}
	return u.Path == HookPath(installID)
}

// HookTarget is an organization (Org set) or a repository (Owner+Repo set).
type HookTarget struct {
	Org   string `json:"org,omitempty"`
	Owner string `json:"owner,omitempty"`
	Repo  string `json:"repo,omitempty"`
}

// String returns "org:<login>" or "owner/repo".
func (t HookTarget) String() string {
	if t.Org != "" {
		return "org:" + t.Org
	}
	return t.Owner + "/" + t.Repo
}

// HookResult is the outcome of ensuring one webhook.
type HookResult struct {
	Target  HookTarget    `json:"target"`
	HookID  int64         `json:"hookId"`
	OK      bool          `json:"ok"`
	Created bool          `json:"created"`
	Problem AccessProblem `json:"problem,omitempty"`
	SSOURL  string        `json:"ssoUrl,omitempty"`
	Error   string        `json:"error,omitempty"`
}

// HookSpec is what EnsureHooks installs.
type HookSpec struct {
	InstallID string
	PublicURL string // e.g. https://abc.ngrok-free.app
	Secret    string
}

func (s HookSpec) hook(active bool) *github.Hook {
	return &github.Hook{
		Name:   new("web"),
		Active: new(active),
		Events: HookEvents,
		Config: &github.HookConfig{
			URL:         new(s.PublicURL + HookPath(s.InstallID)),
			ContentType: new("json"),
			InsecureSSL: new("0"),
			Secret:      new(s.Secret),
		},
	}
}

// EnsureHooks creates or updates this installation's webhook on every target,
// running at most concurrency requests at once.
func (c *Client) EnsureHooks(ctx context.Context, spec HookSpec, targets []HookTarget, concurrency int) []HookResult {
	return parallel(targets, concurrency, func(t HookTarget) HookResult {
		return c.ensureHook(ctx, spec, t)
	})
}

func (c *Client) ensureHook(ctx context.Context, spec HookSpec, t HookTarget) HookResult {
	res := HookResult{Target: t}
	fail := func(err error) HookResult {
		res.Problem, res.SSOURL = ClassifyAccess(err)
		res.Error = err.Error()
		return res
	}

	existing, err := c.findHook(ctx, spec.InstallID, t)
	if err != nil {
		return fail(err)
	}
	var h *github.Hook
	if existing != 0 {
		if t.Org != "" {
			h, _, err = c.REST.Organizations.EditHook(ctx, t.Org, existing, spec.hook(true))
		} else {
			h, _, err = c.REST.Repositories.EditHook(ctx, t.Owner, t.Repo, existing, spec.hook(true))
		}
	} else {
		res.Created = true
		if t.Org != "" {
			h, _, err = c.REST.Organizations.CreateHook(ctx, t.Org, spec.hook(true))
		} else {
			h, _, err = c.REST.Repositories.CreateHook(ctx, t.Owner, t.Repo, spec.hook(true))
		}
	}
	if err != nil {
		return fail(err)
	}
	res.HookID = h.GetID()
	res.OK = true
	return res
}

func (c *Client) findHook(ctx context.Context, installID string, t HookTarget) (int64, error) {
	opts := &github.ListOptions{PerPage: 100}
	for {
		var (
			hooks []*github.Hook
			resp  *github.Response
			err   error
		)
		if t.Org != "" {
			hooks, resp, err = c.REST.Organizations.ListHooks(ctx, t.Org, opts)
		} else {
			hooks, resp, err = c.REST.Repositories.ListHooks(ctx, t.Owner, t.Repo, opts)
		}
		if err != nil {
			return 0, err
		}
		for _, h := range hooks {
			if h.GetConfig() != nil && IsOurHook(h.GetConfig().GetURL(), installID) {
				return h.GetID(), nil
			}
		}
		if resp.NextPage == 0 {
			return 0, nil
		}
		opts.Page = resp.NextPage
	}
}

// DeactivateHooks sets active=false on the given hooks so GitHub stops
// delivering to an ngrok URL that no longer exists.
func (c *Client) DeactivateHooks(ctx context.Context, hooks []HookResult, concurrency int) {
	var ok []HookResult
	for _, h := range hooks {
		if h.OK && h.HookID != 0 {
			ok = append(ok, h)
		}
	}
	parallel(ok, concurrency, func(h HookResult) struct{} {
		body := &github.Hook{Active: new(false)}
		if h.Target.Org != "" {
			_, _, _ = c.REST.Organizations.EditHook(ctx, h.Target.Org, h.HookID, body)
		} else {
			_, _, _ = c.REST.Repositories.EditHook(ctx, h.Target.Owner, h.Target.Repo, h.HookID, body)
		}
		return struct{}{}
	})
}

func parallel[T, R any](in []T, n int, fn func(T) R) []R {
	if n <= 0 {
		n = 1
	}
	out := make([]R, len(in))
	sem := make(chan struct{}, n)
	var wg sync.WaitGroup
	for i, v := range in {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			out[i] = fn(v)
		}()
	}
	wg.Wait()
	return out
}
