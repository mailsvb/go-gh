package events

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/go-github/v93/github"
)

// Context carries data that is not part of every payload: the Events API
// puts repo, actor and time on the envelope, webhooks put them in the payload.
type Context struct {
	Source Source
	Repo   string // owner/name, used when the payload has no repository
	Actor  string
	Avatar string
	Time   time.Time
}

// FromPayload maps a parsed go-github payload (from github.ParseWebHook or
// github.Event.ParsePayload) to an Event. ok is false for payloads that are
// not shown to the user (deletions, tag pushes covered by CreateEvent, etc.).
func FromPayload(payload any, c Context) (Event, bool) {
	switch p := payload.(type) {
	case *github.PushEvent:
		return fromPush(p, c)
	case *github.CreateEvent:
		return fromCreate(p, c)
	case *github.ReleaseEvent:
		return fromRelease(p, c)
	}
	return Event{}, false
}

// zeroSHA is what GitHub sends as "before" for a new ref and as the head
// of a deleted ref.
const zeroSHA = "0000000000000000000000000000000000000000"

// fromPush maps webhook pushes and Events API pushes with the same rules.
// The Events API only carries ref, before and head (no commits, no created
// flag), so everything is derived from those; the commit count and message
// are filled in later with SetCommits (from webhook commits or the Compare
// API), which keeps both sources identical.
func fromPush(p *github.PushEvent, c Context) (Event, bool) {
	ref := p.GetRef()
	before := p.GetBefore()
	head := p.GetAfter()
	if head == "" {
		head = p.GetHead()
	}
	switch {
	case strings.HasPrefix(ref, "refs/tags/"):
		return Event{}, false // reported by the CreateEvent for the tag
	case p.GetDeleted() || head == zeroSHA:
		return Event{}, false // branch deletion
	case p.GetCreated() || before == zeroSHA:
		return Event{}, false // new branch: reported by its CreateEvent
	case !strings.HasPrefix(ref, "refs/heads/") || head == "":
		return Event{}, false
	}

	repo := c.Repo
	if r := p.GetRepo(); r != nil && r.GetFullName() != "" {
		repo = r.GetFullName()
	}
	actor, avatar := c.Actor, c.Avatar
	if s := p.GetSender(); s != nil {
		actor, avatar = s.GetLogin(), s.GetAvatarURL()
	}
	if actor == "" && p.GetPusher() != nil {
		actor = p.GetPusher().GetName()
	}

	link := "https://github.com/" + repo + "/commit/" + head
	if before != "" {
		link = fmt.Sprintf("https://github.com/%s/compare/%s...%s", repo, short(before), short(head))
	}
	e := Event{
		Key:    fmt.Sprintf("push:%s:%s:%s", repo, ref, head),
		Kind:   KindPush,
		Repo:   repo,
		Ref:    strings.TrimPrefix(ref, "refs/heads/"),
		Actor:  actor,
		Avatar: avatar,
		URL:    link,
		Time:   orNow(c.Time),
		Source: c.Source,
		Before: before,
		Head:   head,
	}
	msg := ""
	if hc := p.GetHeadCommit(); hc != nil {
		msg = hc.GetMessage()
	} else if len(p.Commits) > 0 {
		msg = p.Commits[len(p.Commits)-1].GetMessage()
	}
	e.SetCommits(len(p.Commits), msg)
	return e, true
}

// SetCommits sets the commit count and head commit message of a push and
// rebuilds its title, e.g. "mona pushed 2 commits to main".
func (e *Event) SetCommits(n int, headMessage string) {
	e.Commits = n
	e.Detail = firstLine(headMessage)
	if n > 0 {
		e.Title = fmt.Sprintf("%s pushed %d commit%s to %s", e.Actor, n, plural(n), e.Ref)
	} else {
		e.Title = fmt.Sprintf("%s pushed to %s", e.Actor, e.Ref)
	}
}

// NeedsCommits reports whether a push still lacks its commit details (Events
// API pushes before enrichment).
func (e Event) NeedsCommits() bool {
	return e.Kind == KindPush && e.Commits == 0 && e.Before != "" && e.Head != ""
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func fromCreate(p *github.CreateEvent, c Context) (Event, bool) {
	var kind Kind
	switch p.GetRefType() {
	case "tag":
		kind = KindTag
	case "branch":
		kind = KindBranch
	default:
		return Event{}, false // "repository" creation is not tracked in v1
	}
	repo := c.Repo
	if r := p.GetRepo(); r != nil && r.GetFullName() != "" {
		repo = r.GetFullName()
	}
	actor, avatar := c.Actor, c.Avatar
	if s := p.GetSender(); s != nil {
		actor, avatar = s.GetLogin(), s.GetAvatarURL()
	}
	ref := p.GetRef()
	link := "https://github.com/" + repo + "/tree/" + ref
	verb := "created branch"
	if kind == KindTag {
		link = "https://github.com/" + repo + "/releases/tag/" + ref
		verb = "created tag"
	}
	return Event{
		Key:    fmt.Sprintf("create:%s:%s:%s", repo, kind, ref),
		Kind:   kind,
		Repo:   repo,
		Ref:    ref,
		Actor:  actor,
		Avatar: avatar,
		Title:  fmt.Sprintf("%s %s %s", actor, verb, ref),
		URL:    link,
		Time:   orNow(c.Time),
		Source: c.Source,
	}, true
}

func fromRelease(p *github.ReleaseEvent, c Context) (Event, bool) {
	if p.GetAction() != "published" {
		return Event{}, false
	}
	rel := p.GetRelease()
	if rel == nil {
		return Event{}, false
	}
	repo := c.Repo
	if r := p.GetRepo(); r != nil && r.GetFullName() != "" {
		repo = r.GetFullName()
	}
	actor, avatar := c.Actor, c.Avatar
	if s := p.GetSender(); s != nil {
		actor, avatar = s.GetLogin(), s.GetAvatarURL()
	}
	name := rel.GetName()
	if name == "" {
		name = rel.GetTagName()
	}
	t := c.Time
	if t.IsZero() && rel.PublishedAt != nil {
		t = rel.PublishedAt.Time
	}
	return Event{
		Key:    fmt.Sprintf("release:%s:%d", repo, rel.GetID()),
		Kind:   KindRelease,
		Repo:   repo,
		Ref:    rel.GetTagName(),
		Actor:  actor,
		Avatar: avatar,
		Title:  fmt.Sprintf("%s published release %s", actor, name),
		Detail: firstLine(rel.GetBody()),
		URL:    rel.GetHTMLURL(),
		Time:   orNow(t),
		Source: c.Source,
	}, true
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 140 {
		s = string(r[:139]) + "…"
	}
	return s
}

func orNow(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now()
	}
	return t
}
