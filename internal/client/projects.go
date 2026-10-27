package client

import "context"

// Project is one project the current key can see.
type Project struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// Projects lists the projects the current key is a member of.
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	var resp struct {
		Projects []Project `json:"projects"`
	}
	if err := c.Do(ctx, "GET", "/api/v1/projects", nil, &resp); err != nil {
		return nil, err
	}
	if resp.Projects == nil {
		resp.Projects = []Project{}
	}
	return resp.Projects, nil
}
