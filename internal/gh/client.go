// Package gh wraps the GitHub REST (go-github) and GraphQL (githubv4) APIs.
package gh

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v93/github"
	"github.com/shurcooL/githubv4"
	"golang.org/x/oauth2"
)

// UserAgent is sent with every request.
var UserAgent = "go-gh"

// Client bundles the REST and GraphQL clients that share one authenticated
// HTTP client.
type Client struct {
	REST    *github.Client
	GraphQL *githubv4.Client
	http    *http.Client
}

// New creates a Client that authenticates with tokens from src (which may
// refresh expiring GitHub App user tokens).
func New(src oauth2.TokenSource) (*Client, error) {
	return newWithBase(src, nil, "")
}

// newWithBase allows tests to inject a transport and an API base URL.
func newWithBase(src oauth2.TokenSource, base http.RoundTripper, apiURL string) (*Client, error) {
	if base == nil {
		base = http.DefaultTransport
	}
	hc := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &etagTransport{
			next:  &oauth2.Transport{Source: src, Base: base},
			cache: map[string]cached{},
		},
	}
	opts := []github.ClientOptionsFunc{github.WithHTTPClient(hc), github.WithUserAgent(UserAgent)}
	if apiURL != "" {
		opts = append(opts, github.WithURLs(&apiURL, &apiURL))
	}
	rest, err := github.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	return &Client{REST: rest, GraphQL: githubv4.NewClient(hc), http: hc}, nil
}

// User is the authenticated user shown in the header.
type User struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatarUrl"`
	HTMLURL   string `json:"htmlUrl"`
}

// Me returns the authenticated user.
func (c *Client) Me(ctx context.Context) (User, error) {
	u, _, err := c.REST.Users.Get(ctx, "")
	if err != nil {
		return User{}, err
	}
	return User{
		ID:        u.GetID(),
		Login:     u.GetLogin(),
		Name:      u.GetName(),
		AvatarURL: u.GetAvatarURL(),
		HTMLURL:   u.GetHTMLURL(),
	}, nil
}

// IsUnauthorized reports whether err is a 401 (token revoked/expired).
func IsUnauthorized(err error) bool {
	var er *github.ErrorResponse
	return errors.As(err, &er) && er.Response != nil && er.Response.StatusCode == http.StatusUnauthorized
}

// AccessProblem describes why an organization's resources are not accessible.
type AccessProblem string

const (
	AccessOK           AccessProblem = ""
	AccessSSORequired  AccessProblem = "sso"          // SAML SSO authorization needed
	AccessNotInstalled AccessProblem = "notInstalled" // app not installed there, or lacks a permission
	AccessForbidden    AccessProblem = "forbidden"    // other 403/404
	AccessUnknownError AccessProblem = "error"
)

// ClassifyAccess inspects an API error. url is the SSO authorization URL
// when GitHub provides one.
func ClassifyAccess(err error) (p AccessProblem, url string) {
	if err == nil {
		return AccessOK, ""
	}
	var er *github.ErrorResponse
	if !errors.As(err, &er) || er.Response == nil {
		return AccessUnknownError, ""
	}
	// X-GitHub-SSO: required; url=https://github.com/orgs/x/sso?authorization_request=...
	if h := er.Response.Header.Get("X-GitHub-SSO"); strings.HasPrefix(h, "required") {
		if _, u, ok := strings.Cut(h, "url="); ok {
			url = strings.TrimSpace(u)
		}
		return AccessSSORequired, url
	}
	msg := strings.ToLower(er.Message)
	switch {
	// GitHub App tokens get "Resource not accessible by integration" when the
	// app is not installed on the target or lacks the needed permission.
	case strings.Contains(msg, "not accessible by integration"):
		return AccessNotInstalled, ""
	case er.Response.StatusCode == http.StatusForbidden || er.Response.StatusCode == http.StatusNotFound:
		return AccessForbidden, ""
	}
	return AccessUnknownError, ""
}

// etagTransport caches GET responses in memory and revalidates them with
// If-None-Match. GitHub does not count 304 responses against the rate limit.
// A 304 is turned back into a 200 with the cached body for go-github.
type etagTransport struct {
	next  http.RoundTripper
	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	etag   string
	header http.Header
	body   []byte
}

const maxCachedBody = 2 << 20

func (t *etagTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		return t.next.RoundTrip(req)
	}
	key := req.URL.String()
	t.mu.Lock()
	c, hit := t.cache[key]
	t.mu.Unlock()
	if hit {
		req = req.Clone(req.Context())
		req.Header.Set("If-None-Match", c.etag)
	}
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	switch {
	case hit && resp.StatusCode == http.StatusNotModified:
		resp.Body.Close()
		h := c.header.Clone()
		// keep fresh rate-limit headers
		for k, v := range resp.Header {
			h[k] = v
		}
		return &http.Response{
			Status:        "200 OK",
			StatusCode:    http.StatusOK,
			Proto:         resp.Proto,
			ProtoMajor:    resp.ProtoMajor,
			ProtoMinor:    resp.ProtoMinor,
			Header:        h,
			Body:          io.NopCloser(bytes.NewReader(c.body)),
			ContentLength: int64(len(c.body)),
			Request:       req,
		}, nil
	case resp.StatusCode == http.StatusOK && resp.Header.Get("ETag") != "":
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxCachedBody+1))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if len(body) <= maxCachedBody {
			t.mu.Lock()
			t.cache[key] = cached{etag: resp.Header.Get("ETag"), header: resp.Header.Clone(), body: body}
			t.mu.Unlock()
		}
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return resp, nil
	}
	return resp, nil
}
