package core

import (
	"path/filepath"
	"testing"

	"go-gh/internal/config"
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
		{ID: 10, Login: "acme", Owner: true},  // installed, owner -> org hook
		{ID: 11, Login: "beta", Owner: false}, // installed, member -> repo hooks only
		{ID: 12, Login: "gamma", Owner: true}, // not installed
		{ID: 13, Login: "off", Owner: true},   // installed but disabled by user
		{ID: 14, Login: "susp", Owner: true},  // suspended installation
	}
	insts := []gh.Installation{
		{Account: "mona", Selection: "selected", URL: "https://github.com/settings/installations/1"},
		{Account: "ACME", Selection: "all", URL: "https://acme/inst"},
		{Account: "beta", Selection: "all", URL: "https://beta/inst"},
		{Account: "off", Selection: "all", URL: "https://off/inst"},
		{Account: "susp", Suspended: true, URL: "https://susp/inst"},
	}
	app, st := c.installState(user, orgs, insts)

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
	app, _ = c.installState(user, nil, nil)
	if app.Installed || app.InstallURL != "https://github.com/apps/go-gh/installations/new/permissions?target_id=1" {
		t.Errorf("uninstalled app = %+v", app)
	}
}
