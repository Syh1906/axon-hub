package backup

import (
	"context"
	"fmt"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/samber/lo"
)

func (svc *BackupService) restoreUsageArchives(ctx context.Context, db *ent.Client, records []*BackupUsageArchive, retained []*BackupUsageLog, requests map[int]int, resolver *usageRestoreResolver) error {
	if len(records) == 0 {
		return nil
	}
	// Detached source IDs never occupy the positive space used by live rows.
	next := -1
	first, err := db.UsageArchive.Query().Order(ent.Asc(usagearchive.FieldSourceID)).First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return err
	}
	if first != nil && first.SourceID < 0 {
		next = first.SourceID - 1
	}
	alloc := func() int { v := next; next--; return v }
	// Usage-only restores can have a retained request shell whose date differs from
	// the original request. Its identified bill still proves the target request ID.
	refs := map[string]*BackupUsageArchive{}
	for _, r := range records {
		if r != nil && r.Kind == usagearchive.KindUsage {
			refs[archiveRestoreRef(r)] = r
		}
	}
	knownLive := map[string]bool{}
	keys := lo.Keys(refs)
	for start := 0; start < len(keys); start += backupBatchSize {
		rows, err := db.UsageLog.Query().Where(usagelog.RestoreRefIn(keys[start:min(start+backupBatchSize, len(keys))]...)).Select(usagelog.FieldRestoreRef, usagelog.FieldRequestID, usagelog.FieldProjectID).All(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			r := refs[*row.RestoreRef]
			p, ok := resolver.resolveProjectID(r.ProjectID, r.ProjectName)
			if ok && row.ProjectID == p {
				knownLive[*row.RestoreRef] = true
				requests[r.RequestID] = row.RequestID
			}
		}
	}
	// Restore request facts before their possibly later-dated usage rows.
	ordered := make([]*BackupUsageArchive, 0, len(records))
	for _, r := range records {
		if r == nil {
			return fmt.Errorf("missing archived record")
		}
		if r.Kind == usagearchive.KindRequest {
			ordered = append(ordered, r)
		}
	}
	for _, r := range records {
		if r.Kind != usagearchive.KindRequest {
			ordered = append(ordered, r)
		}
	}
	type billKey struct {
		request, channel int
		model            string
		created          time.Time
	}
	protected := map[billKey]int{}
	seen := map[int]bool{}
	for _, l := range retained {
		if l == nil || seen[l.ID] {
			continue
		}
		seen[l.ID] = true
		protected[billKey{l.RequestID, l.ChannelID, l.ModelID, l.CreatedAt.UTC()}]++
	}
	consumed := map[int]bool{}
	for _, r := range ordered {
		project, ok := resolver.resolveProjectID(r.ProjectID, r.ProjectName)
		if !ok {
			continue
		}
		ch, err := resolver.resolveChannelID(r.ChannelID, r.ChannelName)
		if err != nil {
			return err
		}
		key, _ := resolver.resolveAPIKeyID(r.APIKeyKey)
		ref := archiveRestoreRef(r)
		found, err := db.UsageArchive.Query().Where(usagearchive.RestoreRefEQ(ref), usagearchive.ProjectIDEQ(project)).Only(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}
		if found != nil {
			requests[r.RequestID] = found.RequestID
			continue
		}
		// An in-place restore may already contain the native archive, without a restore reference.
		native, err := db.UsageArchive.Query().Where(usagearchive.RestoreRefIsNil(), usagearchive.KindEQ(r.Kind), usagearchive.SourceIDEQ(r.SourceID), usagearchive.ProjectIDEQ(project), usagearchive.ChannelIDEQ(ch), usagearchive.OccurredAtEQ(r.OccurredAt), usagearchive.ModelIDEQ(r.ModelID)).Only(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}
		if native != nil {
			requests[r.RequestID] = native.RequestID
			continue
		}
		rid := requests[r.RequestID]
		if knownLive[ref] {
			continue
		}
		if rid == 0 && r.Kind == usagearchive.KindRequest {
			ids, err := db.Request.Query().Where(request.ProjectIDEQ(project), request.Or(request.RestoreRefEQ(ref), request.And(request.RestoreRefIsNil(), request.CreatedAtEQ(r.OccurredAt), request.ModelIDEQ(r.RequestModelID), request.SourceEQ(request.Source(r.RequestSource)), request.StatusEQ(request.Status(r.RequestStatus))))).IDs(ctx)
			if err != nil {
				return err
			}
			if len(ids) == 1 {
				rid = ids[0]
			}
		}
		if rid == 0 && r.Parent != nil {
			// Only portable request metadata was exported for this retained parent.
			if r.Parent.ID != r.RequestID || r.Parent.ProjectID != r.ProjectID {
				return fmt.Errorf("archived billing parent mismatch")
			}
			r.Parent.RequestBody = []byte("{}")
			originalRef := r.Parent.RestoreRef
			shellRef := requestShellRef(objects.UsageRestoreRef(originalRef, "request", r.Parent.ProjectID, r.Parent.ID, r.Parent.CreatedAt))
			r.Parent.RestoreRef = &shellRef
			ids, err := svc.restoreUsageRequests(ctx, db, []*BackupUsageRequest{r.Parent}, resolver)
			if err != nil {
				return err
			}
			rid = ids[r.RequestID]
		}
		if rid == 0 {
			rid = alloc()
		}
		requests[r.RequestID] = rid
		if r.Kind == usagearchive.KindUsage && rid > 0 {
			// Restoring a later archive over its retained bill must not add a second bill.
			live, err := db.UsageLog.Query().Where(usagelog.RestoreRefIsNil(), usagelog.RequestIDEQ(rid), usagelog.ProjectIDEQ(project), usagelog.ChannelIDEQ(ch), usagelog.ModelIDEQ(r.ModelID), usagelog.CreatedAtEQ(r.OccurredAt)).Order(ent.Asc(usagelog.FieldID)).IDs(ctx)
			if err != nil {
				return err
			}
			matched := false
			exclude := protected[billKey{r.RequestID, r.ChannelID, r.ModelID, r.OccurredAt.UTC()}]
			for _, id := range live[min(exclude, len(live)):] {
				if !consumed[id] {
					consumed[id] = true
					matched = true
					break
				}
			}
			if matched {
				continue
			}
		}
		src := rid
		if r.Kind != usagearchive.KindRequest {
			src = alloc()
		}
		group := r.BillingGroupID
		if lo.FromPtr(group) != 0 {
			g, err := restoredBillingGroup(ctx, db, project, r.BillingGroupName)
			if err != nil {
				return err
			}
			group = &g
		}
		create := db.UsageArchive.Create().SetRestoreRef(ref).SetKind(r.Kind).SetSourceID(src).SetRequestID(rid).SetProjectID(project).SetAPIKeyID(key).SetChannelID(ch).SetModelID(r.ModelID).
			SetOccurredAt(r.OccurredAt).SetNillableClientSessionID(r.ClientSessionID).SetRequestModelID(r.RequestModelID).SetRequestStatus(r.RequestStatus).SetRequestSource(r.RequestSource).
			SetNillableBillingGroupID(group).SetNillableBilledCost(r.BilledCost).SetBillingEstimated(r.BillingEstimated).SetBillingEvidence(r.BillingEvidence).SetNillableTotalCost(r.TotalCost).
			SetCacheCreationCost(r.CacheCreationCost).SetFirstTokenSum(r.FirstTokenSum).SetSpeedSum(r.SpeedSum).SetNillablePricingComplete(r.PricingComplete).
			SetSpeedVersion(r.SpeedVersion).SetBillingVersion(r.BillingVersion).SetPromptTokens(r.PromptTokens).SetCompletionTokens(r.CompletionTokens).SetTotalTokens(r.TotalTokens).
			SetPromptCachedTokens(r.PromptCachedTokens).SetPromptWriteCachedTokens(r.PromptWriteCachedTokens).SetCompletionReasoningTokens(r.CompletionReasoningTokens).
			SetCacheRequests(r.CacheRequests).SetCacheInput(r.CacheInput).SetSuccessCount(r.SuccessCount).SetOutcomeCount(r.OutcomeCount).SetFirstTokenCount(r.FirstTokenCount).SetSpeedCount(r.SpeedCount)
		if err := create.Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Resolve archived identities before creating request shells or matching mutable bill contents.
func filterArchivedBackup(ctx context.Context, db *ent.Client, requests []*BackupUsageRequest, logs []*BackupUsageLog, resolver *usageRestoreResolver) ([]*BackupUsageRequest, []*BackupUsageLog, map[int]int, error) {
	type sourceRecord struct {
		ref                               string
		project, channel, id, parent, key int
		kind                              usagearchive.Kind
		model                             string
		at                                time.Time
	}
	batch := make([]sourceRecord, 0, len(requests)+len(logs))
	add := func(saved *string, kind usagearchive.Kind, id, parent, projectID, channelID int, projectName, channelName, apiKey, model string, at time.Time) error {
		project, ok := resolver.resolveProjectID(projectID, projectName)
		if !ok {
			return nil
		}
		ch, err := resolver.resolveChannelID(channelID, channelName)
		if err != nil {
			return err
		}
		key, _ := resolver.resolveAPIKeyID(apiKey)
		batch = append(batch, sourceRecord{objects.UsageRestoreRef(saved, string(kind), projectID, id, at), project, ch, id, parent, key, kind, model, at})
		return nil
	}
	for _, r := range requests {
		if r != nil {
			if err := add(r.RestoreRef, usagearchive.KindRequest, r.ID, r.ID, r.ProjectID, r.ChannelID, r.ProjectName, r.ChannelName, r.APIKeyKey, r.ModelID, r.CreatedAt); err != nil {
				return nil, nil, nil, err
			}
		}
	}
	for _, r := range logs {
		if r != nil {
			if err := add(r.RestoreRef, usagearchive.KindUsage, r.ID, r.RequestID, r.ProjectID, r.ChannelID, r.ProjectName, r.ChannelName, r.APIKeyKey, r.ModelID, r.CreatedAt); err != nil {
				return nil, nil, nil, err
			}
		}
	}
	parents := map[int]int{}
	seenRequests, seenBills := map[int]bool{}, map[int]bool{}
	for start := 0; start < len(batch); start += backupBatchSize {
		records := batch[start:min(start+backupBatchSize, len(batch))]
		refs := make([]string, 0, len(records))
		ids := make([]int, 0, len(records))
		for _, r := range records {
			refs = append(refs, r.ref)
			ids = append(ids, r.id)
		}
		rows, err := db.UsageArchive.Query().Where(usagearchive.Or(usagearchive.RestoreRefIn(refs...), usagearchive.And(usagearchive.RestoreRefIsNil(), usagearchive.SourceIDIn(ids...)))).Select(usagearchive.FieldRestoreRef, usagearchive.FieldSourceID, usagearchive.FieldRequestID, usagearchive.FieldProjectID, usagearchive.FieldKind, usagearchive.FieldOccurredAt, usagearchive.FieldModelID, usagearchive.FieldRequestModelID, usagearchive.FieldChannelID, usagearchive.FieldAPIKeyID).All(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
		known := map[string]*ent.UsageArchive{}
		type nativeID struct {
			kind usagearchive.Kind
			id   int
		}
		native := map[nativeID]*ent.UsageArchive{}
		for _, r := range rows {
			if r.RestoreRef != nil {
				known[*r.RestoreRef] = r
			} else {
				native[nativeID{r.Kind, r.SourceID}] = r
			}
		}
		for _, r := range records {
			row := known[r.ref]
			if row == nil {
				// Archives written before portable identities existed can be matched in-place
				// by original IDs, but only when their immutable request facts also agree.
				row = native[nativeID{r.kind, r.id}]
				if row == nil || row.RequestID != r.parent || !row.OccurredAt.Equal(r.at) || row.ChannelID != r.channel || (r.key != 0 && row.APIKeyID != r.key) {
					continue
				}
				model := row.ModelID
				if r.kind == usagearchive.KindRequest {
					model = row.RequestModelID
				}
				if model != r.model {
					continue
				}
			}
			if row.Kind != r.kind || row.ProjectID != r.project {
				continue
			}
			parents[r.parent] = row.RequestID
			if r.kind == usagearchive.KindRequest {
				seenRequests[r.id] = true
			} else {
				seenBills[r.id] = true
			}
		}
	}
	remainingRequests := lo.Filter(requests, func(r *BackupUsageRequest, _ int) bool { return r != nil && !seenRequests[r.ID] })
	remainingLogs := lo.Filter(logs, func(r *BackupUsageLog, _ int) bool { return r != nil && !seenBills[r.ID] })
	return remainingRequests, remainingLogs, parents, nil
}
