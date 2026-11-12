// Package events holds the normalized event model shared by the webhook
// receiver, the Events API backfill and the UI.
package events

import "time"

// Kind is the type of a repository event shown to the user.
type Kind string

const (
	KindPush    Kind = "push"
	KindTag     Kind = "tag"
	KindBranch  Kind = "branch"
	KindRelease Kind = "release"
)

// Source tells where an event came from.
type Source string

const (
	SourceWebhook  Source = "webhook"
	SourceBackfill Source = "backfill"
)

// Event is a normalized repository event.
type Event struct {
	// Key identifies the underlying change independent of its source, so the
	// same change seen via webhook and via the Events API is deduplicated.
	Key     string    `json:"key"`
	Kind    Kind      `json:"kind"`
	Repo    string    `json:"repo"` // owner/name
	Ref     string    `json:"ref"`  // short ref name (branch or tag) or release tag
	Actor   string    `json:"actor"`
	Avatar  string    `json:"avatar"`
	Title   string    `json:"title"`
	Detail  string    `json:"detail"`
	URL     string    `json:"url"`
	Time    time.Time `json:"time"`
	Source  Source    `json:"source"`
	Commits int       `json:"commits"`
}

// Short returns the notification headline, e.g. "octo/repo · tag".
func (e Event) Short() string {
	return e.Repo + " · " + string(e.Kind)
}
