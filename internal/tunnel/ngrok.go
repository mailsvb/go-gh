// Package tunnel exposes a local http.Handler on a public ngrok URL.
package tunnel

import (
	"context"
	"errors"
	"net/http"
	"time"

	"golang.ngrok.com/ngrok/v2"
)

// Tunnel is a running ngrok endpoint serving an http.Handler.
type Tunnel struct {
	agent ngrok.Agent
	ln    ngrok.EndpointListener
	srv   *http.Server
	done  chan error
}

// Start connects to ngrok with authtoken and serves h on an ephemeral
// https URL.
func Start(ctx context.Context, authtoken string, h http.Handler) (*Tunnel, error) {
	if authtoken == "" {
		return nil, errors.New("ngrok authtoken is not set")
	}
	agent, err := ngrok.NewAgent(
		ngrok.WithAuthtoken(authtoken),
		ngrok.WithAgentDescription("go-gh webhook receiver"),
	)
	if err != nil {
		return nil, err
	}
	ln, err := agent.Listen(ctx, ngrok.WithDescription("go-gh webhooks"))
	if err != nil {
		_ = agent.Disconnect()
		return nil, err
	}
	t := &Tunnel{
		agent: agent,
		ln:    ln,
		srv:   &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second},
		done:  make(chan error, 1),
	}
	go func() { t.done <- t.srv.Serve(ln) }()
	return t, nil
}

// URL returns the public base URL, e.g. https://abcd.ngrok-free.app.
func (t *Tunnel) URL() string { return t.ln.URL().String() }

// Done is closed (receives) when serving stops.
func (t *Tunnel) Done() <-chan error { return t.done }

// Close stops the server and disconnects from ngrok.
func (t *Tunnel) Close(ctx context.Context) error {
	err := t.srv.Shutdown(ctx)
	_ = t.ln.CloseWithContext(ctx)
	_ = t.agent.Disconnect()
	return err
}
