package gh

import (
	"context"
	"strings"
	"sync"

	"github.com/google/go-github/v93/github"

	"go-gh/internal/events"
)

// pushInfo is what the Compare API adds to an Events API push.
type pushInfo struct {
	commits int
	message string
}

// pushCache keeps compare results for the app's lifetime; a commit range
// never changes, so refreshes and wake-ups reuse them.
type pushCache struct {
	mu sync.Mutex
	m  map[string]pushInfo
}

func (p *pushCache) get(k string) (pushInfo, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.m[k]
	return v, ok
}

func (p *pushCache) put(k string, v pushInfo) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[string]pushInfo{}
	}
	p.m[k] = v
}

// EnrichPushes fills in the commit count and head commit message of Events
// API pushes, which GitHub no longer includes there, so they read exactly
// like the same push received as a webhook. It calls
// GET /repos/{repo}/compare/{before}...{head} once per push range. Events that
// can't be enriched (e.g. no access) are returned unchanged.
func (c *Client) EnrichPushes(ctx context.Context, evts []events.Event, concurrency int) []events.Event {
	var idx []int
	for i, e := range evts {
		if e.NeedsCommits() {
			idx = append(idx, i)
		}
	}
	infos := parallel(idx, concurrency, func(i int) *pushInfo {
		e := evts[i]
		key := e.Repo + "@" + e.Before + "..." + e.Head
		if v, ok := c.pushes.get(key); ok {
			return &v
		}
		owner, repo, ok := strings.Cut(e.Repo, "/")
		if !ok {
			return nil
		}
		cmp, _, err := c.REST.Repositories.CompareCommits(ctx, owner, repo, e.Before, e.Head, &github.ListOptions{PerPage: 100})
		if err != nil {
			return nil
		}
		v := pushInfo{commits: cmp.GetTotalCommits()}
		// Commits are oldest first; the last one is the head when the
		// whole range fits on the first page.
		if n := len(cmp.Commits); n > 0 && n == v.commits {
			v.message = cmp.Commits[n-1].GetCommit().GetMessage()
		} else if v.commits > 0 {
			if hc, _, err := c.REST.Repositories.GetCommit(ctx, owner, repo, e.Head, nil); err == nil {
				v.message = hc.GetCommit().GetMessage()
			}
		}
		c.pushes.put(key, v)
		return &v
	})
	out := append([]events.Event(nil), evts...)
	for k, i := range idx {
		if v := infos[k]; v != nil {
			out[i].SetCommits(v.commits, v.message)
		}
	}
	return out
}
