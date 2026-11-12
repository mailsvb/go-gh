package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
			 "pushedAt":"2026-11-12T15:03:34Z",
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
	if want := time.Date(2026, 11, 12, 15, 3, 34, 0, time.UTC); !repos[0].PushedAt.Equal(want) {
		t.Errorf("pushedAt = %v, want %v", repos[0].PushedAt, want)
	}
	if r := repos[1]; r.OwnerIsOrg || r.OwnerID != 7 || r.Admin || !r.PushedAt.IsZero() {
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

// activityJSON is a real /repos/{o}/{r}/activity response (shortened SHAs).
const activityJSON = `[
 {"id":6,"activity_type":"push","ref":"refs/heads/main","before":"6ef7efb","after":"706d9cf",
  "timestamp":"2026-11-12T15:03:34Z","actor":{"login":"mona","avatar_url":"https://av/mona"}},
 {"id":5,"activity_type":"branch_deletion","ref":"refs/heads/feature","before":"c5fc689",
  "after":"0000000000000000000000000000000000000000","timestamp":"2026-11-12T13:54:55Z","actor":{"login":"hubot"}},
 {"id":4,"activity_type":"pr_merge","ref":"refs/heads/main","before":"d458b83","after":"6ef7efb",
  "timestamp":"2026-11-12T13:54:52Z","actor":{"login":"hubot"}},
 {"id":3,"activity_type":"branch_creation","ref":"refs/heads/feature",
  "before":"0000000000000000000000000000000000000000","after":"c5fc689","timestamp":"2026-11-12T13:52:55Z","actor":{"login":"hubot"}},
 {"id":2,"activity_type":"force_push","ref":"refs/heads/dev","before":"aaa","after":"bbb",
  "timestamp":"2026-11-12T12:00:00Z","actor":{"login":"mona"}},
 {"id":1,"activity_type":"branch_creation","ref":"refs/tags/v1.0",
  "before":"0000000000000000000000000000000000000000","after":"ccc","timestamp":"2026-11-12T11:00:00Z","actor":{"login":"mona"}}
]`

func TestMapActivities(t *testing.T) {
	var acts []*github.RepositoryActivity
	if err := json.Unmarshal([]byte(activityJSON), &acts); err != nil {
		t.Fatal(err)
	}
	got := MapActivities("octo/app", acts)
	want := []struct{ key, title string }{
		{"push:octo/app:refs/heads/main:706d9cf", "mona pushed to main"},
		{"push:octo/app:refs/heads/main:6ef7efb", "hubot pushed to main"}, // like the webhook push of a merge
		{"create:octo/app:branch:feature", "hubot created branch feature"},
		{"push:octo/app:refs/heads/dev:bbb", "mona pushed to dev"},
		{"create:octo/app:tag:v1.0", "mona created tag v1.0"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events: %+v", len(got), got)
	}
	for i, w := range want {
		if got[i].Key != w.key || got[i].Title != w.title || got[i].Source != events.SourceBackfill {
			t.Errorf("event %d = %q %q %s, want %q %q", i, got[i].Key, got[i].Title, got[i].Source, w.key, w.title)
		}
	}
	if e := got[0]; e.Before != "6ef7efb" || e.Head != "706d9cf" || !e.NeedsCommits() || e.Avatar != "https://av/mona" ||
		!e.Time.Equal(time.Date(2026, 11, 12, 15, 3, 34, 0, time.UTC)) {
		t.Errorf("push = %+v", e)
	}
}

func TestBackfillReadsActivity(t *testing.T) {
	var deniedCalls, compareCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/mona/events", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `[]`) })
	mux.HandleFunc("GET /repos/octo/app/activity", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("per_page") != "100" {
			t.Errorf("per_page = %q", r.URL.Query().Get("per_page"))
		}
		fmt.Fprint(w, activityJSON)
	})
	// An installation without "Contents: read".
	denied := func(w http.ResponseWriter, r *http.Request) {
		deniedCalls.Add(1)
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"Resource not accessible by integration"}`)
	}
	mux.HandleFunc("GET /repos/Locked/a/activity", denied)
	mux.HandleFunc("GET /repos/Locked/b/activity", denied)
	mux.HandleFunc("GET /repos/locked/c/compare/", func(w http.ResponseWriter, r *http.Request) {
		compareCalls.Add(1)
		denied(w, r)
	})
	mux.HandleFunc("GET /repos/broken/x/activity", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"boom"}`, http.StatusInternalServerError)
	})
	c := testClient(t, mux)
	evts, errs := c.Backfill(context.Background(), BackfillSources{
		Login: "mona",
		Activity: []HookTarget{
			{Owner: "octo", Repo: "app"}, {Owner: "Locked", Repo: "a"}, {Owner: "Locked", Repo: "b"}, {Owner: "broken", Repo: "x"},
		},
	}, 1)
	if len(evts) != 5 {
		t.Errorf("events = %d, want 5", len(evts))
	}
	// The permission problem is not an error; other failures are.
	if len(errs) != 1 || errs["activity:broken/x"] == nil {
		t.Errorf("errs = %v", errs)
	}
	if got := c.NoContentsAccess(); !slices.Equal(got, []string{"locked"}) {
		t.Errorf("NoContentsAccess = %v", got)
	}
	// With concurrency 1 the second repo of the denied account is skipped.
	if n := deniedCalls.Load(); n != 1 {
		t.Errorf("denied account was asked %d times, want 1", n)
	}
	// Commit lookups for that account are skipped too.
	out := c.EnrichPushes(context.Background(), []events.Event{{Kind: events.KindPush, Repo: "locked/c", Before: "a", Head: "b"}}, 1)
	if compareCalls.Load() != 0 || out[0].Commits != 0 {
		t.Errorf("compare calls = %d, commits = %d", compareCalls.Load(), out[0].Commits)
	}
}
