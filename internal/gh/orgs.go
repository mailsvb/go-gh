package gh

import (
	"context"

	"github.com/google/go-github/v93/github"
)

// Org is an organization the user is a member of.
type Org struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	AvatarURL string `json:"avatarUrl"`
	Owner     bool   `json:"owner"` // role "admin": may manage org webhooks
}

// Orgs lists the user's active organization memberships.
func (c *Client) Orgs(ctx context.Context) ([]Org, error) {
	opts := &github.ListOrgMembershipsOptions{State: "active", ListOptions: github.ListOptions{PerPage: 100}}
	var out []Org
	for {
		ms, resp, err := c.REST.Organizations.ListOrgMemberships(ctx, opts)
		if err != nil {
			return nil, err
		}
		for _, m := range ms {
			o := m.GetOrganization()
			out = append(out, Org{
				ID:        o.GetID(),
				Login:     o.GetLogin(),
				AvatarURL: o.GetAvatarURL(),
				Owner:     m.GetRole() == "admin",
			})
		}
		if resp.NextPage == 0 {
			return out, nil
		}
		opts.Page = resp.NextPage
	}
}
