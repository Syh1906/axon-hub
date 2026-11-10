package backup

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/log"
)

// Only immutable usage identity participates in restore matching. Late settlement
// may update tokens and prices without creating a new ledger entry. Counts keep
// separate entries even when their immutable fields are identical.
type usageRestoreIdentity struct {
	RequestID, ProjectID, ChannelID int
	ModelID, Format                 string
	Source                          usagelog.Source
	CreatedAt                       time.Time
}

func usageRestoreKey(row *ent.UsageLog) usageRestoreIdentity {
	return usageRestoreIdentity{
		RequestID: row.RequestID, ProjectID: row.ProjectID, ChannelID: row.ChannelID,
		ModelID: row.ModelID, Format: row.Format, Source: row.Source, CreatedAt: row.CreatedAt.UTC(),
	}
}

func (svc *BackupService) restoreUsageLogs(
	ctx context.Context,
	db *ent.Client,
	usageLogs []*BackupUsageLog,
	requestIDMap map[int]int,
	resolver *usageRestoreResolver,
) error {
	if len(usageLogs) == 0 {
		return nil
	}

	if err := svc.ensureUsageLogRequests(ctx, db, usageLogs, requestIDMap, resolver); err != nil {
		return err
	}

	requestIDs := make([]int, 0, len(requestIDMap))
	for _, requestID := range requestIDMap {
		requestIDs = append(requestIDs, requestID)
	}

	existingLogs := make([]*ent.UsageLog, 0)
	for start := 0; start < len(requestIDs); start += backupBatchSize {
		end := min(start+backupBatchSize, len(requestIDs))
		logs, err := db.UsageLog.Query().Where(usagelog.RequestIDIn(requestIDs[start:end]...)).All(ctx)
		if err != nil {
			return err
		}
		existingLogs = append(existingLogs, logs...)
	}

	restoredSourceIDs := map[int]struct{}{}
	candidates := make([]*ent.UsageLog, 0, len(usageLogs))
	for _, usageData := range usageLogs {
		if usageData == nil {
			continue
		}

		requestID, ok := requestIDMap[usageData.RequestID]
		if !ok {
			log.Warn(ctx, "request not found for restoring usage log, skipping",
				log.Int("usage_log_id", usageData.ID),
				log.Int("request_id", usageData.RequestID),
			)
			continue
		}

		if _, duplicate := restoredSourceIDs[usageData.ID]; usageData.ID != 0 && duplicate {
			continue
		}

		projectID, ok := resolver.resolveProjectID(usageData.ProjectID, usageData.ProjectName)
		if !ok {
			log.Warn(ctx, "project not found for restoring usage log, skipping",
				log.Int("usage_log_id", usageData.ID),
				log.String("project", usageData.ProjectName),
			)
			continue
		}

		channelID, resolveErr := resolver.resolveChannelID(usageData.ChannelID, usageData.ChannelName)
		if resolveErr != nil {
			return resolveErr
		}
		if channelID == 0 && hasBackupChannelRef(usageData.ChannelID, usageData.ChannelName) {
			log.Warn(ctx, "channel not found for restoring usage log, restoring with null channel",
				log.Int("usage_log_id", usageData.ID),
				log.Int("channel_id", usageData.ChannelID),
				log.String("channel", usageData.ChannelName),
			)
		}

		apiKeyID, ok := resolver.resolveAPIKeyID(usageData.APIKeyKey)
		if !ok && usageData.APIKeyKey != "" {
			log.Warn(ctx, "API key not found for restoring usage log, restoring with null API key",
				log.Int("usage_log_id", usageData.ID),
			)
		}

		if usageData.ID != 0 {
			restoredSourceIDs[usageData.ID] = struct{}{}
		}
		row := usageData.UsageLog
		row.RequestID, row.ProjectID, row.ChannelID, row.APIKeyID = requestID, projectID, channelID, apiKeyID
		candidates = append(candidates, &row)
	}
	matches, err := matchRestoredUsage(existingLogs, candidates)
	if err != nil {
		return err
	}
	builders := make([]*ent.UsageLogCreate, 0, min(len(candidates), backupBatchSize))
	flush := func() error {
		if len(builders) == 0 {
			return nil
		}
		if _, err := db.UsageLog.CreateBulk(builders...).Save(ctx); err != nil {
			return fmt.Errorf("failed to restore usage logs: %w", err)
		}
		builders = builders[:0]
		return nil
	}
	for i, usageData := range candidates {
		if matches[i] {
			continue
		}
		builders = append(builders, db.UsageLog.Create().
			SetCreatedAt(usageData.CreatedAt).
			SetUpdatedAt(usageData.UpdatedAt).
			SetRequestID(usageData.RequestID).
			SetNillableAPIKeyID(nilIfZero(usageData.APIKeyID)).
			SetProjectID(usageData.ProjectID).
			SetNillableChannelID(nilIfZero(usageData.ChannelID)).
			SetModelID(usageData.ModelID).
			SetPromptTokens(usageData.PromptTokens).
			SetCompletionTokens(usageData.CompletionTokens).
			SetTotalTokens(usageData.TotalTokens).
			SetPromptAudioTokens(usageData.PromptAudioTokens).
			SetPromptCachedTokens(usageData.PromptCachedTokens).
			SetPromptWriteCachedTokens(usageData.PromptWriteCachedTokens).
			SetPromptWriteCachedTokens5m(usageData.PromptWriteCachedTokens5m).
			SetPromptWriteCachedTokens1h(usageData.PromptWriteCachedTokens1h).
			SetCompletionAudioTokens(usageData.CompletionAudioTokens).
			SetCompletionReasoningTokens(usageData.CompletionReasoningTokens).
			SetCompletionAcceptedPredictionTokens(usageData.CompletionAcceptedPredictionTokens).
			SetCompletionRejectedPredictionTokens(usageData.CompletionRejectedPredictionTokens).
			SetSource(usageData.Source).
			SetFormat(usageData.Format).
			SetNillableTotalCost(usageData.TotalCost).
			SetCostItems(usageData.CostItems).
			SetCostSnapshot(usageData.CostSnapshot).
			SetNillableCostPriceReferenceID(nilIfEmpty(usageData.CostPriceReferenceID)))

		if len(builders) >= backupBatchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}

	return flush()
}

// Reserve exact billing matches across the whole input before matching a row
// whose mutable settlement changed. This also repairs a partial restore when
// multiple bills have the same immutable identity but different amounts.
func matchRestoredUsage(existing, incoming []*ent.UsageLog) ([]bool, error) {
	type contentKey struct {
		identity usageRestoreIdentity
		content  [32]byte
	}
	exact := map[contentKey]int{}
	remaining := map[usageRestoreIdentity]int{}
	fingerprint := func(row *ent.UsageLog) (contentKey, error) {
		copy := *row
		copy.ID, copy.RequestID, copy.ProjectID, copy.ChannelID, copy.APIKeyID = 0, 0, 0, 0, 0
		copy.CreatedAt, copy.UpdatedAt = time.Time{}, time.Time{}
		data, err := (BackupUsageLog{UsageLog: copy}).MarshalJSON()
		return contentKey{usageRestoreKey(row), sha256.Sum256(data)}, err
	}
	for _, row := range existing {
		key, err := fingerprint(row)
		if err != nil {
			return nil, err
		}
		exact[key]++
		remaining[key.identity]++
	}
	keys := make([]contentKey, len(incoming))
	matched := make([]bool, len(incoming))
	for i, row := range incoming {
		key, err := fingerprint(row)
		if err != nil {
			return nil, err
		}
		keys[i] = key
		if exact[key] > 0 {
			exact[key]--
			remaining[key.identity]--
			matched[i] = true
		}
	}
	for i, key := range keys {
		if !matched[i] && remaining[key.identity] > 0 {
			remaining[key.identity]--
			matched[i] = true
		}
	}
	return matched, nil
}
