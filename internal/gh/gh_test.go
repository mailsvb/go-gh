package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/go-github/v93/github"
	"golang.org/x/oauth2"

	"go-gh/internal/events"
)

func testClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := newWithBase(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok"}), nil, srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestIsOurHook(t *testing.T) {
	for _, tt := range []struct {
		url  string
		want bool
	}{
		{"https://a1b2.ngrok-free.app/hook/abc", true},
		{"https://other.ngrok-free.app/hook/abc", true}, // host changes per launch
		{"https://a1b2.ngrok-free.app/hook/abcd", false},
		{"https://example.com/webhook", false},
		{"::bad", false},
	} {
		if got := IsOurHook(tt.url, "abc"); got != tt.want {
			t.Errorf("IsOurHook(%q) = %v", tt.url, got)
		}
	}
}

func TestEnsureHooks(t *testing.T) {
	var (
		mu      sync.Mutex
		created []string
		edited  []string
		bodies  []github.Hook
	)
	mux := http.NewServeMux()
	// Repo with our hook already present (old ngrok host).
	mux.HandleFunc("GET /repos/octo/has/hooks", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"id":1,"config":{"url":"https://foreign.example/x"}},{"id":5,"config":{"url":"https://old.ngrok-free.app/hook/inst"}}]`)
	})
	mux.HandleFunc("PATCH /repos/octo/has/hooks/5", func(w http.ResponseWriter, r *http.Request) {
		var h github.Hook
		_ = json.NewDecoder(r.Body).Decode(&h)
		mu.Lock()
		edited = append(edited, "octo/has")
		bodies = append(bodies, h)
		mu.Unlock()
		fmt.Fprint(w, `{"id":5}`)
	})
	// Org without our hook.
	mux.HandleFunc("GET /orgs/acme/hooks", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `[]`) })
	mux.HandleFunc("POST /orgs/acme/hooks", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		created = append(created, "acme")
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":9}`)
	})
	// Org that requires SSO.
	mux.HandleFunc("GET /orgs/sso/hooks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-GitHub-SSO", "required; url=https://github.com/orgs/sso/sso?authorization_request=x")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"Resource protected by organization SAML enforcement."}`)
	})
	// Org where the GitHub App is not installed.
	mux.HandleFunc("GET /orgs/restricted/hooks", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"Resource not accessible by integration"}`)
	})

	c := testClient(t, mux)
	spec := HookSpec{InstallID: "inst", PublicURL: "https://new.ngrok-free.app", Secret: "sec"}
	res := c.EnsureHooks(context.Background(), spec, []HookTarget{
		{Owner: "octo", Repo: "has"}, {Org: "acme"}, {Org: "sso"}, {Org: "restricted"},
	}, 4)

	if !res[0].OK || res[0].HookID != 5 || res[0].Created {
		t.Errorf("repo hook: %+v", res[0])
	}
	if !res[1].OK || res[1].HookID != 9 || !res[1].Created {
		t.Errorf("org hook: %+v", res[1])
	}
	if res[2].OK || res[2].Problem != AccessSSORequired || !strings.HasPrefix(res[2].SSOURL, "https://github.com/orgs/sso/sso") {
		t.Errorf("sso: %+v", res[2])
	}
	if res[3].OK || res[3].Problem != AccessNotInstalled {
		t.Errorf("restricted: %+v", res[3])
	}
	if len(bodies) != 1 {
		t.Fatalf("edits = %v", edited)
	}
	b := bodies[0]
	if b.GetConfig().GetURL() != "https://new.ngrok-free.app/hook/inst" || !b.GetActive() ||
		b.GetConfig().GetSecret() != "sec" || b.GetConfig().GetContentType() != "json" ||
		strings.Join(b.Events, ",") != "push,create,release" {
		t.Errorf("edit body: %+v", b)
	}
	if len(created) != 1 {
		t.Errorf("created = %v", created)
	}
}

func TestDeactivateHooks(t *testing.T) {
	var got atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /repos/octo/a/hooks/1", func(w http.ResponseWriter, r *http.Request) {
		var h github.Hook
		_ = json.NewDecoder(r.Body).Decode(&h)
		if h.Active == nil || *h.Active || h.Config != nil {
			t.Errorf("unexpected body %+v", h)
		}
		got.Add(1)
		fmt.Fprint(w, `{"id":1}`)
	})
	mux.HandleFunc("PATCH /orgs/acme/hooks/2", func(w http.ResponseWriter, r *http.Request) {
		got.Add(1)
		fmt.Fprint(w, `{"id":2}`)
	})
	c := testClient(t, mux)
	c.DeactivateHooks(context.Background(), []HookResult{
		{Target: HookTarget{Owner: "octo", Repo: "a"}, HookID: 1, OK: true},
		{Target: HookTarget{Org: "acme"}, HookID: 2, OK: true},
		{Target: HookTarget{Org: "failed"}, OK: false},
	}, 2)
	if got.Load() != 2 {
		t.Fatalf("deactivated %d hooks, want 2", got.Load())
	}
}

func TestETagTransport(t *testing.T) {
	var hits, notModified atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/mona/events", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("If-None-Match") == `"v1"` {
			notModified.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		fmt.Fprint(w, `[{"id":"1","type":"CreateEvent","repo":{"name":"mona/x"},"actor":{"login":"mona"},
			"created_at":"2026-11-01T10:00:00Z","payload":{"ref":"v1","ref_type":"tag"}},
			{"id":"2","type":"WatchEvent","repo":{"name":"mona/x"},"payload":{}}]`)
	})
	c := testClient(t, mux)
	for i := range 2 {
		evts, errs := c.Backfill(context.Background(), BackfillSources{Login: "mona"}, 1)
		if len(errs) != 0 {
			t.Fatalf("run %d: errs %v", i, errs)
		}
		if len(evts) != 1 || evts[0].Key != "create:mona/x:tag:v1" || evts[0].Actor != "mona" {
			t.Fatalf("run %d: events %+v", i, evts)
		}
	}
	if hits.Load() != 2 || notModified.Load() != 1 {
		t.Fatalf("hits=%d notModified=%d", hits.Load(), notModified.Load())
	}
}

func TestClassifyAccess(t *testing.T) {
	mk := func(code int, msg string) error {
		return &github.ErrorResponse{Response: &http.Response{StatusCode: code, Header: http.Header{}}, Message: msg}
	}
	if p, _ := ClassifyAccess(mk(403, "Resource not accessible by integration")); p != AccessNotInstalled {
		t.Errorf("not installed: %s", p)
	}
	if p, _ := ClassifyAccess(mk(404, "Not Found")); p != AccessForbidden {
		t.Errorf("404: %s", p)
	}
	if p, _ := ClassifyAccess(nil); p != AccessOK {
		t.Errorf("nil: %s", p)
	}
	if !IsUnauthorized(mk(401, "Bad credentials")) {
		t.Error("401 not detected")
	}
}

func TestInstallations(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /user/installations", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"total_count":2,"installations":[
			{"id":1,"account":{"login":"mona","id":10},"target_type":"User","repository_selection":"selected","html_url":"https://github.com/settings/installations/1"},
			{"id":2,"account":{"login":"acme","id":20},"target_type":"Organization","repository_selection":"all","html_url":"https://github.com/organizations/acme/settings/installations/2","suspended_at":"2026-01-01T00:00:00Z"}]}`)
	})
	c := testClient(t, mux)
	is, err := c.Installations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(is) != 2 || is[0].Account != "mona" || is[0].IsOrg || is[0].Selection != "selected" ||
		!is[1].IsOrg || !is[1].Suspended || is[1].AccountID != 20 {
		t.Fatalf("got %+v", is)
	}
	if got := InstallURL("go-gh", 20); got != "https://github.com/apps/go-gh/installations/new/permissions?target_id=20" {
		t.Errorf("InstallURL = %s", got)
	}
	if InstallURL("", 20) != "" {
		t.Error("InstallURL without slug should be empty")
	}
}

func TestReposGraphQL(t *testing.T) {
	var query string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /graphql", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		query = body.Query
		fmt.Fprint(w, `{"data":{"viewer":{"repositories":{"nodes":[
			{"nameWithOwner":"acme/api","name":"api","description":"API","openGraphImageUrl":"https://img/1",
			 "url":"https://github.com/acme/api","isPrivate":true,"isArchived":false,"viewerPermission":"ADMIN",
			 "owner":{"login":"acme","__typename":"Organization","avatarUrl":"https://av/acme","databaseId":42}},
			{"nameWithOwner":"mona/x","name":"x","description":"","openGraphImageUrl":"https://img/2",
			 "url":"https://github.com/mona/x","isPrivate":false,"isArchived":false,"viewerPermission":"WRITE",
			 "owner":{"login":"mona","__typename":"User","avatarUrl":"https://av/mona","databaseId":7}}],
			"pageInfo":{"endCursor":"c1","hasNextPage":false}}}}}`)
	})
	c := testClient(t, mux)
	repos, err := c.Repos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, frag := range []string{"... on Organization{databaseId}", "... on User{databaseId}", "openGraphImageUrl"} {
		if !strings.Contains(query, frag) {
			t.Errorf("query lacks %q: %s", frag, query)
		}
	}
	if len(repos) != 2 {
		t.Fatalf("repos = %+v", repos)
	}
	if r := repos[0]; r.Owner != "acme" || !r.OwnerIsOrg || r.OwnerID != 42 || r.OwnerAvatar != "https://av/acme" || !r.Admin || r.ImageURL != "https://img/1" {
		t.Errorf("org repo = %+v", r)
	}
	if r := repos[1]; r.OwnerIsOrg || r.OwnerID != 7 || r.Admin {
		t.Errorf("user repo = %+v", r)
	}
}

func TestEnrichPushes(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/octo/app/compare/aaa...bbb", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"total_commits":2,"commits":[{"sha":"x","commit":{"message":"first"}},{"sha":"bbb","commit":{"message":"second\n\nbody"}}]}`)
	})
	mux.HandleFunc("GET /repos/octo/gone/compare/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	})
	c := testClient(t, mux)

	push := func(repo, before, head string) events.Event {
		e, _ := events.FromPayload(&github.PushEvent{Ref: new("refs/heads/main"), Before: new(before), Head: new(head)},
			events.Context{Source: events.SourceBackfill, Repo: repo, Actor: "mona"})
		return e
	}
	in := []events.Event{push("octo/app", "aaa", "bbb"), push("octo/gone", "ccc", "ddd"), {Key: "create:x", Kind: events.KindTag}}
	for range 2 { // the second run is served from the cache
		out := c.EnrichPushes(context.Background(), in, 4)
		if out[0].Title != "mona pushed 2 commits to main" || out[0].Detail != "second" || out[0].Commits != 2 {
			t.Fatalf("enriched = %+v", out[0])
		}
		if out[1].Title != "mona pushed to main" || out[2].Key != "create:x" {
			t.Fatalf("unchanged = %+v / %+v", out[1], out[2])
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("compare called %d times, want 1 (cached)", calls.Load())
	}
	if in[0].Commits != 0 {
		t.Fatal("input slice was modified")
	}
}
