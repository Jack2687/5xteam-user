package vm2api

import (
	"context"
	"regexp"
	"slices"

	"chatgpt-space-merge/internal/model"
)

type Group struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Enabled      bool   `json:"enabled"`
	AccountCount int    `json:"account_count"`
}

var groupIDPattern = regexp.MustCompile(`^pool_[a-f0-9]{8}$`)

func NormalizeGroupIDs(ids []string) []string {
	out := NormalizeProxyIDs(ids)
	slices.Sort(out)
	return out
}

func (c *Client) Groups(ctx context.Context) ([]Group, error) {
	var out struct {
		Groups []Group `json:"groups"`
	}
	if err := c.request(ctx, "GET", "/vm-groups", "", nil, &out); err != nil {
		return nil, err
	}
	if out.Groups == nil {
		out.Groups = []Group{}
	}
	return out.Groups, nil
}

// Compare pinned onboarding groups, not live membership edited in vm2api.
func SlotGroupsMatch(v model.VM2APISettings, slot Slot) bool {
	if slot.VM.PoolID != v.PoolID {
		return false
	}
	if len(v.GroupIDs) == 0 {
		return true
	} // Legacy server/snapshot compatibility.
	return slices.Equal(NormalizeGroupIDs(v.GroupIDs), NormalizeGroupIDs(slot.VM.GroupIDs))
}
