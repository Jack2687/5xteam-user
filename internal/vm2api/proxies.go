package vm2api

import (
	"context"
	"strings"
)

// Only public selection fields are returned to the browser, never proxy credentials.
type Proxy struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	Scheme        string `json:"scheme"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Enabled       bool   `json:"enabled"`
	Status        string `json:"status"`
	BlockedReason string `json:"blocked_reason"`
	BoundCount    *int   `json:"bound_count"`
	BindLimit     *int   `json:"bind_limit"`
}

func NormalizeProxyIDs(ids []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func (c *Client) Proxies(ctx context.Context) ([]Proxy, error) {
	var out struct {
		Proxies []Proxy `json:"proxies"`
	}
	if err := c.request(ctx, "GET", "/proxies", "", nil, &out); err != nil {
		return nil, err
	}
	if out.Proxies == nil {
		out.Proxies = []Proxy{}
	}
	return out.Proxies, nil
}
