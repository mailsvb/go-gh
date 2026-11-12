// Package auth implements GitHub App user authorization via the device flow.
// Only the GitHub App's client ID is embedded in the binary. Device-flow
// tokens can be refreshed without a client secret.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"
)

// ErrNoClientID is returned when the binary was built without a client ID.
var ErrNoClientID = errors.New("no GitHub App client ID configured (build with -ldflags \"-X main.githubClientID=...\" or set GOGH_GITHUB_CLIENT_ID)")

// DeviceCode is shown to the user while the flow is pending.
type DeviceCode struct {
	UserCode        string    `json:"userCode"`
	VerificationURI string    `json:"verificationUri"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

// Flow runs one device authorization.
type Flow struct {
	cfg  *oauth2.Config
	resp *oauth2.DeviceAuthResponse
}

// Config returns the OAuth config for a GitHub App client ID. GitHub Apps
// ignore scopes; access is defined by the app's permissions and installations.
func Config(clientID string) *oauth2.Config {
	ep := endpoints.GitHub
	// Send client_id in the form body; there is no client secret.
	ep.AuthStyle = oauth2.AuthStyleInParams
	return &oauth2.Config{ClientID: clientID, Endpoint: ep}
}

// Start requests a device code from GitHub.
func Start(ctx context.Context, clientID string) (*Flow, DeviceCode, error) {
	if clientID == "" {
		return nil, DeviceCode{}, ErrNoClientID
	}
	cfg := Config(clientID)
	resp, err := cfg.DeviceAuth(ctx)
	if err != nil {
		return nil, DeviceCode{}, Explain(err, clientID)
	}
	return &Flow{cfg: cfg, resp: resp}, DeviceCode{
		UserCode:        resp.UserCode,
		VerificationURI: resp.VerificationURI,
		ExpiresAt:       resp.Expiry,
	}, nil
}

// Wait polls GitHub until the user approved (or denied) the request, the
// code expired, or ctx is cancelled. The token usually expires after 8 hours
// and carries a refresh token valid for about 6 months.
func (f *Flow) Wait(ctx context.Context) (*oauth2.Token, error) {
	tok, err := f.cfg.DeviceAccessToken(ctx, f.resp)
	if err != nil {
		return nil, Explain(err, f.cfg.ClientID)
	}
	return tok, nil
}

// storedToken is the keychain representation of a token.
type storedToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Expiry       time.Time `json:"expiry,omitzero"`
}

// Marshal serializes a token for the keychain.
func Marshal(t *oauth2.Token) (string, error) {
	b, err := json.Marshal(storedToken{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, Expiry: t.Expiry})
	return string(b), err
}

// Unmarshal parses a token stored with Marshal. A bare string is accepted as
// a non-expiring access token.
func Unmarshal(s string) (*oauth2.Token, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("empty token")
	}
	if !strings.HasPrefix(s, "{") {
		return &oauth2.Token{AccessToken: s, TokenType: "Bearer"}, nil
	}
	var st storedToken
	if err := json.Unmarshal([]byte(s), &st); err != nil {
		return nil, err
	}
	if st.AccessToken == "" {
		return nil, errors.New("stored token has no access token")
	}
	return &oauth2.Token{AccessToken: st.AccessToken, RefreshToken: st.RefreshToken, Expiry: st.Expiry, TokenType: "Bearer"}, nil
}

// TokenSource returns a token source that refreshes expired tokens and calls
// save whenever GitHub issued a new token. GitHub rotates refresh tokens on
// every use, so the new one must be persisted or the next refresh fails.
func TokenSource(clientID string, tok *oauth2.Token, save func(*oauth2.Token)) oauth2.TokenSource {
	return &savingSource{
		base: Config(clientID).TokenSource(context.Background(), tok),
		last: tok.AccessToken,
		save: save,
	}
}

type savingSource struct {
	mu   sync.Mutex
	base oauth2.TokenSource
	last string
	save func(*oauth2.Token)
}

func (s *savingSource) Token() (*oauth2.Token, error) {
	t, err := s.base.Token()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	changed := t.AccessToken != s.last
	s.last = t.AccessToken
	s.mu.Unlock()
	if changed && s.save != nil {
		s.save(t)
	}
	return t, nil
}

// IsRefreshFailure reports whether err (possibly wrapped, e.g. in a
// *url.Error from an HTTP client) is a failed token refresh, which means the
// user has to log in again.
func IsRefreshFailure(err error) bool {
	var re *oauth2.RetrieveError
	return errors.As(err, &re)
}

// Explain turns GitHub's OAuth error codes into actionable messages. Other
// errors (network, context) are returned unchanged.
func Explain(err error, clientID string) error {
	var re *oauth2.RetrieveError
	if !errors.As(err, &re) {
		return err
	}
	switch re.ErrorCode {
	case "Not Found", "incorrect_client_credentials", "unauthorized_client":
		return fmt.Errorf("GitHub does not know the client ID %q. Check that the build uses the Client ID of your GitHub App (GH_APP_CLIENT_ID in CI, GOGH_GITHUB_CLIENT_ID locally): %w", clientID, err)
	case "device_flow_disabled":
		return fmt.Errorf("device flow is disabled for this GitHub App. Tick \"Enable Device Flow\" in the app settings on GitHub: %w", err)
	case "access_denied":
		return fmt.Errorf("the authorization was cancelled on GitHub: %w", err)
	case "expired_token":
		return fmt.Errorf("the login code expired. Please start the login again: %w", err)
	case "bad_refresh_token":
		return fmt.Errorf("your GitHub session expired. Please log in again: %w", err)
	}
	return err
}
