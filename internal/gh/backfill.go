package gh

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v93/github"

	"go-gh/internal/events"
)

// BackfillSources selects the feeds used to rebuild the history.
//
// The Events API lags by up to several hours and does not record every push
// (a pull request merge only appears as a PullRequestEvent), so pushes and
// new branches are read from each repository's activity log, which lists
// every ref update right away. Tags and releases still come from the Events
// API feeds, which also cover pushes of repositories not listed in Activity.
type BackfillSources struct {
	Login    string       // the authenticated user
	Orgs     []string     // organizations: /users/{login}/events/orgs/{org}
	Repos    []HookTarget // repositories outside those orgs: /repos/{o}/{r}/events
	Activity []HookTarget // repositories whose ref updates are read: /repos/{o}/{r}/activity
}

// Backfill reads recent events and maps the relevant ones (push, tag/branch
// create, release published). The same change seen by several feeds has the
// same Event.Key; callers deduplicate. Accounts whose installation lacks
// "Contents: read" are skipped for the activity log and reported by
// NoContentsAccess instead of as errors. Errors of individual feeds are
// returned keyed by feed name ("user:<login>", "org:<org>", "owner/repo",
// "activity:owner/repo") alongside whatever could be read.
func (c *Client) Backfill(ctx context.Context, src BackfillSources, concurrency int) ([]events.Event, map[string]error) {
	// One page of 100 per feed is enough to fill a 100-event history.
	page := &github.ListOptions{PerPage: 100}
	eventFeed := func(list func() ([]*github.Event, *github.Response, error)) func() ([]events.Event, error) {
		return func() ([]events.Event, error) {
			raw, _, err := list()
			return MapEvents(raw), err
		}
	}
	type feed struct {
		name string
		read func() ([]events.Event, error)
	}
	feeds := []feed{{
		name: "user:" + src.Login,
		read: eventFeed(func() ([]*github.Event, *github.Response, error) {
			return c.REST.Activity.ListEventsPerformedByUser(ctx, src.Login, false, page)
		}),
	}}
	for _, org := range src.Orgs {
		feeds = append(feeds, feed{
			name: "org:" + org,
			read: eventFeed(func() ([]*github.Event, *github.Response, error) {
				return c.REST.Activity.ListUserEventsForOrganization(ctx, org, src.Login, page)
			}),
		})
	}
	for _, r := range src.Repos {
		feeds = append(feeds, feed{
			name: r.String(),
			read: eventFeed(func() ([]*github.Event, *github.Response, error) {
				return c.REST.Activity.ListRepositoryEvents(ctx, r.Owner, r.Repo, page)
			}),
		})
	}
	// Probe the permission again on every backfill, so an approval is
	// picked up on the next refresh.
	c.noContents.reset()
	for _, r := range src.Activity {
		feeds = append(feeds, feed{
			name: "activity:" + r.String(),
			read: func() ([]events.Event, error) {
				if c.noContents.has(r.Owner) {
					return nil, nil
				}
				acts, _, err := c.REST.Repositories.ListRepositoryActivities(ctx, r.Owner, r.Repo,
					&github.ListRepositoryActivityOptions{PerPage: 100})
				if c.contentsDenied(r.Owner, err) {
					return nil, nil // not an error: the Events API feeds cover it
				}
				return MapActivities(r.String(), acts), err
			},
		})
	}

	type result struct {
		name string
		evts []events.Event
		err  error
	}
	results := parallel(feeds, concurrency, func(f feed) result {
		evts, err := f.read()
		if err != nil {
			return result{name: f.name, err: fmt.Errorf("%s: %w", f.name, err)}
		}
		return result{name: f.name, evts: evts}
	})

	var out []events.Event
	errs := map[string]error{}
	for _, r := range results {
		out = append(out, r.evts...)
		if r.err != nil {
			errs[r.name] = r.err
		}
	}
	return out, errs
}

// MapEvents converts Events API entries to app events, skipping the rest.
func MapEvents(raw []*github.Event) []events.Event {
	var out []events.Event
	for _, e := range raw {
		switch e.GetType() {
		case "PushEvent", "CreateEvent", "ReleaseEvent":
		default:
			continue
		}
		p, err := e.ParsePayload()
		if err != nil {
			continue
		}
		c := events.Context{Source: events.SourceBackfill}
		if e.Repo != nil {
			c.Repo = e.Repo.GetName() // Events API: "owner/name"
		}
		if e.Actor != nil {
			c.Actor, c.Avatar = e.Actor.GetLogin(), e.Actor.GetAvatarURL()
		}
		if e.CreatedAt != nil {
			c.Time = e.CreatedAt.Time
		}
		if ev, ok := events.FromPayload(p, c); ok {
			out = append(out, ev)
		}
	}
	return out
}

// MapActivities converts a repository's activity log (GET
// /repos/{o}/{r}/activity) to app events with the same rules and keys as
// webhook pushes: ref updates (push, force push, pull request and merge
// queue merges) become pushes, branch creations become branch events, and
// deletions are skipped.
func MapActivities(repo string, acts []*github.RepositoryActivity) []events.Event {
	var out []events.Event
	for _, a := range acts {
		c := events.Context{Source: events.SourceBackfill, Repo: repo}
		if a.Actor != nil {
			c.Actor, c.Avatar = a.Actor.GetLogin(), a.Actor.GetAvatarURL()
		}
		if a.Timestamp != nil {
			c.Time = a.Timestamp.Time
		}
		var payload any
		switch a.ActivityType {
		case "push", "force_push", "pr_merge", "merge_queue_merge":
			ref, before, after := a.Ref, a.Before, a.After
			payload = &github.PushEvent{Ref: &ref, Before: &before, After: &after}
		case "branch_creation":
			ref, refType := a.Ref, "branch"
			if t, ok := strings.CutPrefix(ref, "refs/tags/"); ok {
				ref, refType = t, "tag"
			} else {
				ref = strings.TrimPrefix(ref, "refs/heads/")
			}
			payload = &github.CreateEvent{Ref: &ref, RefType: &refType}
		default:
			continue // branch_deletion and anything new
		}
		if ev, ok := events.FromPayload(payload, c); ok {
			out = append(out, ev)
		}
	}
	return out
}
