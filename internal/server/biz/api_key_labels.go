package biz

import (
	"context"
	"strings"
	"unicode"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikeygroup"
	"github.com/looplj/axonhub/internal/ent/user"
)

// APIKeyLabels accepts keys already authorized by the caller; only display fields
// for those keys' owners and groups are read in the narrow bypass.
func APIKeyLabels(ctx context.Context, client *ent.Client, keys []*ent.APIKey) (map[int]string, error) {
	return authz.RunWithSystemBypass(ctx, "api-key-display-labels", func(ctx context.Context) (map[int]string, error) {
		labels := make(map[int]string, len(keys))
		if len(keys) == 0 {
			return labels, nil
		}
		var users, groups []int
		for _, key := range keys {
			users = append(users, key.UserID)
			if key.GroupRef != nil {
				groups = append(groups, *key.GroupRef)
			}
		}
		owners, err := client.User.Query().Where(user.IDIn(users...)).Select(user.FieldID, user.FieldFirstName, user.FieldLastName).All(ctx)
		if err != nil {
			return nil, err
		}
		ownerNames := make(map[int]string)
		for _, owner := range owners {
			ownerNames[owner.ID] = APIKeyUserLabel(owner.FirstName, owner.LastName)
		}
		groupNames := make(map[int]*ent.APIKeyGroup)
		if len(groups) > 0 {
			rows, err := client.APIKeyGroup.Query().Where(apikeygroup.IDIn(groups...)).All(ctx)
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				groupNames[row.ID] = row
			}
		}
		for _, key := range keys {
			owner := ownerNames[key.UserID]
			if key.GroupRef != nil {
				if group := groupNames[*key.GroupRef]; group != nil && group.ProjectID == key.ProjectID {
					owner = group.Name
				}
			}
			labels[key.ID] = key.Name
			if owner != "" {
				labels[key.ID] += " · " + owner
			}
		}
		return labels, nil
	})
}

// APIKeyUserLabel matches the shared frontend name formatting.
func APIKeyUserLabel(first, last string) string {
	first, last = strings.TrimSpace(first), strings.TrimSpace(last)
	if first == "" {
		return last
	}
	if last == "" {
		return first
	}
	for _, r := range first + last {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
			return last + first
		}
	}
	return first + " " + last
}
