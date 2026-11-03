package gql

import (
	"context"
	"sync"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikeygroup"
	"github.com/looplj/axonhub/internal/ent/schema/schematype"
	"github.com/looplj/axonhub/internal/server/biz"
)

type keyIdentityCacheKey struct{}
type keyIdentityValue struct {
	name string
	err  error
}
type keyIdentityCache struct {
	mu      sync.Mutex
	names   map[[3]int]keyIdentityValue
	history map[[2]int]*ent.APIKey
}

func withKeyIdentityCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, keyIdentityCacheKey{}, &keyIdentityCache{names: make(map[[3]int]keyIdentityValue), history: make(map[[2]int]*ent.APIKey)})
}

// Deleted identities are immutable. Reuse one lookup when several historical
// requests refer to the same key, without widening the root query's visibility.
func historicalRequestAPIKey(ctx context.Context, request *ent.Request) (*ent.APIKey, error) {
	cache, _ := ctx.Value(keyIdentityCacheKey{}).(*keyIdentityCache)
	identity := [2]int{request.ProjectID, request.APIKeyID}
	if cache != nil {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		if key, ok := cache.history[identity]; ok {
			return key, nil
		}
	}
	key, err := request.QueryAPIKey().Only(schematype.SkipSoftDelete(ctx))
	if ent.IsNotFound(err) {
		key, err = nil, nil
	}
	if cache != nil && err == nil && (key == nil || key.DeletedAt != 0) {
		cache.history[identity] = key
	}
	return key, err
}

func keyIdentityName(ctx context.Context, client *ent.Client, key *ent.APIKey, group bool) (string, error) {
	id := key.UserID
	kind := 0
	if group {
		if key.GroupRef == nil {
			return "", nil
		}
		id = *key.GroupRef
		kind = 1
	}
	cache, _ := ctx.Value(keyIdentityCacheKey{}).(*keyIdentityCache)
	cacheID := [3]int{kind, key.ProjectID, id}
	if cache != nil {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		if value, ok := cache.names[cacheID]; ok {
			return value.name, value.err
		}
	}
	var value keyIdentityValue
	if group {
		value.name, value.err = authz.RunWithSystemBypass(ctx, "api-key-group-label", func(ctx context.Context) (string, error) {
			row, err := client.APIKeyGroup.Query().Where(apikeygroup.IDEQ(id), apikeygroup.ProjectIDEQ(key.ProjectID)).Only(ctx)
			if ent.IsNotFound(err) {
				return "", nil
			}
			if err != nil {
				return "", err
			}
			return row.Name, nil
		})
	} else {
		owner, err := getNilableUser(ctx, client, id)
		value.err = err
		if owner != nil {
			value.name = biz.APIKeyUserLabel(owner.FirstName, owner.LastName)
		}
	}
	if cache != nil {
		cache.names[cacheID] = value
	}
	return value.name, value.err
}
