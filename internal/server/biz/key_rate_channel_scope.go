package biz

import (
	"cmp"
	"context"
	"slices"
	"sync"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/ent/project"
	"github.com/looplj/axonhub/internal/objects"
)

func (s *APIKeyService) KeyRateChannelScope(ctx context.Context, models *ModelService, keyID int) (*objects.KeyRateChannelScope, error) {
	if _, err := s.rateTarget(ctx, &keyID, nil, false); err != nil {
		return nil, err
	}
	projectID, _ := contexts.GetProjectID(ctx)
	db := s.entFromContext(ctx)
	return authz.RunAsHost(ctx, db, "read-key-rate-channel-scope", func(ctx context.Context) (*objects.KeyRateChannelScope, error) {
		key, err := db.APIKey.Query().Where(apikey.IDEQ(keyID), apikey.ProjectIDEQ(projectID)).
			Select(apikey.FieldID, apikey.FieldProjectID, apikey.FieldProfiles).
			WithProject(func(q *ent.ProjectQuery) { q.Select(project.FieldID, project.FieldProfiles) }).Only(ctx)
		if err != nil {
			return nil, err
		}
		return models.rateChannelScope(ctx, key)
	})
}

func (svc *ModelService) rateChannelScope(ctx context.Context, key *ent.APIKey) (*objects.KeyRateChannelScope, error) {
	result := &objects.KeyRateChannelScope{}
	var profiles []objects.APIKeyProfile
	if key.Profiles != nil {
		profiles = key.Profiles.Profiles
	}
	if active := key.GetActiveProfile(); active != nil {
		result.ActiveProfile = active.Name
	} else {
		// 没有启用配置时，请求只受项目限制，即使密钥已经加载了其他模板。
		profiles = append([]objects.APIKeyProfile{{}}, profiles...)
	}

	enabled := svc.channelService.GetEnabledChannels()
	byID := make(map[int]*objects.KeyRateChannel)
	loadModels := sync.OnceValues(func() (modelResolver, error) {
		entries, err := svc.entFromContext(ctx).Model.Query().Where(model.StatusEQ(model.StatusEnabled)).All(ctx)
		return newModelResolver(entries), err
	})
	settings := sync.OnceValue(func() *SystemModelSettings { return svc.modelSettingsOrDefault(ctx) })
	for i := range profiles {
		profile := &profiles[i]
		result.Profiles = append(result.Profiles, profile.Name)
		scope := svc.modelChannelScope(ctx, key.Edges.Project.GetActiveProfile(), profile)
		allowed := make([]*Channel, 0, len(enabled))
		for _, ch := range enabled {
			if scope == nil || scope.Allows(ch.Channel) {
				allowed = append(allowed, ch)
			}
		}
		var modelChannels map[int]bool
		if len(profile.ModelIDs) != 0 {
			resolver, err := loadModels()
			if err != nil {
				return nil, err
			}
			modelChannels, err = rateChannelsForModels(profile, scope, allowed, resolver, settings())
			if err != nil {
				return nil, err
			}
		}
		for _, ch := range allowed {
			if modelChannels != nil && !modelChannels[ch.ID] {
				continue
			}
			row := byID[ch.ID]
			if row == nil {
				row = &objects.KeyRateChannel{ChannelID: ch.ID, Name: ch.Name, Tags: ch.Tags}
				byID[ch.ID] = row
				result.Channels = append(result.Channels, row)
			}
			row.Profiles = append(row.Profiles, profile.Name)
		}
	}
	slices.SortFunc(result.Channels, func(a, b *objects.KeyRateChannel) int { return cmp.Compare(a.ChannelID, b.ChannelID) })
	return result, nil
}

func rateChannelsForModels(profile *objects.APIKeyProfile, scope *ModelChannelScope, channels []*Channel, resolver modelResolver, settings *SystemModelSettings) (map[int]bool, error) {
	ids := make(map[int]bool)
	for _, publicID := range profile.ModelIDs {
		target := profile.MapModel(publicID)
		for matched := range resolver.matching(target) {
			associations := EffectiveModelAssociationsForRequest(settings, matched, target)
			connections := MatchConnectionsForModel(associations, channels, target)
			if len(connections) == 0 && scope != nil {
				excluded, err := scope.ExcludesAllTargets(associations)
				if err != nil {
					return nil, err
				}
				if excluded {
					continue
				}
			}
			for _, connection := range connections {
				ids[connection.Channel.ID] = true
			}
			break
		}
	}
	return ids, nil
}
