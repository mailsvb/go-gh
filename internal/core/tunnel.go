package core

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"go-gh/internal/gh"
	"go-gh/internal/secrets"
	"go-gh/internal/tunnel"
	"go-gh/internal/webhook"
)

// The ngrok tunnel lives independently of the GitHub session: it connects
// as soon as an authtoken is stored (also before login), stays up across
// logout, and stops only when the token is removed or the app quits.
// Webhooks are applied whenever both the tunnel and a session are up.

// Tunnel is a running public endpoint (see package tunnel).
type Tunnel interface {
	URL() string
	// Done receives an error when the tunnel stops serving.
	Done() <-chan error
	Close(ctx context.Context) error
}

// TunnelStarter connects a tunnel that serves h. It stays up until Close
// is called or ctx is cancelled.
type TunnelStarter func(ctx context.Context, authtoken string, h http.Handler) (Tunnel, error)

func startNgrok(ctx context.Context, authtoken string, h http.Handler) (Tunnel, error) {
	t, err := tunnel.Start(ctx, authtoken, h)
	if err != nil {
		return nil, err // not a typed nil inside the interface
	}
	return t, nil
}

// tunnelRun is one connect-and-reconnect loop for one authtoken.
type tunnelRun struct {
	cancel  context.CancelFunc
	stopped chan struct{} // closed once the loop closed its tunnel and exited
}

// connectTunnel (re)starts the tunnel with the stored authtoken, or turns it
// off when none is stored. A previous loop is stopped first.
func (c *Core) connectTunnel() {
	tok, err := c.opts.Secrets.Get(secrets.NgrokToken)
	if err != nil {
		c.log.Warn("reading ngrok authtoken from keychain", "err", err)
	}
	var (
		run *tunnelRun
		ctx context.Context
	)
	if tok != "" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(context.Background())
		run = &tunnelRun{cancel: cancel, stopped: make(chan struct{})}
	}
	var prev *tunnelRun
	c.update(func(s *State) {
		prev, c.run = c.run, run
		s.NgrokSet = run != nil
		if run != nil {
			s.Tunnel = TunnelState{Status: "connecting"}
		} else {
			s.Tunnel = TunnelState{Status: "off"}
		}
	})
	if prev != nil {
		prev.cancel()
	}
	if run != nil {
		go c.runTunnel(ctx, run, prev, tok)
	}
}

// stopTunnel stops the tunnel loop and waits (bounded by ctx) until the
// tunnel is closed.
func (c *Core) stopTunnel(ctx context.Context) {
	var run *tunnelRun
	c.update(func(s *State) {
		run, c.run = c.run, nil
		s.Tunnel = TunnelState{Status: "off"}
	})
	if run == nil {
		return
	}
	run.cancel()
	select {
	case <-run.stopped:
	case <-ctx.Done():
	}
}

// runTunnel connects, serves until the tunnel dies, and reconnects after a
// delay, until ctx is cancelled.
func (c *Core) runTunnel(ctx context.Context, run, prev *tunnelRun, tok string) {
	defer close(run.stopped)
	// ngrok limits concurrent agent sessions; let the old one close first.
	if prev != nil {
		select {
		case <-prev.stopped:
		case <-ctx.Done():
			return
		}
	}
	secret, err := secrets.EnsureWebhookSecret(c.opts.Secrets)
	if err != nil {
		c.setTunnel(run, TunnelState{Status: "error", Error: "webhook secret: " + err.Error()})
		return
	}
	h := &webhook.Handler{
		Path:    gh.HookPath(c.opts.Config.Get().InstallID),
		Secret:  []byte(secret),
		OnEvent: c.onWebhookEvent,
		Log:     c.log,
	}
	for {
		c.setTunnel(run, TunnelState{Status: "connecting"})
		tun, err := c.opts.StartTunnel(ctx, tok, h)
		if err == nil {
			err = c.serveTunnel(ctx, run, tun)
		}
		if ctx.Err() != nil {
			return
		}
		c.log.Error("ngrok tunnel failed", "err", err)
		c.setTunnel(run, TunnelState{Status: "error", Error: err.Error()})
		select {
		case <-ctx.Done():
			return
		case <-time.After(c.retryDelay):
		}
	}
}

// serveTunnel publishes tun as the current tunnel, points the webhooks at
// it and blocks until it dies (returning why) or ctx is cancelled (nil).
// The tunnel is closed on return.
func (c *Core) serveTunnel(ctx context.Context, run *tunnelRun, tun Tunnel) error {
	defer func() {
		c.mu.Lock()
		if c.tun == tun {
			c.tun = nil
		}
		c.mu.Unlock()
		closeCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = tun.Close(closeCtx)
	}()
	c.mu.Lock()
	if ctx.Err() != nil {
		c.mu.Unlock()
		return nil
	}
	c.tun = tun
	c.mu.Unlock()
	c.log.Info("ngrok tunnel online", "url", tun.URL())
	c.setTunnel(run, TunnelState{Status: "online", URL: tun.URL()})
	c.reapplyHooks()

	select {
	case <-ctx.Done():
		return nil
	case err := <-tun.Done():
		if err == nil {
			err = errors.New("tunnel closed")
		}
		return err
	}
}

// setTunnel updates the tunnel state unless run was replaced meanwhile.
func (c *Core) setTunnel(run *tunnelRun, ts TunnelState) {
	c.update(func(s *State) {
		if c.run == run {
			s.Tunnel = ts
		}
	})
}

// SetNgrokToken stores the ngrok authtoken and (re)connects the tunnel.
func (c *Core) SetNgrokToken(tok string) error {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return c.ClearNgrokToken()
	}
	if err := c.opts.Secrets.Set(secrets.NgrokToken, tok); err != nil {
		return err
	}
	c.connectTunnel()
	return nil
}

// ClearNgrokToken removes the ngrok authtoken, deactivates the webhooks and
// disconnects the tunnel.
func (c *Core) ClearNgrokToken() error {
	if err := c.opts.Secrets.Delete(secrets.NgrokToken); err != nil {
		return err
	}
	ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	c.mu.Lock()
	client := c.client
	results := slices.Clone(c.state.Hooks.Results)
	c.mu.Unlock()
	if client != nil {
		c.hookRuns.Lock()
		client.DeactivateHooks(ctx, results, apiConcurrency)
		c.hookRuns.Unlock()
	}
	c.stopTunnel(ctx)
	c.update(func(s *State) { s.NgrokSet, s.Hooks = false, HookSummary{} })
	return nil
}

// reapplyHooks points the webhooks at the current tunnel in the background.
func (c *Core) reapplyHooks() { go c.applyHooks() }

// applyHooks points the webhooks at the current tunnel. It does nothing
// until both a session and the tunnel are up; whichever comes second
// applies them.
func (c *Core) applyHooks() {
	c.mu.Lock()
	ctx, client, tun := c.session, c.client, c.tun
	c.mu.Unlock()
	if client == nil || tun == nil || ctx == nil || ctx.Err() != nil {
		return
	}
	secret, err := secrets.EnsureWebhookSecret(c.opts.Secrets)
	if err != nil {
		c.log.Warn("reading webhook secret", "err", err)
		return
	}
	cfg := c.opts.Config.Get()
	c.ensureHooks(ctx, client, gh.HookSpec{InstallID: cfg.InstallID, PublicURL: tun.URL(), Secret: secret})
}
