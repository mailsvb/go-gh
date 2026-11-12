package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go-gh/internal/config"
	"go-gh/internal/secrets"
)

// syncStore is a concurrency-safe in-memory secrets.Store.
type syncStore struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *syncStore) Get(k string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[k], nil
}
func (s *syncStore) Set(k, v string) error { s.mu.Lock(); defer s.mu.Unlock(); s.m[k] = v; return nil }
func (s *syncStore) Delete(k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, k)
	return nil
}

type fakeTunnel struct {
	url    string
	done   chan error
	closed chan struct{}
	once   sync.Once
}

func (t *fakeTunnel) URL() string        { return t.url }
func (t *fakeTunnel) Done() <-chan error { return t.done }
func (t *fakeTunnel) Close(context.Context) error {
	t.once.Do(func() { close(t.closed) })
	return nil
}

func (t *fakeTunnel) isClosed() bool {
	select {
	case <-t.closed:
		return true
	default:
		return false
	}
}

// fakeNgrok records started tunnels; fail makes the next starts fail.
type fakeNgrok struct {
	mu      sync.Mutex
	tunnels []*fakeTunnel
	tokens  []string
	fail    error
}

func (f *fakeNgrok) start(_ context.Context, tok string, _ http.Handler) (Tunnel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = append(f.tokens, tok)
	if f.fail != nil {
		return nil, f.fail
	}
	// ngrok allows only one agent session: the previous tunnel must be closed.
	for _, t := range f.tunnels {
		if !t.isClosed() {
			return nil, errors.New("previous tunnel still open")
		}
	}
	t := &fakeTunnel{
		url:    fmt.Sprintf("https://t%d.ngrok.test", len(f.tunnels)+1),
		done:   make(chan error, 1),
		closed: make(chan struct{}),
	}
	f.tunnels = append(f.tunnels, t)
	return t, nil
}

func (f *fakeNgrok) last() *fakeTunnel {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.tunnels) == 0 {
		return nil
	}
	return f.tunnels[len(f.tunnels)-1]
}

func newTunnelCore(t *testing.T, ngrokToken string) (*Core, *fakeNgrok, *syncStore) {
	t.Helper()
	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	store := &syncStore{m: map[string]string{}}
	if ngrokToken != "" {
		store.m[secrets.NgrokToken] = ngrokToken
	}
	f := &fakeNgrok{}
	c := New(Options{ClientID: "id", Secrets: store, Config: cfg, StartTunnel: f.start})
	c.retryDelay = 10 * time.Millisecond
	t.Cleanup(c.Shutdown)
	return c, f, store
}

// waitTunnel waits until the tunnel state matches status (and url, if set).
func waitTunnel(t *testing.T, c *Core, status, url string) TunnelState {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		ts := c.State().Tunnel
		if ts.Status == status && (url == "" || ts.URL == url) {
			return ts
		}
		if time.Now().After(deadline) {
			t.Fatalf("tunnel state = %+v, want status %q url %q", ts, status, url)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTunnelConnectsBeforeLogin(t *testing.T) {
	c, f, _ := newTunnelCore(t, "tok")
	c.Start()
	waitTunnel(t, c, "online", "https://t1.ngrok.test")
	st := c.State()
	if st.Phase != PhaseLoggedOut || !st.NgrokSet {
		t.Errorf("phase=%s ngrokSet=%v, want loggedOut and set", st.Phase, st.NgrokSet)
	}

	// Logging out keeps the tunnel connected.
	c.Logout()
	if st := c.State().Tunnel; st.Status != "online" || f.last().isClosed() {
		t.Errorf("after logout: tunnel %+v, closed=%v", st, f.last().isClosed())
	}
}

func TestTunnelOffWithoutToken(t *testing.T) {
	c, f, _ := newTunnelCore(t, "")
	c.Start()
	if st := c.State(); st.NgrokSet || st.Tunnel.Status != "off" {
		t.Errorf("state = ngrokSet %v tunnel %+v", st.NgrokSet, st.Tunnel)
	}
	if f.last() != nil {
		t.Error("tunnel started without a token")
	}
}

func TestTunnelReconnectsAfterFailure(t *testing.T) {
	c, f, _ := newTunnelCore(t, "tok")
	f.fail = errors.New("bad token")
	c.Start()
	ts := waitTunnel(t, c, "error", "")
	if ts.Error != "bad token" {
		t.Errorf("error = %q", ts.Error)
	}
	f.mu.Lock()
	f.fail = nil
	f.mu.Unlock()
	waitTunnel(t, c, "online", "https://t1.ngrok.test")

	// The tunnel dies: error, then a fresh tunnel.
	f.last().done <- errors.New("session closed")
	waitTunnel(t, c, "online", "https://t2.ngrok.test")
	f.mu.Lock()
	first := f.tunnels[0]
	f.mu.Unlock()
	if !first.isClosed() {
		t.Error("dead tunnel was not closed")
	}
}

func TestClearNgrokToken(t *testing.T) {
	c, f, store := newTunnelCore(t, "tok")
	c.Start()
	waitTunnel(t, c, "online", "")
	tun := f.last()

	if err := c.ClearNgrokToken(); err != nil {
		t.Fatal(err)
	}
	st := c.State()
	if st.NgrokSet || st.Tunnel.Status != "off" || st.Tunnel.URL != "" {
		t.Errorf("state = ngrokSet %v tunnel %+v", st.NgrokSet, st.Tunnel)
	}
	if !tun.isClosed() {
		t.Error("tunnel not closed")
	}
	if v, _ := store.Get(secrets.NgrokToken); v != "" {
		t.Error("token not deleted")
	}
}

func TestSetNgrokTokenReplacesTunnel(t *testing.T) {
	c, f, store := newTunnelCore(t, "")
	c.Start()
	if err := c.SetNgrokToken(" first "); err != nil {
		t.Fatal(err)
	}
	waitTunnel(t, c, "online", "https://t1.ngrok.test")
	if v, _ := store.Get(secrets.NgrokToken); v != "first" {
		t.Errorf("stored token = %q", v)
	}
	// The fake fails the start if the old tunnel is still open.
	if err := c.SetNgrokToken("second"); err != nil {
		t.Fatal(err)
	}
	waitTunnel(t, c, "online", "https://t2.ngrok.test")
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.tunnels[0].isClosed() || f.tokens[len(f.tokens)-1] != "second" {
		t.Errorf("old closed=%v tokens=%v", f.tunnels[0].isClosed(), f.tokens)
	}
}
