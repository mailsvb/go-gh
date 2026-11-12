package gh

import (
	"slices"
	"strings"
	"sync"
)

// The repository activity log and the commit APIs (compare, commits) need
// the GitHub App permission "Contents: read". Installations that have not
// approved it yet answer 403 "Resource not accessible by integration". Such
// accounts are remembered so their remaining repositories are skipped, and
// their history falls back to the Events API.

// ownerSet is a concurrency-safe set of account logins (lowercase).
type ownerSet struct {
	mu sync.Mutex
	m  map[string]bool
}

func (s *ownerSet) has(owner string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[strings.ToLower(owner)]
}

func (s *ownerSet) add(owner string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]bool{}
	}
	s.m[strings.ToLower(owner)] = true
}

func (s *ownerSet) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m = nil
}

func (s *ownerSet) list() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.m))
	for k := range s.m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// contentsDenied reports whether err means the installation on owner lacks
// "Contents: read", and remembers it.
func (c *Client) contentsDenied(owner string, err error) bool {
	if p, _ := ClassifyAccess(err); p != AccessNotInstalled {
		return false
	}
	c.noContents.add(owner)
	return true
}

// NoContentsAccess returns the accounts (lowercase logins) whose go-gh
// installation lacks "Contents: read", as found by the last Backfill and
// EnrichPushes.
func (c *Client) NoContentsAccess() []string { return c.noContents.list() }
