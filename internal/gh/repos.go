package gh

import (
	"context"

	"github.com/shurcooL/githubv4"
)

// Repo is a repository shown in the main list.
type Repo struct {
	NameWithOwner string `json:"nameWithOwner"`
	Owner         string `json:"owner"`
	OwnerID       int64  `json:"ownerId"`
	OwnerAvatar   string `json:"ownerAvatar"`
	OwnerIsOrg    bool   `json:"ownerIsOrg"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	ImageURL      string `json:"imageUrl"` // social preview (openGraphImageUrl)
	URL           string `json:"url"`
	Private       bool   `json:"private"`
	Archived      bool   `json:"archived"`
	Admin         bool   `json:"admin"` // viewer can manage webhooks
}

type repoNode struct {
	NameWithOwner     string
	Name              string
	Description       string
	OpenGraphImageURL githubv4.URI `graphql:"openGraphImageUrl"`
	URL               githubv4.URI
	IsPrivate         bool
	IsArchived        bool
	ViewerPermission  githubv4.RepositoryPermission
	Owner             struct {
		Login        string
		Typename     string       `graphql:"__typename"`
		AvatarURL    githubv4.URI `graphql:"avatarUrl"`
		Organization struct {
			DatabaseID int64 `graphql:"databaseId"`
		} `graphql:"... on Organization"`
		User struct {
			DatabaseID int64 `graphql:"databaseId"`
		} `graphql:"... on User"`
	}
}

// Repos lists all repositories the viewer owns, collaborates on or can access
// as an organization member, most recently updated first.
func (c *Client) Repos(ctx context.Context) ([]Repo, error) {
	var q struct {
		Viewer struct {
			Repositories struct {
				Nodes    []repoNode
				PageInfo struct {
					EndCursor   githubv4.String
					HasNextPage bool
				}
			} `graphql:"repositories(first: 100, after: $cursor, affiliations: [OWNER, COLLABORATOR, ORGANIZATION_MEMBER], ownerAffiliations: [OWNER, COLLABORATOR, ORGANIZATION_MEMBER], orderBy: {field: UPDATED_AT, direction: DESC})"`
		}
	}
	vars := map[string]any{"cursor": (*githubv4.String)(nil)}
	var out []Repo
	for {
		err := c.GraphQL.Query(ctx, &q, vars)
		nodes := q.Viewer.Repositories.Nodes
		if err != nil && len(nodes) == 0 {
			// GraphQL may return partial data (e.g. SSO-protected orgs); keep
			// what we got and fail only when nothing came back.
			if len(out) == 0 {
				return nil, err
			}
			return out, nil
		}
		for _, n := range nodes {
			r := Repo{
				NameWithOwner: n.NameWithOwner,
				Owner:         n.Owner.Login,
				OwnerIsOrg:    n.Owner.Typename == "Organization",
				OwnerID:       max(n.Owner.Organization.DatabaseID, n.Owner.User.DatabaseID),
				Name:          n.Name,
				Description:   n.Description,
				Private:       n.IsPrivate,
				Archived:      n.IsArchived,
				Admin:         n.ViewerPermission == githubv4.RepositoryPermissionAdmin,
			}
			if n.OpenGraphImageURL.URL != nil {
				r.ImageURL = n.OpenGraphImageURL.String()
			}
			if n.Owner.AvatarURL.URL != nil {
				r.OwnerAvatar = n.Owner.AvatarURL.String()
			}
			if n.URL.URL != nil {
				r.URL = n.URL.String()
			}
			out = append(out, r)
		}
		if !q.Viewer.Repositories.PageInfo.HasNextPage {
			return out, nil
		}
		vars["cursor"] = githubv4.NewString(q.Viewer.Repositories.PageInfo.EndCursor)
	}
}
