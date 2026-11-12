package core

import (
	"path/filepath"
	"testing"
	"time"

	"go-gh/internal/config"
	"go-gh/internal/events"
	"go-gh/internal/gh"
	"go-gh/internal/secrets"
)

func TestInstallState(t *testing.T) {
	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = cfg.Update(func(c *config.Config) { c.DisabledOrgs = []string{"off"} })
	c := New(Options{ClientID: "id", AppSlug: "go-gh", Secrets: secrets.Memory{}, Config: cfg})

	user := gh.User{ID: 1, Login: "Mona"}
	orgs := []gh.Org{
		{ID: 10, Login: "acme", Owner: true, RoleKnown: true},  // installed, owner -> org hook
		{ID: 11, Login: "beta", Owner: false, RoleKnown: true}, // installed, member -> repo hooks only
		{ID: 12, Login: "gamma", Owner: true, RoleKnown: true}, // not installed
		{ID: 13, Login: "off", Owner: true, RoleKnown: true},   // installed but disabled by user
		{ID: 14, Login: "susp", Owner: true, RoleKnown: true},  // suspended installation
	}
	insts := []gh.Installation{
		{Account: "mona", Selection: "selected", URL: "https://github.com/settings/installations/1"},
		{Account: "ACME", Selection: "all", URL: "https://acme/inst"},
		{Account: "beta", Selection: "all", URL: "https://beta/inst"},
		{Account: "off", Selection: "all", URL: "https://off/inst"},
		{Account: "susp", Suspended: true, URL: "https://susp/inst"},
	}
	app, st := c.installState(user, orgSources{members: orgs, installs: insts})

	if !app.Installed || app.Selection != "selected" || app.InstallURL != "https://github.com/settings/installations/1" ||
		app.AnyInstallURL != "https://github.com/apps/go-gh/installations/new" {
		t.Errorf("app = %+v", app)
	}
	want := map[string]struct {
		installed, enabled bool
		url                string
	}{
		"acme":  {true, true, "https://acme/inst"},
		"beta":  {true, false, "https://beta/inst"},
		"gamma": {false, false, "https://github.com/apps/go-gh/installations/new/permissions?target_id=12"},
		"off":   {true, false, "https://off/inst"},
		"susp":  {false, false, "https://github.com/apps/go-gh/installations/new/permissions?target_id=14"},
	}
	for _, o := range st {
		w := want[o.Login]
		if o.Installed != w.installed || o.Enabled != w.enabled || o.InstallURL != w.url {
			t.Errorf("%s: installed=%v enabled=%v url=%s", o.Login, o.Installed, o.Enabled, o.InstallURL)
		}
	}

	// Not installed on the personal account: link preselects the user.
	app, _ = c.installState(user, orgSources{})
	if app.Installed || app.InstallURL != "https://github.com/apps/go-gh/installations/new/permissions?target_id=1" {
		t.Errorf("uninstalled app = %+v", app)
	}
}

func TestHookTargets(t *testing.T) {
	repo := func(owner, name string, org, admin bool) gh.Repo {
		return gh.Repo{NameWithOwner: owner + "/" + name, Owner: owner, Name: name, OwnerIsOrg: org, Admin: admin}
	}
	in := targetInput{
		userLogin:     "mailsvb",
		userInstalled: true,
		installsKnown: true,
		repoHooks:     true,
		orgs: []OrgState{
			{Org: gh.Org{Login: "acme", Owner: true}, Installed: true, Enabled: true},
			{Org: gh.Org{Login: "beta"}, Installed: true},
			{Org: gh.Org{Login: "circuit"}},
		},
		repos: []gh.Repo{
			repo("mailsvb", "go-gh", false, true),
			repo("acme", "api", true, true),           // covered by org hook
			repo("beta", "web", true, true),           // installed org, repo admin
			repo("circuit", "backup", true, true),     // app not installed: skip
			repo("mailsvb", "readonly", false, false), // not admin
			{NameWithOwner: "mailsvb/old", Owner: "mailsvb", Name: "old", Admin: true, Archived: true},
		},
	}
	targets, skipped := hookTargets(in)
	want := []gh.HookTarget{{Org: "acme"}, {Owner: "mailsvb", Repo: "go-gh"}, {Owner: "beta", Repo: "web"}}
	if len(targets) != len(want) {
		t.Fatalf("targets = %v, want %v", targets, want)
	}
	for i := range want {
		if targets[i] != want[i] {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	}
	if len(skipped) != 1 || skipped[0] != "circuit/backup" {
		t.Fatalf("skipped = %v", skipped)
	}

	// Unknown installations (listing failed): try everything, skip nothing.
	in.installsKnown = false
	if targets, skipped := hookTargets(in); len(targets) != 4 || len(skipped) != 0 {
		t.Fatalf("unknown installs: targets=%v skipped=%v", targets, skipped)
	}
	// Repo hooks off: only org hooks.
	in.repoHooks = false
	if targets, _ := hookTargets(in); len(targets) != 1 {
		t.Fatalf("repo hooks off: %v", targets)
	}
}

func TestInstallStateFindsOrgsWithoutMembership(t *testing.T) {
	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	c := New(Options{ClientID: "id", AppSlug: "go-gh", Secrets: secrets.Memory{}, Config: cfg})
	user := gh.User{ID: 1, Login: "mona"}

	// With a GitHub App token the membership list only has installed orgs.
	_, st := c.installState(user, orgSources{
		members: []gh.Org{{ID: 10, Login: "acme", Owner: true, RoleKnown: true}},
		public:  []gh.Org{{ID: 20, Login: "Public-Org", AvatarURL: "a20"}, {ID: 10, Login: "acme"}},
		repos: []gh.Repo{
			{Owner: "repo-org", OwnerID: 30, OwnerIsOrg: true, OwnerAvatar: "a30"},
			{Owner: "public-org", OwnerID: 20, OwnerIsOrg: true},
			{Owner: "mona", OwnerID: 1},    // the user, not an org
			{Owner: "someone", OwnerID: 2}, // another user, not an org
		},
		installs: []gh.Installation{{Account: "acme", AccountID: 10, IsOrg: true, URL: "https://acme/inst"}},
	})

	got := map[string]OrgState{}
	var order []string
	for _, o := range st {
		got[o.Login] = o
		order = append(order, o.Login)
	}
	if len(st) != 3 || order[0] != "acme" {
		t.Fatalf("orgs = %v, want acme first then 2 more", order)
	}
	if o := got["acme"]; !o.Installed || !o.RoleKnown || !o.Owner || !o.Enabled {
		t.Errorf("acme = %+v", o)
	}
	if o := got["Public-Org"]; o.Installed || o.RoleKnown ||
		o.InstallURL != "https://github.com/apps/go-gh/installations/new/permissions?target_id=20" {
		t.Errorf("public-org = %+v", o)
	}
	if o := got["repo-org"]; o.AvatarURL != "a30" ||
		o.InstallURL != "https://github.com/apps/go-gh/installations/new/permissions?target_id=30" {
		t.Errorf("repo-org = %+v", o)
	}
}

func TestInScopeAndNewest(t *testing.T) {
	at := func(s int) time.Time { return time.Unix(int64(s), 0) }
	evts := []events.Event{
		{Key: "a", Repo: "mona/app", Time: at(1)},
		{Key: "b", Repo: "Acme/api", Time: at(3)},   // org target, case-insensitive
		{Key: "c", Repo: "circuit/x", Time: at(2)},  // no webhook: dropped
		{Key: "b", Repo: "acme/api", Time: at(3)},   // duplicate
		{Key: "d", Repo: "mona/other", Time: at(4)}, // repo without webhook: dropped
	}
	targets := []gh.HookTarget{{Org: "acme"}, {Owner: "mona", Repo: "app"}}
	got := newest(inScope(evts, targets), 10)
	if len(got) != 2 || got[0].Key != "b" || got[1].Key != "a" {
		t.Fatalf("got %+v", got)
	}
	if got := newest(evts, 2); len(got) != 2 || got[0].Key != "d" || got[1].Key != "b" {
		t.Fatalf("newest(2) = %+v", got)
	}
}
