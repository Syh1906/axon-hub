package gql

import (
	"context"
	"fmt"
	"slices"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/llm"
)

type protocolLastExecution struct {
	ChannelID int    `json:"channel_id"`
	ModelID   string `json:"model_id"`
	Format    string `json:"format"`
	ID        int    `json:"latest_id"`
}

type ProtocolRoutePage struct {
	TotalCount int
	Items      []*orchestrator.ProtocolRouteSummary
}
type ProtocolRouteEvidence struct {
	ChannelID   int    `json:"channel_id"`
	ActualModel string `json:"model_id"`
	APIFormat   string `json:"format"`
	// Start time of the successful execution, not its mutable UpdatedAt.
	LastSuccess time.Time `json:"last_success"`
}
type ProtocolRouteDetail struct {
	Routes           []*orchestrator.ProtocolRoute
	Evidence         []*ProtocolRouteEvidence
	EvidenceReadable bool
}

func authorizeProtocolRoutes(ctx context.Context, protocol string) error {
	if err := authz.RequireScope(ctx, scopes.ScopeReadChannels); err != nil {
		return err
	}
	if !hasProtocolSystemScope(ctx, scopes.ScopeReadChannels) {
		return fmt.Errorf("protocol routes require system-level read_channels permission")
	}
	if !objects.IsClientChatProtocol(objects.ChatProtocol(llm.APIFormat(protocol))) {
		return fmt.Errorf("unsupported client protocol %q", protocol)
	}
	return nil
}

func hasProtocolSystemScope(ctx context.Context, scope scopes.ScopeSlug) bool {
	p, _ := authz.GetPrincipal(ctx)
	if p.Type == authz.PrincipalTypeSystem || p.Type == authz.PrincipalTypeTest {
		return true
	}
	user, _ := contexts.GetUser(ctx)
	return scopes.EffectiveSystemScope(ctx, user, scope)
}

func (r *queryResolver) protocolChannels(ctx context.Context) ([]*biz.Channel, error) {
	// Exclude credential and request-body columns from this configuration view.
	entities, err := r.client.Channel.Query().Where(channel.StatusNEQ(channel.StatusArchived)).Select(
		channel.FieldID, channel.FieldName, channel.FieldType, channel.FieldStatus, channel.FieldBaseURL,
		channel.FieldEndpoints, channel.FieldSettings, channel.FieldSupportedModels, channel.FieldTags, channel.FieldUpdatedAt,
	).All(ctx)
	if err != nil {
		return nil, err
	}
	return lo.Map(entities, func(ch *ent.Channel, _ int) *biz.Channel { return &biz.Channel{Channel: ch} }), nil
}

func (r *queryResolver) readProtocolRoutes(ctx context.Context, protocol, search string, offset, limit int) (*ProtocolRoutePage, error) {
	if err := authorizeProtocolRoutes(ctx, protocol); err != nil {
		return nil, err
	}
	if offset < 0 || limit < 1 || limit > 100 {
		return nil, fmt.Errorf("offset must be non-negative and limit must be between 1 and 100")
	}
	return authz.RunAsHost(ctx, r.client, "protocol-route-inventory", func(readCtx context.Context) (*ProtocolRoutePage, error) {
		// Enumerate only identity columns, then load settings only for this page.
		identities, err := r.client.Model.Query().Where(model.StatusEQ(model.StatusEnabled)).Select(
			model.FieldID, model.FieldModelID, model.FieldName, model.FieldType, model.FieldMatchType, model.FieldMatchPriority, model.FieldPublicModelIds,
		).All(readCtx)
		if err != nil {
			return nil, err
		}
		public := biz.ProtocolModelIdentities(identities, search)
		page := &ProtocolRoutePage{TotalCount: len(public), Items: []*orchestrator.ProtocolRouteSummary{}}
		if offset >= len(public) {
			return page, nil
		}
		public = public[offset:min(offset+limit, len(public))]
		ids := lo.Uniq(lo.Map(public, func(m biz.ProtocolModelIdentity, _ int) int { return m.EntityID }))
		models, err := r.client.Model.Query().Where(model.IDIn(ids...)).Select(model.FieldID, model.FieldModelID, model.FieldDeveloper, model.FieldSettings, model.FieldMatchType).All(readCtx)
		if err != nil {
			return nil, err
		}
		byID := lo.SliceToMap(models, func(m *ent.Model) (int, *ent.Model) { return m.ID, m })
		channels, err := r.protocolChannels(readCtx)
		if err != nil {
			return nil, err
		}
		settings, err := r.systemService.ModelSettings(readCtx)
		if err != nil {
			return nil, err
		}
		for _, identity := range public {
			m := byID[identity.EntityID]
			if m == nil {
				continue
			}
			routes := orchestrator.InspectProtocolRoutes(readCtx, settings, m, identity.ModelID, llm.APIFormat(protocol), channels)
			page.Items = append(page.Items, orchestrator.SummarizeProtocolRoutes(identity.ModelID, identity.Name, routes))
		}
		return page, nil
	})
}

func (r *queryResolver) readProtocolRouteDetail(ctx context.Context, protocol, modelID string) (*ProtocolRouteDetail, error) {
	if err := authorizeProtocolRoutes(ctx, protocol); err != nil {
		return nil, err
	}
	canReadEvidence := hasProtocolSystemScope(ctx, scopes.ScopeReadRequests)
	return authz.RunAsHost(ctx, r.client, "protocol-route-detail", func(readCtx context.Context) (*ProtocolRouteDetail, error) {
		m, err := r.modelService.ResolveModel(readCtx, modelID)
		if err != nil {
			return nil, err
		}
		if m.Type != model.TypeChat || !slices.Contains(biz.PublicModelIDs(m), modelID) {
			return nil, fmt.Errorf("model %q is not a public chat model", modelID)
		}
		channels, err := r.protocolChannels(readCtx)
		if err != nil {
			return nil, err
		}
		settings, err := r.systemService.ModelSettings(readCtx)
		if err != nil {
			return nil, err
		}
		detail := &ProtocolRouteDetail{Routes: orchestrator.InspectProtocolRoutes(readCtx, settings, m, modelID, llm.APIFormat(protocol), channels), Evidence: []*ProtocolRouteEvidence{}, EvidenceReadable: canReadEvidence}
		if !canReadEvidence {
			return detail, nil
		}
		evidenceRoutes := lo.Filter(detail.Routes, func(route *orchestrator.ProtocolRoute, _ int) bool { return route.State != "excluded" })
		if len(evidenceRoutes) == 0 {
			return detail, nil
		}
		ids := lo.Uniq(lo.Map(evidenceRoutes, func(route *orchestrator.ProtocolRoute, _ int) int { return route.ChannelID }))
		actualModels := lo.Uniq(lo.Map(evidenceRoutes, func(route *orchestrator.ProtocolRoute, _ int) string { return route.ActualModel }))
		upstreamFormats := lo.Uniq(lo.Map(evidenceRoutes, func(route *orchestrator.ProtocolRoute, _ int) string { return route.APIFormat }))
		// Bounded historical metadata, fetched only on expansion. A success is
		// evidence for this ingress/model/upstream tuple, never a live health claim.
		var latest []protocolLastExecution
		err = r.client.RequestExecution.Query().Where(
			requestexecution.ChannelIDIn(ids...), requestexecution.StatusEQ(requestexecution.StatusCompleted),
			requestexecution.ModelIDIn(actualModels...), requestexecution.FormatIn(upstreamFormats...),
			requestexecution.CreatedAtGTE(time.Now().Add(-7*24*time.Hour)),
			requestexecution.HasRequestWith(request.ModelIDEQ(modelID), request.FormatEQ(protocol)),
		).GroupBy(requestexecution.FieldChannelID, requestexecution.FieldModelID, requestexecution.FieldFormat).
			Aggregate(func(s *sql.Selector) string { return sql.As(sql.Max(s.C(requestexecution.FieldID)), "latest_id") }).Scan(readCtx, &latest)
		if err != nil {
			log.Warn(readCtx, "failed to read protocol route evidence", log.Cause(err))
			detail.EvidenceReadable = false
			return detail, nil
		}
		if len(latest) > 0 {
			executions, err := r.client.RequestExecution.Query().Where(requestexecution.IDIn(lo.Map(latest, func(row protocolLastExecution, _ int) int { return row.ID })...)).Select(requestexecution.FieldID, requestexecution.FieldChannelID, requestexecution.FieldModelID, requestexecution.FieldFormat, requestexecution.FieldCreatedAt).All(readCtx)
			if err != nil {
				log.Warn(readCtx, "failed to read protocol route evidence", log.Cause(err))
				detail.EvidenceReadable = false
				return detail, nil
			}
			detail.Evidence = lo.Map(executions, func(e *ent.RequestExecution, _ int) *ProtocolRouteEvidence {
				return &ProtocolRouteEvidence{ChannelID: e.ChannelID, ActualModel: e.ModelID, APIFormat: e.Format, LastSuccess: e.CreatedAt}
			})
		}
		// Configuration edits invalidate older evidence without changing history.
		updated := lo.SliceToMap(channels, func(ch *biz.Channel) (int, time.Time) { return ch.ID, ch.UpdatedAt })
		detail.Evidence = lo.Filter(detail.Evidence, func(e *ProtocolRouteEvidence, _ int) bool {
			return !e.LastSuccess.Before(updated[e.ChannelID]) && !e.LastSuccess.Before(m.UpdatedAt)
		})
		return detail, nil
	})
}
