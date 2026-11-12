package gh

import (
	"context"
	"fmt"

	"github.com/google/go-github/v93/github"

	"go-gh/internal/events"
)

// BackfillSources selects the Events API feeds used to rebuild the history.
type BackfillSources struct {
	Login string       // the authenticated user
	Orgs  []string     // organizations: /users/{login}/events/orgs/{org}
	Repos []HookTarget // repositories outside those orgs: /repos/{o}/{r}/events
}

// Backfill reads recent events from the Events API and maps the relevant
// ones (push, tag/branch create, release published). Errors of individual
// feeds are returned keyed by feed name ("user:<login>", "org:<org>",
// "owner/repo") alongside whatever could be read.
func (c *Client) Backfill(ctx context.Context, src BackfillSources, concurrency int) ([]events.Event, map[string]error) {
	type feed struct {
		name string
		list func(*github.ListOptions) ([]*github.Event, *github.Response, error)
	}
	feeds := []feed{{
		name: "user:" + src.Login,
		list: func(o *github.ListOptions) ([]*github.Event, *github.Response, error) {
			return c.REST.Activity.ListEventsPerformedByUser(ctx, src.Login, false, o)
		},
	}}
	for _, org := range src.Orgs {
		feeds = append(feeds, feed{
			name: "org:" + org,
			list: func(o *github.ListOptions) ([]*github.Event, *github.Response, error) {
				return c.REST.Activity.ListUserEventsForOrganization(ctx, org, src.Login, o)
			},
		})
	}
	for _, r := range src.Repos {
		feeds = append(feeds, feed{
			name: r.String(),
			list: func(o *github.ListOptions) ([]*github.Event, *github.Response, error) {
				return c.REST.Activity.ListRepositoryEvents(ctx, r.Owner, r.Repo, o)
			},
		})
	}

	type result struct {
		name string
		evts []events.Event
		err  error
	}
	results := parallel(feeds, concurrency, func(f feed) result {
		// One page of 100 per feed is enough to fill a 100-event history.
		raw, _, err := f.list(&github.ListOptions{PerPage: 100})
		if err != nil {
			return result{name: f.name, err: fmt.Errorf("%s: %w", f.name, err)}
		}
		return result{name: f.name, evts: MapEvents(raw)}
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
