package backup

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
)

// ChannelReference carries display metadata, never credentials. IDs belong to
// the source backup and must be mapped before writing to the target database.
type ChannelReference struct {
	ID        int      `json:"id"`
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Tags      []string `json:"tags"`
	HasConfig bool     `json:"has_config"`
}

type ChannelRestoreMapping struct {
	SourceID   int
	SourceName *string // Legacy price-only backups identify the source by name.
	TargetID   int     // Zero explicitly creates a new channel.
}

func (svc *BackupService) streamChannelReferences(ctx context.Context, o *objWriter, opts BackupOptions) error {
	include := opts.IncludeChannels || opts.IncludeModels || opts.IncludeAPIKeys || opts.IncludeModelPrices || opts.IncludeUsageStats || opts.IncludeRequestLogs || opts.IncludeProjects || opts.IncludeSystemConfigs
	return streamArrayField(o, "channel_references", include, false,
		func(lastID int) ([]*ent.Channel, int, error) {
			rows, err := svc.db.Channel.Query().Select(channel.FieldID, channel.FieldName, channel.FieldType, channel.FieldTags).
				Where(channel.IDGT(lastID)).Order(ent.Asc(channel.FieldID)).Limit(backupBatchSize).All(ctx)
			next := 0
			if len(rows) > 0 {
				next = rows[len(rows)-1].ID
			}
			return rows, next, err
		}, func(ch *ent.Channel) ([]byte, bool, error) {
			b, err := json.Marshal(ChannelReference{ID: ch.ID, Name: ch.Name, Type: string(ch.Type), Tags: ch.Tags, HasConfig: opts.IncludeChannels})
			return b, true, err
		})
}

type channelRestoreState struct {
	ids           map[int]int
	explicit      map[int]bool
	targets       map[int]*ent.Channel
	targetNames   map[string][]int
	sourceNames   map[string][]int
	sources       map[int]string
	targetSources map[int]string
	legacyNames   map[string]bool
	nameTargets   map[string]int
}

func newChannelRestoreState(ctx context.Context, db *ent.Client, data BackupData, opts RestoreOptions) (*channelRestoreState, error) {
	s := &channelRestoreState{ids: map[int]int{}, explicit: map[int]bool{}, targets: map[int]*ent.Channel{}, targetNames: map[string][]int{}, sourceNames: map[string][]int{}, sources: map[int]string{}, targetSources: map[int]string{}, legacyNames: map[string]bool{}, nameTargets: map[string]int{}}
	creatable := map[int]bool{}
	for _, ref := range data.ChannelReferences {
		if ref != nil {
			s.sources[ref.ID] = ref.Name
		}
	}
	for _, ch := range data.Channels {
		if ch != nil {
			s.sources[ch.ID] = ch.Name
			creatable[ch.ID] = ch.Credentials.APIKey != "" || len(ch.Credentials.APIKeys) > 0 || ch.Credentials.OAuth != nil
		}
	}
	for _, price := range data.ChannelModelPrices {
		if price != nil && price.ChannelID == 0 && price.ChannelName != "" {
			s.legacyNames[price.ChannelName] = true
		}
	}
	addSource := func(id int, name string) {
		if id > 0 {
			if _, exists := s.sources[id]; !exists {
				s.sources[id] = name
			}
		}
	}
	if opts.IncludeModelPrices {
		for _, p := range data.ChannelModelPrices {
			if p != nil {
				addSource(p.ChannelID, p.ChannelName)
			}
		}
	}
	if opts.IncludeUsageStats || opts.IncludeRequestLogs {
		for _, r := range data.UsageRequests {
			if r != nil {
				addSource(r.ChannelID, r.ChannelName)
			}
		}
		for _, r := range data.UsageLogs {
			if r != nil {
				addSource(r.ChannelID, r.ChannelName)
			}
		}
	}
	if opts.IncludeAPIKeys {
		for _, c := range data.BudgetCharges {
			if c != nil && c.AsyncTask != nil {
				addSource(c.AsyncTask.ChannelID, c.AsyncTask.ChannelName)
			}
		}
	}
	for _, id := range configurationChannelIDs(data, opts) {
		addSource(id, "")
	}
	for id, name := range s.sources {
		if id > 0 {
			s.sourceNames[name] = append(s.sourceNames[name], id)
		}
	}
	rows, err := db.Channel.Query().Select(channel.FieldID, channel.FieldName).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, ch := range rows {
		s.targets[ch.ID] = ch
		s.targetNames[ch.Name] = append(s.targetNames[ch.Name], ch.ID)
	}
	for _, mapping := range opts.ChannelMappings {
		if mapping == nil {
			return nil, fmt.Errorf("channel mapping is empty")
		}
		if mapping.SourceName != nil {
			name := *mapping.SourceName
			if mapping.SourceID != 0 || !s.legacyNames[name] || len(s.sourceNames[name]) > 0 {
				return nil, fmt.Errorf("unknown legacy source channel %q; use its source ID when available", name)
			}
			if _, exists := s.nameTargets[name]; exists {
				return nil, fmt.Errorf("duplicate mapping for source channel %q", name)
			}
			if mapping.TargetID <= 0 || s.targets[mapping.TargetID] == nil {
				return nil, fmt.Errorf("legacy price-only channel %q requires an existing target channel", name)
			}
			if err := s.claimTarget("name:"+name, mapping.TargetID); err != nil {
				return nil, err
			}
			s.nameTargets[name] = mapping.TargetID
			continue
		}
		if _, ok := s.sources[mapping.SourceID]; !ok {
			return nil, fmt.Errorf("unknown source channel %d", mapping.SourceID)
		}
		if s.explicit[mapping.SourceID] {
			return nil, fmt.Errorf("duplicate mapping for source channel %d", mapping.SourceID)
		}
		if mapping.TargetID < 0 || (mapping.TargetID > 0 && s.targets[mapping.TargetID] == nil) {
			return nil, fmt.Errorf("target channel %d does not exist", mapping.TargetID)
		}
		if mapping.TargetID == 0 && !opts.IncludeChannels {
			return nil, fmt.Errorf("creating a new channel requires including channels in the restore")
		}
		if mapping.TargetID == 0 && !creatable[mapping.SourceID] {
			return nil, fmt.Errorf("source channel %d has no restorable configuration; select an existing target or include channel configuration in the backup", mapping.SourceID)
		}
		s.explicit[mapping.SourceID] = true
		if err := s.assign(mapping.SourceID, mapping.TargetID); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *channelRestoreState) resolve(sourceID int, name string) (int, error) {
	if sourceID == 0 {
		if id, ok := s.nameTargets[name]; ok {
			return id, nil
		}
	}
	if id, ok := s.ids[sourceID]; sourceID != 0 && ok {
		return id, nil
	}
	if sourceID > 0 && s.sources[sourceID] != "" {
		name = s.sources[sourceID]
	}
	if name == "" {
		return 0, nil
	}
	sources := s.sourceNames[name]
	if len(sources) == 1 {
		if id, ok := s.ids[sources[0]]; ok {
			return id, nil
		}
	}
	if len(sources) > 1 || len(s.targetNames[name]) > 1 {
		return 0, fmt.Errorf("channel %q is ambiguous; select a target channel for source ID %d", name, sourceID)
	}
	if len(s.targetNames[name]) == 1 {
		id := s.targetNames[name][0]
		if sourceID > 0 {
			if err := s.assign(sourceID, id); err != nil {
				return 0, err
			}
		}
		return id, nil
	}
	return 0, nil
}

func (s *channelRestoreState) assign(sourceID, targetID int) error {
	if sourceID > 0 && targetID > 0 {
		if err := s.claimTarget(fmt.Sprintf("id:%d", sourceID), targetID); err != nil {
			return err
		}
	}
	s.ids[sourceID] = targetID
	return nil
}

func (s *channelRestoreState) claimTarget(source string, targetID int) error {
	if owner, used := s.targetSources[targetID]; used && owner != source {
		return fmt.Errorf("multiple source channels cannot map to target channel %d", targetID)
	}
	s.targetSources[targetID] = source
	return nil
}

// Only configuration references require an existing channel. Historical usage
// may retain its existing null-channel behavior when a channel was deleted.
func (s *channelRestoreState) resolveConfigurationReferences(data BackupData, opts RestoreOptions) error {
	for _, sourceID := range configurationChannelIDs(data, opts) {
		id, err := s.resolve(sourceID, s.sources[sourceID])
		if err != nil {
			return err
		}
		if id == 0 {
			return fmt.Errorf("configuration requires source channel %d; include channels or select its restore target", sourceID)
		}
		s.ids[sourceID] = id
	}
	return nil
}

func configurationChannelIDs(data BackupData, opts RestoreOptions) []int {
	var ids []int
	addExcludes := func(excludes []*objects.ExcludeAssociation) {
		for _, ex := range excludes {
			if ex != nil {
				ids = append(ids, ex.ChannelIds...)
			}
		}
	}
	if opts.IncludeModels {
		for _, m := range data.Models {
			if m == nil || m.Settings == nil {
				continue
			}
			for _, a := range m.Settings.Associations {
				if a == nil {
					continue
				}
				if a.ChannelModel != nil {
					ids = append(ids, a.ChannelModel.ChannelID)
				}
				if a.ChannelRequest != nil {
					ids = append(ids, a.ChannelRequest.ChannelID)
				}
				if a.ChannelRegex != nil {
					ids = append(ids, a.ChannelRegex.ChannelID)
				}
				if a.Regex != nil {
					addExcludes(a.Regex.Exclude)
				}
				if a.ModelID != nil {
					addExcludes(a.ModelID.Exclude)
				}
			}
		}
	}
	if opts.IncludeProjects {
		for _, p := range data.Projects {
			if p != nil && p.Profiles != nil {
				for _, profile := range p.Profiles.Profiles {
					ids = append(ids, profile.ChannelIDs...)
				}
			}
		}
	}
	if opts.IncludeAPIKeys {
		for _, k := range data.APIKeys {
			if k != nil && k.Profiles != nil {
				for _, profile := range k.Profiles.Profiles {
					ids = append(ids, profile.ChannelIDs...)
					for _, priority := range profile.ChannelPriorities {
						ids = append(ids, priority.ChannelID)
					}
				}
			}
		}
	}
	return ids
}
