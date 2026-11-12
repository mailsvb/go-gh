package gh

import (
	"context"
	"fmt"

	"github.com/google/go-github/v93/github"
)

// Installation is an installation of the GitHub App that the user can access.
type Installation struct {
	ID int64 `json:"id"`
	// Account is the user or organization login the app is installed on.
	Account   string `json:"account"`
	AccountID int64  `json:"accountId"`
	IsOrg     bool   `json:"isOrg"`
	// Selection is "all" or "selected" repositories.
	Selection string `json:"selection"`
	// URL is where the installation's repository selection is configured.
	URL       string `json:"url"`
	Suspended bool   `json:"suspended"`
}

// Installations lists the app's installations accessible to the user
// (GET /user/installations).
func (c *Client) Installations(ctx context.Context) ([]Installation, error) {
	opts := &github.ListOptions{PerPage: 100}
	var out []Installation
	for {
		is, resp, err := c.REST.Apps.ListUserInstallations(ctx, opts)
		if err != nil {
			return nil, err
		}
		for _, i := range is {
			a := i.GetAccount()
			out = append(out, Installation{
				ID:        i.GetID(),
				Account:   a.GetLogin(),
				AccountID: a.GetID(),
				IsOrg:     i.GetTargetType() == "Organization",
				Selection: i.GetRepositorySelection(),
				URL:       i.GetHTMLURL(),
				Suspended: i.SuspendedAt != nil,
			})
		}
		if resp.NextPage == 0 {
			return out, nil
		}
		opts.Page = resp.NextPage
	}
}

// InstallURL returns the page where the app is installed on a new account.
// With targetID (a user or organization ID) GitHub preselects that account;
// org members who are not owners can request the installation there.
func InstallURL(slug string, targetID int64) string {
	if slug == "" {
		return ""
	}
	if targetID == 0 {
		return fmt.Sprintf("https://github.com/apps/%s/installations/new", slug)
	}
	return fmt.Sprintf("https://github.com/apps/%s/installations/new/permissions?target_id=%d", slug, targetID)
}
