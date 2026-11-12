package events

import (
	"testing"
	"time"

	"github.com/google/go-github/v93/github"
)

func TestFromPayload(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	ctx := Context{Source: SourceWebhook, Time: now}
	repo := &github.PushEventRepository{FullName: new("octo/app"), HTMLURL: new("https://github.com/octo/app")}
	sender := &github.User{Login: new("mona"), AvatarURL: new("https://avatars/mona")}

	tests := []struct {
		name    string
		payload any
		ok      bool
		kind    Kind
		key     string
		title   string
	}{
		{
			name: "push with commits",
			payload: &github.PushEvent{
				Ref: new("refs/heads/main"), After: new("abc123"), Repo: repo, Sender: sender,
				Commits:    []*github.HeadCommit{{Message: new("one")}, {Message: new("two\n\nbody")}},
				HeadCommit: &github.HeadCommit{Message: new("two\n\nbody")},
				Compare:    new("https://github.com/octo/app/compare/x...abc123"),
			},
			ok: true, kind: KindPush, key: "push:octo/app:refs/heads/main:abc123",
			title: "mona pushed 2 commits to main",
		},
		{
			name:    "tag push is skipped (CreateEvent covers it)",
			payload: &github.PushEvent{Ref: new("refs/tags/v1.0.0"), Created: new(true), Repo: repo, Sender: sender},
		},
		{
			name:    "new branch without commits is skipped",
			payload: &github.PushEvent{Ref: new("refs/heads/feat"), Created: new(true), Repo: repo, Sender: sender},
		},
		{
			name:    "branch deletion is skipped",
			payload: &github.PushEvent{Ref: new("refs/heads/feat"), Deleted: new(true), Repo: repo, Sender: sender},
		},
		{
			name: "tag created",
			payload: &github.CreateEvent{Ref: new("v1.0.0"), RefType: new("tag"),
				Repo: &github.Repository{FullName: new("octo/app")}, Sender: sender},
			ok: true, kind: KindTag, key: "create:octo/app:tag:v1.0.0", title: "mona created tag v1.0.0",
		},
		{
			name: "branch created",
			payload: &github.CreateEvent{Ref: new("feat"), RefType: new("branch"),
				Repo: &github.Repository{FullName: new("octo/app")}, Sender: sender},
			ok: true, kind: KindBranch, key: "create:octo/app:branch:feat", title: "mona created branch feat",
		},
		{
			name: "release published",
			payload: &github.ReleaseEvent{Action: new("published"), Sender: sender,
				Repo:    &github.Repository{FullName: new("octo/app")},
				Release: &github.RepositoryRelease{ID: 42, TagName: "v1.0.0", Name: new("One"), HTMLURL: "https://r"}},
			ok: true, kind: KindRelease, key: "release:octo/app:42", title: "mona published release One",
		},
		{
			name: "release edited is skipped",
			payload: &github.ReleaseEvent{Action: new("edited"),
				Release: &github.RepositoryRelease{ID: 42}},
		},
		{name: "other payloads are skipped", payload: &github.IssuesEvent{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, ok := FromPayload(tt.payload, ctx)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if !ok {
				return
			}
			if e.Kind != tt.kind || e.Key != tt.key || e.Title != tt.title {
				t.Fatalf("got kind=%s key=%s title=%q", e.Kind, e.Key, e.Title)
			}
			if e.Repo != "octo/app" || e.Actor != "mona" || !e.Time.Equal(now) || e.Source != SourceWebhook {
				t.Fatalf("bad envelope: %+v", e)
			}
		})
	}
}

func TestFromPayloadEventsAPIPush(t *testing.T) {
	// The Events API has no repository/sender in the payload and may omit commits.
	e, ok := FromPayload(&github.PushEvent{Ref: new("refs/heads/main"), Head: new("def"), Size: new(3)},
		Context{Source: SourceBackfill, Repo: "octo/app", Actor: "mona", Time: time.Unix(1, 0)})
	if !ok {
		t.Fatal("push from Events API was skipped")
	}
	if e.Key != "push:octo/app:refs/heads/main:def" || e.Commits != 3 || e.URL != "https://github.com/octo/app/commit/def" {
		t.Fatalf("got %+v", e)
	}
}
