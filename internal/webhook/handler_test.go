package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-gh/internal/events"
)

const secret = "s3cr3t"

func sign(body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func deliver(t *testing.T, h http.Handler, path, typ, body, sig string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-GitHub-Event", typ)
	r.Header.Set("X-GitHub-Delivery", "d-1")
	if sig != "" {
		r.Header.Set("X-Hub-Signature-256", sig)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

const (
	pushBody = `{"ref":"refs/heads/main","after":"abc","created":false,"deleted":false,
		"commits":[{"id":"abc","message":"fix bug"}],"head_commit":{"id":"abc","message":"fix bug"},
		"compare":"https://github.com/octo/app/compare/a...abc",
		"repository":{"full_name":"octo/app","html_url":"https://github.com/octo/app"},
		"sender":{"login":"mona"}}`
	tagPushBody = `{"ref":"refs/tags/v1","after":"abc","created":true,"commits":[],
		"repository":{"full_name":"octo/app"},"sender":{"login":"mona"}}`
	createTagBody    = `{"ref":"v1","ref_type":"tag","repository":{"full_name":"octo/app"},"sender":{"login":"mona"}}`
	createBranchBody = `{"ref":"feat","ref_type":"branch","repository":{"full_name":"octo/app"},"sender":{"login":"mona"}}`
	releaseBody      = `{"action":"published","release":{"id":7,"tag_name":"v1","name":"v1","html_url":"https://r"},
		"repository":{"full_name":"octo/app"},"sender":{"login":"mona"}}`
	pingBody = `{"zen":"Keep it logically awesome.","hook_id":99}`
)

func TestHandler(t *testing.T) {
	var got []events.Event
	var pinged int64
	h := &Handler{
		Path:    "/hook/id1",
		Secret:  []byte(secret),
		OnEvent: func(e events.Event) { got = append(got, e) },
		OnPing:  func(id int64) { pinged = id },
	}

	cases := []struct {
		typ, body string
		wantKind  events.Kind // "" = no event
	}{
		{"push", pushBody, events.KindPush},
		{"push", tagPushBody, ""},
		{"create", createTagBody, events.KindTag},
		{"create", createBranchBody, events.KindBranch},
		{"release", releaseBody, events.KindRelease},
		{"issues", `{"action":"opened"}`, ""},
	}
	for _, c := range cases {
		got = nil
		w := deliver(t, h, "/hook/id1", c.typ, c.body, sign(c.body))
		if w.Code != http.StatusNoContent {
			t.Fatalf("%s: status %d", c.typ, w.Code)
		}
		switch {
		case c.wantKind == "" && len(got) != 0:
			t.Fatalf("%s: unexpected event %+v", c.typ, got)
		case c.wantKind != "" && (len(got) != 1 || got[0].Kind != c.wantKind):
			t.Fatalf("%s: got %+v, want kind %s", c.typ, got, c.wantKind)
		case c.wantKind != "" && got[0].Source != events.SourceWebhook:
			t.Fatalf("%s: wrong source", c.typ)
		}
	}

	if w := deliver(t, h, "/hook/id1", "ping", pingBody, sign(pingBody)); w.Code != http.StatusNoContent || pinged != 99 {
		t.Fatalf("ping: status %d hook %d", w.Code, pinged)
	}
}

func TestHandlerRejects(t *testing.T) {
	called := false
	h := &Handler{Path: "/hook/id1", Secret: []byte(secret), OnEvent: func(events.Event) { called = true }}

	if w := deliver(t, h, "/hook/id1", "push", pushBody, sign(pushBody+"x")); w.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature: status %d", w.Code)
	}
	if w := deliver(t, h, "/hook/id1", "push", pushBody, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("missing signature: status %d", w.Code)
	}
	if w := deliver(t, h, "/hook/other", "push", pushBody, sign(pushBody)); w.Code != http.StatusNotFound {
		t.Fatalf("wrong path: status %d", w.Code)
	}
	r := httptest.NewRequest(http.MethodGet, "/hook/id1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: status %d", w.Code)
	}
	if called {
		t.Fatal("OnEvent called for rejected delivery")
	}
}
