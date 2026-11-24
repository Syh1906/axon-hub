package biz

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"fmt"
	"github.com/shopspring/decimal"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/apikeychannelrate"
	"github.com/looplj/axonhub/internal/ent/apikeygroup"
	"github.com/looplj/axonhub/internal/ent/apikeygroupchannelrate"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/samber/lo"
)

func requireRateAdmin(ctx context.Context, permission scopes.ScopeSlug) error {
	p, ok := authz.GetPrincipal(ctx)
	if !ok || p.Type != authz.PrincipalTypeUser && p.Type != authz.PrincipalTypeTest {
		return fmt.Errorf("administrative login required")
	}
	if _, ok := contexts.GetUser(ctx); !ok {
		return fmt.Errorf("administrative login required")
	}
	return authz.RequireScope(ctx, permission)
}

func (s *APIKeyService) rateTarget(ctx context.Context, keyID, groupID *int, write bool) (*int, error) {
	if (keyID == nil) == (groupID == nil) {
		return nil, fmt.Errorf("select exactly one rate owner")
	}
	permission := scopes.ScopeReadChannels
	keyPermission := scopes.ScopeReadAPIKeys
	if write {
		permission = scopes.ScopeWriteChannels
		keyPermission = scopes.ScopeWriteAPIKeys
	}
	if err := requireRateAdmin(ctx, permission); err != nil {
		return nil, err
	}
	if err := authz.RequireScope(ctx, keyPermission); err != nil {
		return nil, err
	}
	projectID, err := s.groupProject(ctx, write)
	if err != nil {
		return nil, err
	}
	db := s.entFromContext(ctx)
	if keyID != nil {
		key, err := db.APIKey.Query().Where(apikey.IDEQ(*keyID), apikey.ProjectIDEQ(projectID)).Select(apikey.FieldGroupRef, apikey.FieldType, apikey.FieldUserID).Only(ctx)
		if err != nil {
			return nil, err
		}
		u, _ := contexts.GetUser(ctx)
		if write && key.Type == apikey.TypePersonal && !u.IsOwner && key.UserID != u.ID {
			return nil, fmt.Errorf("personal key belongs to another user")
		}
		return key.GroupRef, nil
	}
	_, err = db.APIKeyGroup.Query().Where(apikeygroup.IDEQ(*groupID), apikeygroup.ProjectIDEQ(projectID)).OnlyID(ctx)
	return groupID, err
}

func readKeyChannelRates(ctx context.Context, db *ent.Client, keyID, groupID *int) (*objects.KeyChannelRates, error) {
	result := &objects.KeyChannelRates{Rules: []*objects.KeyChannelRate{}, Inherited: []*objects.KeyChannelRate{}}
	if groupID != nil {
		rows, err := db.APIKeyGroupChannelRate.Query().Where(apikeygroupchannelrate.GroupIDEQ(*groupID)).Order(ent.Asc(apikeygroupchannelrate.FieldChannelID)).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, v := range rows {
			result.Inherited = append(result.Inherited, &objects.KeyChannelRate{ChannelID: v.ChannelID, Multiplier: v.Multiplier})
		}
	}
	if keyID == nil {
		result.Rules, result.Inherited = result.Inherited, result.Rules
	} else {
		rows, err := db.APIKeyChannelRate.Query().Where(apikeychannelrate.KeyIDEQ(*keyID)).Order(ent.Asc(apikeychannelrate.FieldChannelID)).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, v := range rows {
			result.Rules = append(result.Rules, &objects.KeyChannelRate{ChannelID: v.ChannelID, Multiplier: v.Multiplier})
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	result.Revision = fmt.Sprintf("%x", sha256.Sum256(encoded))
	return result, nil
}

func (s *APIKeyService) KeyChannelRates(ctx context.Context, keyID, groupID *int) (*objects.KeyChannelRates, error) {
	group, err := s.rateTarget(ctx, keyID, groupID, false)
	if err != nil {
		return nil, err
	}
	db := s.entFromContext(ctx)
	return authz.RunAsHost(ctx, db, "read-key-channel-rates", func(ctx context.Context) (*objects.KeyChannelRates, error) {
		return readKeyChannelRates(ctx, db, keyID, group)
	})
}

func (s *APIKeyService) SaveKeyChannelRates(ctx context.Context, keyID, groupID *int, revision string, rules []*objects.KeyChannelRate) error {
	return s.RunInTransaction(ctx, func(ctx context.Context) error {
		projectID, ok := contexts.GetProjectID(ctx)
		if !ok {
			return fmt.Errorf("project context required")
		}
		if err := requireRateAdmin(ctx, scopes.ScopeWriteChannels); err != nil {
			return err
		}
		if err := s.lockProjectForAPIKeyName(ctx, projectID); err != nil {
			return err
		}
		group, err := s.rateTarget(ctx, keyID, groupID, true)
		if err != nil {
			return err
		}
		db := s.entFromContext(ctx)
		_, err = authz.RunAsHost(ctx, db, "save-key-channel-rates", func(ctx context.Context) (bool, error) {
			current, err := readKeyChannelRates(ctx, db, keyID, group)
			if err != nil {
				return false, err
			}
			if current.Revision != revision {
				return false, fmt.Errorf("channel rates changed; reload before saving")
			}
			ids := make([]int, 0, len(rules))
			seen := make(map[int]bool, len(rules))
			for _, r := range rules {
				if r == nil || r.ChannelID <= 0 || r.Multiplier.IsNegative() || seen[r.ChannelID] {
					return false, fmt.Errorf("invalid or duplicate channel rate")
				}
				seen[r.ChannelID] = true
				ids = append(ids, r.ChannelID)
			}
			if len(ids) > 0 {
				channels := db.Channel.Query().Where(channel.IDIn(ids...)).Order(ent.Asc(channel.FieldID))
				if db.Driver().Dialect() != dialect.SQLite {
					channels.Unique(false).Modify(func(q *sql.Selector) { q.ForUpdate() })
				}
				matched, err := channels.IDs(ctx)
				if err != nil {
					return false, err
				}
				if len(matched) != len(ids) {
					return false, fmt.Errorf("channel no longer exists")
				}
			}
			if keyID != nil {
				if _, err = db.APIKeyChannelRate.Delete().Where(apikeychannelrate.KeyIDEQ(*keyID)).Exec(ctx); err != nil {
					return false, err
				}
				for _, r := range rules {
					if err = db.APIKeyChannelRate.Create().SetKeyID(*keyID).SetChannelID(r.ChannelID).SetMultiplier(r.Multiplier).Exec(ctx); err != nil {
						return false, err
					}
				}
			} else {
				if _, err = db.APIKeyGroupChannelRate.Delete().Where(apikeygroupchannelrate.GroupIDEQ(*group)).Exec(ctx); err != nil {
					return false, err
				}
				for _, r := range rules {
					if err = db.APIKeyGroupChannelRate.Create().SetGroupID(*group).SetChannelID(r.ChannelID).SetMultiplier(r.Multiplier).Exec(ctx); err != nil {
						return false, err
					}
				}
			}
			return true, nil
		})
		return err
	})
}

// Resolve the request's frozen owner and both prices in a single database read.
func executionBilling(ctx context.Context, db *ent.Client, req *ent.Request, channelID int) (*objects.ExecutionBilling, error) {
	return authz.RunAsHost(ctx, db, "execution-key-channel-price", func(ctx context.Context) (*objects.ExecutionBilling, error) {
		var rows []struct {
			GroupID   *int             `json:"group_id"`
			GroupName *string          `json:"group_name"`
			KeyRate   *decimal.Decimal `json:"key_rate"`
			GroupRate *decimal.Decimal `json:"group_rate"`
		}
		err := db.APIKey.Query().Where(apikey.IDEQ(req.APIKeyID), apikey.ProjectIDEQ(req.ProjectID)).Modify(func(q *sql.Selector) {
			requestRow := sql.Table(request.Table)
			group := sql.Table(apikeygroup.Table)
			keyRate := sql.Table(apikeychannelrate.Table)
			groupRate := sql.Table(apikeygroupchannelrate.Table)
			q.Join(requestRow).OnP(sql.And(sql.EQ(requestRow.C("id"), req.ID), sql.ColumnsEQ(requestRow.C("project_id"), q.C("project_id")), sql.ColumnsEQ(requestRow.C("api_key_id"), q.C("id"))))
			group.SetDialect(q.Dialect())
			owner := "NULLIF(COALESCE(" + requestRow.C("billing_group_ref") + "," + q.C("group_ref") + "),0)"
			q.LeftJoin(group).OnP(sql.And(sql.ExprP(group.C("id")+" = "+owner), sql.ColumnsEQ(group.C("project_id"), q.C("project_id"))))
			q.LeftJoin(keyRate).OnP(sql.And(sql.ColumnsEQ(keyRate.C("key_id"), q.C("id")), sql.EQ(keyRate.C("channel_id"), channelID)))
			q.LeftJoin(groupRate).OnP(sql.And(sql.ColumnsEQ(groupRate.C("group_id"), group.C("id")), sql.EQ(groupRate.C("channel_id"), channelID)))
			q.Select(sql.As(owner, "group_id"), sql.As(group.C("name"), "group_name"), sql.As(keyRate.C("multiplier"), "key_rate"), sql.As(groupRate.C("multiplier"), "group_rate"))
		}).Scan(ctx, &rows)
		if err != nil {
			return nil, err
		}
		if len(rows) != 1 {
			return nil, fmt.Errorf("request billing owner not found")
		}
		row := rows[0]
		result := &objects.ExecutionBilling{GroupID: row.GroupID, GroupName: lo.FromPtr(row.GroupName)}
		if row.KeyRate != nil {
			result.Rate, result.Source = row.KeyRate, "key"
		} else if row.GroupRate != nil {
			result.Rate, result.Source = row.GroupRate, "group"
		}
		return result, nil
	})
}

// The optional same-price routing optimization compares inherited rates only after
// the regular channel price check. An explicit rate must match on both candidates.
func (s *RequestService) CallerRatesForRouting(ctx context.Context, key *ent.APIKey) (map[int]string, error) {
	db := s.entFromContext(ctx)
	return authz.RunAsHost(ctx, db, "same-price-caller-rates", func(ctx context.Context) (map[int]string, error) {
		current, err := db.APIKey.Query().Where(apikey.IDEQ(key.ID), apikey.ProjectIDEQ(key.ProjectID)).Select(apikey.FieldGroupRef).Only(ctx)
		if err != nil {
			return nil, err
		}
		rates, err := readKeyChannelRates(ctx, db, &key.ID, current.GroupRef)
		if err != nil {
			return nil, err
		}
		result := map[int]string{}
		for _, r := range rates.Inherited {
			result[r.ChannelID] = r.Multiplier.String()
		}
		for _, r := range rates.Rules {
			result[r.ChannelID] = r.Multiplier.String()
		}
		return result, nil
	})
}

func (s *RequestService) RefreshBillingGroup(ctx context.Context, req *ent.Request, exec *ent.RequestExecution) error {
	db := s.entFromContext(ctx)
	billing, err := executionBilling(ctx, db, req, exec.ChannelID)
	if err != nil {
		return err
	}
	snap := *exec.CostSnapshot
	snap.Billing = billing
	if err = db.RequestExecution.UpdateOneID(exec.ID).Where(requestexecution.RequestIDEQ(req.ID), requestexecution.ProjectIDEQ(req.ProjectID), requestexecution.StatusEQ(requestexecution.StatusProcessing)).SetCostSnapshot(&snap).Exec(ctx); err != nil {
		return err
	}
	exec.CostSnapshot = &snap
	return nil
}

// Channel deletion is soft and production migrations disable foreign keys.
func removeChannelRates(ctx context.Context, db *ent.Client, ids []int) error {
	_, err := authz.RunAsHost(ctx, db, "delete-channel-pricing-rules", func(ctx context.Context) (int, error) {
		if _, err := db.APIKeyChannelRate.Delete().Where(apikeychannelrate.ChannelIDIn(ids...)).Exec(ctx); err != nil {
			return 0, err
		}
		return db.APIKeyGroupChannelRate.Delete().Where(apikeygroupchannelrate.ChannelIDIn(ids...)).Exec(ctx)
	})
	return err
}
