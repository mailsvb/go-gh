package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestDeviceAuthErrorsAreExplained(t *testing.T) {
	for _, tt := range []struct {
		status int
		body   string
		want   string
	}{
		// What github.com/login/device/code returns for an unknown client_id.
		{http.StatusNotFound, `{"error":"Not Found"}`, `does not know the client ID "Iv1.test"`},
		{http.StatusBadRequest, `{"error":"device_flow_disabled"}`, `Enable Device Flow`},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tt.status)
			_, _ = w.Write([]byte(tt.body))
		}))
		cfg := &oauth2.Config{ClientID: "Iv1.test", Endpoint: oauth2.Endpoint{DeviceAuthURL: srv.URL}}
		_, err := cfg.DeviceAuth(context.Background())
		srv.Close()
		if err == nil {
			t.Fatalf("%s: no error", tt.body)
		}
		if got := Explain(err, "Iv1.test").Error(); !strings.Contains(got, tt.want) {
			t.Errorf("%s: got %q, want it to contain %q", tt.body, got, tt.want)
		}
	}
}

func TestStartWithoutClientID(t *testing.T) {
	if _, _, err := Start(context.Background(), ""); err != ErrNoClientID {
		t.Fatalf("err = %v, want ErrNoClientID", err)
	}
}

func TestTokenRoundTrip(t *testing.T) {
	in := &oauth2.Token{AccessToken: "a", RefreshToken: "r", Expiry: time.Date(2026, 11, 12, 18, 0, 0, 0, time.UTC)}
	s, err := Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Unmarshal(s)
	if err != nil || out.AccessToken != "a" || out.RefreshToken != "r" || !out.Expiry.Equal(in.Expiry) {
		t.Fatalf("got %+v, %v", out, err)
	}
	if out, _ := Unmarshal("gho_plain"); out.AccessToken != "gho_plain" || out.RefreshToken != "" {
		t.Fatalf("plain token: %+v", out)
	}
	if _, err := Unmarshal(""); err == nil {
		t.Fatal("empty token accepted")
	}
}

func TestTokenSourceRefreshesWithoutSecretAndSaves(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new","refresh_token":"r2","expires_in":28800,"token_type":"bearer"}`))
	}))
	defer srv.Close()

	var saved []*oauth2.Token
	expired := &oauth2.Token{AccessToken: "old", RefreshToken: "r1", Expiry: time.Now().Add(-time.Minute)}
	ts := TokenSource("Iv1.client", expired, func(t *oauth2.Token) { saved = append(saved, t) })
	// Point the refresh at the test server.
	ts.(*savingSource).base = func() oauth2.TokenSource {
		cfg := Config("Iv1.client")
		cfg.Endpoint.TokenURL = srv.URL
		return cfg.TokenSource(context.Background(), expired)
	}()

	tok, err := ts.Token()
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "new" || len(saved) != 1 || saved[0].RefreshToken != "r2" {
		t.Fatalf("tok=%+v saved=%v", tok, saved)
	}
	if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "r1" ||
		form.Get("client_id") != "Iv1.client" || form.Has("client_secret") {
		t.Fatalf("refresh request form = %v", form)
	}
	// A cached, still-valid token is not saved again.
	if _, err := ts.Token(); err != nil || len(saved) != 1 {
		t.Fatalf("second Token(): saved %d times, err %v", len(saved), err)
	}
}
