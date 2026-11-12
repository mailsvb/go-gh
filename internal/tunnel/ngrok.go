// Package tunnel exposes a local http.Handler on a public ngrok URL.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"golang.ngrok.com/ngrok/v2"
)

// ConnectTimeout bounds how long Start waits for ngrok to come online.
var ConnectTimeout = 30 * time.Second

// Tunnel is a running ngrok endpoint serving an http.Handler.
type Tunnel struct {
	agent      ngrok.Agent
	ln         ngrok.EndpointListener
	srv        *http.Server
	done       chan error
	cancel     context.CancelFunc
	stopParent func() bool
}

// Start connects to ngrok with authtoken and serves h on an ephemeral
// https URL. The tunnel stays up until Close is called or parent is
// cancelled.
//
// ngrok ties the agent session to the context passed to Listen and closes
// the session as soon as that context ends, so the tunnel gets its own
// long-lived context. The connect timeout only applies until the endpoint
// is online.
func Start(parent context.Context, authtoken string, h http.Handler) (*Tunnel, error) {
	if authtoken == "" {
		return nil, errors.New("ngrok authtoken is not set")
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopParent := context.AfterFunc(parent, cancel)
	timedOut := make(chan struct{})
	timer := time.AfterFunc(ConnectTimeout, func() { close(timedOut); cancel() })
	fail := func(err error) (*Tunnel, error) {
		timer.Stop()
		stopParent()
		cancel()
		select {
		case <-timedOut:
			return nil, fmt.Errorf("ngrok did not come online within %s: %w", ConnectTimeout, err)
		default:
			return nil, err
		}
	}

	agent, err := ngrok.NewAgent(
		ngrok.WithAuthtoken(authtoken),
		ngrok.WithAgentDescription("go-gh webhook receiver"),
	)
	if err != nil {
		return fail(err)
	}
	ln, err := agent.Listen(ctx, ngrok.WithDescription("go-gh webhooks"))
	if err != nil {
		_ = agent.Disconnect()
		return fail(err)
	}
	if !timer.Stop() {
		// The timeout fired while Listen was returning; the session is closing.
		_ = ln.Close()
		_ = agent.Disconnect()
		return fail(errors.New("connect timed out"))
	}

	t := &Tunnel{
		agent:      agent,
		ln:         ln,
		srv:        &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second},
		done:       make(chan error, 1),
		cancel:     cancel,
		stopParent: stopParent,
	}
	go func() { t.done <- t.srv.Serve(ln) }()
	return t, nil
}

// URL returns the public base URL, e.g. https://abcd.ngrok-free.app.
func (t *Tunnel) URL() string { return t.ln.URL().String() }

// Done receives the serve error when serving stops.
func (t *Tunnel) Done() <-chan error { return t.done }

// Close stops the server and disconnects from ngrok.
func (t *Tunnel) Close(ctx context.Context) error {
	err := t.srv.Shutdown(ctx)
	_ = t.ln.CloseWithContext(ctx)
	_ = t.agent.Disconnect()
	t.stopParent()
	t.cancel()
	return err
}
