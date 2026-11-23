package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

type responsesContinuation struct {
	store                *responsesSessionStore
	original             []byte
	effective            []byte
	snapshotBody         []byte
	holdHTTPHistory      bool
	pendingHTTPHistory   *responsesHTTPHistory
	previous             string
	input                []json.RawMessage
	parent               *shared.ResponsesSnapshot
	complete             bool
	retain               bool
	retentionBytes       int
	replaceInputAtCommit bool
	lane                 *shared.ResponsesLane
	firstOrigin          *shared.ResponsesOrigin
	owner                *shared.ResponsesRecord
	capture              *responsesCompletion
}

func newResponsesContinuation(store *responsesSessionStore, body []byte) *responsesContinuation {
	return responsesContinuationWithID(store, body, responsePreviousID(body))
}
func responsesContinuationWithID(store *responsesSessionStore, body []byte, previous string) *responsesContinuation {
	return &responsesContinuation{store: store, original: body, previous: previous, capture: &responsesCompletion{}}
}

type responsesAttemptTarget struct {
	origin     shared.ResponsesOrigin
	forwardsID bool
}

func prepareResponsesAttemptBody(ctx context.Context, p *PersistentOutboundTransformer, target responsesAttemptTarget) ([]byte, error) {
	return prepareResponsesAttemptBodyWithRequest(ctx, p, nil, target)
}

func prepareResponsesAttemptBodyWithRequest(ctx context.Context, p *PersistentOutboundTransformer, request *llm.Request, target responsesAttemptTarget) ([]byte, error) {
	c := p.state.ResponsesContinuation
	c.releaseCapture()
	c.capture = &responsesCompletion{}
	c.releaseRetention()
	c.snapshotBody = nil
	c.parent = nil
	c.replaceInputAtCommit = false
	c.lane = shared.GetResponsesLane(ctx)
	body := c.original
	inputBytes := len(gjson.GetBytes(body, "input").Raw)
	c.complete = c.previous == ""
	if c.previous != "" {
		if _, ok := shared.GetSessionScope(ctx); !ok {
			return nil, shared.ResponsesRecoveryError()
		}
		if lane := shared.GetResponsesLane(ctx); lane != nil {
			record := lane.Record()
			if record.ID == c.previous {
				c.owner = &record
			}
		} else if c.store != nil && c.owner == nil {
			c.owner = loadResponsesContinuationOwner(ctx, c)
		}
		nativeContinuation := c.owner != nil && target.forwardsID &&
			target.origin.Format == string(llm.APIFormatOpenAIResponseWebSocket) &&
			originMatches(c.owner.Origin, target.origin) && (c.lane != nil && c.lane.CanContinue(*c.owner) ||
			c.lane == nil && c.owner.Origin.Generation != 0)
		if !nativeContinuation && c.lane == nil && c.owner != nil && c.owner.Snapshot == nil && c.owner.Origin.Generation != 0 {
			if err := c.loadHTTPSnapshot(ctx); err != nil {
				return nil, err
			}
		}
		if nativeContinuation {
			c.parent = c.owner.Snapshot
		} else if c.owner != nil && c.owner.Snapshot != nil {
			var err error
			c.parent = c.owner.Snapshot
			body, err = expandResponsesSnapshot(body, c.owner.Snapshot)
			if err != nil {
				return nil, err
			}
			inputBytes = len(gjson.GetBytes(body, "input").Raw)
			c.complete = true
		} else {
			switch {
			case c.owner != nil:
				if !originMatches(c.owner.Origin, target.origin) || !target.forwardsID {
					return nil, shared.ResponsesRecoveryError()
				}
			case shared.IsResponsesWebSocket(ctx), target.origin.Format != string(llm.APIFormatOpenAIResponse), !target.forwardsID:
				return nil, shared.ResponsesRecoveryError()
			default:
				if c.store != nil && c.store.known != nil {
					known, err := c.store.known(ctx, c.previous)
					if err != nil || known {
						return nil, shared.ResponsesRecoveryError()
					}
				}
				if c.firstOrigin != nil && !originMatches(*c.firstOrigin, target.origin) {
					return nil, shared.ResponsesRecoveryError()
				}
			}
		}
	}
	c.effective = body
	native := shared.IsResponsesWebSocket(ctx) && target.origin.Format == string(llm.APIFormatOpenAIResponseWebSocket)
	if c.complete && !native {
		c.snapshotBody = body
	}
	c.input = nil
	// Only Responses carries lossless input/output items for this recovery
	// contract. Converted providers must not certify missing native input.
	c.retain = responses.IsResponsesFormat(llm.APIFormat(target.origin.Format)) &&
		(c.complete || c.parent != nil) && inputBytes <= 64<<20
	if c.retain {
		if lane := shared.GetResponsesLane(ctx); lane != nil {
			c.lane = lane
			c.retentionBytes = inputBytes
			if c.complete && c.parent != nil {
				// The lane already owns the old prefix. Recheck it after all rewrites.
				c.retentionBytes = len(gjson.GetBytes(c.original, "input").Raw)
			}
			if !lane.Budget().Replace(0, c.retentionBytes) {
				c.retentionBytes, c.retain = 0, false
			}
		}
	}
	if lane := shared.GetResponsesLane(ctx); lane != nil {
		c.capture.metadataOnly = !c.retain
		if !c.capture.metadataOnly {
			c.capture.budget = lane.Budget()
		}
	}
	return body, nil
}

func loadResponsesContinuationOwner(ctx context.Context, c *responsesContinuation) *shared.ResponsesRecord {
	if lane := shared.GetResponsesLane(ctx); lane != nil {
		record := lane.Record()
		if record.ID == c.previous {
			return &record
		}
		return nil
	}
	if c.store == nil {
		return nil
	}
	// For HTTP ingress the connection pool will check the scoped key, ID and
	// generation before writing. Load archived bodies only if replay is needed.
	if record := c.store.lookup(ctx, c.previous); record != nil && record.origin.Generation != 0 && record.origin.Format == string(llm.APIFormatOpenAIResponseWebSocket) && !record.complete {
		return &shared.ResponsesRecord{ID: c.previous, Origin: record.origin}
	}
	record, err := c.store.lookupOrLoad(ctx, c.previous)
	if err != nil {
		return nil
	}
	if record == nil {
		return nil
	}
	owner := &shared.ResponsesRecord{ID: c.previous, Origin: record.origin}
	if record.complete {
		owner.Snapshot = httpRecoverySnapshot(record)
	}
	return owner
}

func selectResponsesContinuationCandidate(ctx context.Context, p *PersistentOutboundTransformer, request *llm.Request) error {
	c := p.state.ResponsesContinuation
	if c == nil || c.previous == "" {
		return nil
	}
	if c.owner == nil {
		c.owner = loadResponsesContinuationOwner(ctx, c)
	}
	if c.owner == nil {
		return nil
	}
	if c.owner.Snapshot != nil {
		lane := shared.GetResponsesLane(ctx)
		if lane == nil || !lane.CanContinue(*c.owner) {
			return nil
		}
	}

	for index := p.state.CurrentCandidateIndex; index < len(p.state.ChannelModelsCandidates); index++ {
		candidate := p.state.ChannelModelsCandidates[index]
		if candidate == nil || candidate.Channel == nil || len(candidate.Models) == 0 {
			continue
		}
		if c.owner.Snapshot != nil && candidate.tierPreference > p.state.ChannelModelsCandidates[p.state.CurrentCandidateIndex].tierPreference {
			continue
		}
		for modelIndex := range candidate.Models {
			p.refreshCandidateAPIFormat(ctx, candidate, modelIndex, request)
			p.preferVerifiedHTTPWebSocket(ctx, candidate, candidate.Models[modelIndex], request)
			origin := shared.ResponsesOrigin{
				ChannelID: candidate.Channel.ID,
				Format:    candidate.APIFormat,
				Model:     candidate.Models[modelIndex].ActualModel,
			}
			if !originMatches(c.owner.Origin, origin) {
				continue
			}
			p.state.CurrentCandidateIndex = index
			p.state.CurrentModelIndex = modelIndex
			p.state.CurrentCandidate = candidate
			p.wrapped = selectOutboundForCandidate(candidate)
			return nil
		}
	}
	return nil
}

func (c *responsesContinuation) releaseRetention() {
	if c.lane != nil && c.retentionBytes > 0 {
		c.lane.Budget().Replace(c.retentionBytes, 0)
	}
	c.retentionBytes = 0
}

func originMatches(owner, target shared.ResponsesOrigin) bool {
	return owner.ChannelID == target.ChannelID && owner.Format == target.Format && owner.Model == target.Model &&
		(target.Endpoint == "" || owner.Endpoint == target.Endpoint) && (target.Auth == "" || owner.Auth == target.Auth)
}

func forwardsResponsesID(outbound transformer.Outbound) bool {
	if finalizer, ok := outbound.(transformer.TransportRequestFinalizer); ok {
		probe := finalizer.FinalizeTransportRequest(&httpclient.Request{Body: []byte(`{"previous_response_id":"probe"}`)})
		return probe != nil && gjson.GetBytes(probe.Body, "previous_response_id").String() == "probe"
	}
	return outbound.APIFormat() == llm.APIFormatOpenAIResponseWebSocket
}

func prepareResponsesAttempt(ctx context.Context, p *PersistentOutboundTransformer, request *llm.Request, credentialUnavailable bool) (*llm.Request, error) {
	if p.state.ResponsesContinuation == nil {
		return request, nil
	}
	if err := selectResponsesContinuationCandidate(ctx, p, request); err != nil {
		return nil, err
	}
	origin := shared.ResponsesOrigin{ChannelID: p.GetCurrentChannel().ID, Format: string(p.wrapped.APIFormat()), Model: request.Model}
	if p.state.CurrentCandidate.APIFormat != "" {
		origin.Format = p.state.CurrentCandidate.APIFormat
	}
	c := p.state.ResponsesContinuation
	lane := shared.GetResponsesLane(ctx)
	candidate := p.state.CurrentCandidate
	forwardsID := forwardsResponsesID(p.wrapped)
	credentialChanged := false
	if lane != nil && c.owner != nil && c.owner.Snapshot != nil && candidate.continuationStrict {
		// A live socket may still carry a credential removed from the channel.
		// Restore before transformation/hooks, rather than sending its old ID or
		// failing later after the complete lane history was already recovered.
		credentialChanged = credentialUnavailable
		forwardsID = forwardsID && !credentialChanged
	}
	body, err := prepareResponsesAttemptBodyWithRequest(ctx, p, request, responsesAttemptTarget{origin: origin, forwardsID: forwardsID})
	if err != nil {
		return nil, err
	}
	if lane != nil && c.complete && c.owner != nil && c.owner.Snapshot != nil {
		// Candidate selection already enforced model/project/key permissions.
		// Only the verified, fully expanded request can release the old key binding.
		candidate.continuationStrict = false
		if credentialChanged {
			lane.RetireLeases()
		}
	}
	if attempt := shared.GetResponsesAttempt(ctx); attempt != nil {
		attempt.SetOrigin(origin)
	}
	if !p.state.ResponsesContinuation.complete || p.state.ResponsesContinuation.previous == "" {
		return request, nil
	}
	parsed, err := responses.NewInboundTransformer().TransformRequest(ctx, &httpclient.Request{Body: body})
	if err != nil {
		return nil, err
	}
	if c.owner == nil || c.owner.Snapshot == nil {
		return nil, shared.ResponsesRecoveryError()
	}
	historyItems := c.owner.Snapshot.Items()
	var historyMessages []llm.Message
	if len(historyItems) > 0 {
		historyBody, err := json.Marshal(struct {
			Model string            `json:"model"`
			Input []json.RawMessage `json:"input"`
		}{request.Model, historyItems})
		if err != nil {
			return nil, err
		}
		historyRequest, err := responses.NewInboundTransformer().TransformRequest(ctx, &httpclient.Request{Body: historyBody})
		if err != nil {
			return nil, err
		}
		historyMessages = historyRequest.Messages
	}
	cloned := *request
	cloned.Messages = append(append([]llm.Message(nil), historyMessages...), request.Messages...)
	cloned.ProviderExtensions = parsed.ProviderExtensions
	applyRecoveredReasoning(&cloned, parsed)
	return &cloned, nil
}

// Model suffixes are resolved before route recovery and take precedence over
// the reasoning configuration in the restored input history.
func applyRecoveredReasoning(req, restored *llm.Request) {
	if req.TransformerMetadata[autoEffortApplied] != true && restored.ReasoningEffort != "" {
		req.ReasoningEffort, req.ReasoningBudget = restored.ReasoningEffort, restored.ReasoningBudget
	}
}

func effectiveResponsesRequestBody(state *PersistenceState) []byte {
	if state.ResponsesContinuation != nil && state.ResponsesContinuation.effective != nil {
		return state.ResponsesContinuation.effective
	}
	if state.LlmRequest.RawRequest != nil && len(state.LlmRequest.RawRequest.Body) > 0 {
		return state.LlmRequest.RawRequest.Body
	}
	if state.RawRequest != nil && len(state.RawRequest.JSONBody) > 0 {
		return state.RawRequest.JSONBody
	}
	if state.RawRequest != nil {
		return state.RawRequest.Body
	}
	return nil
}

func verifyResponsesAttempt(p *PersistentOutboundTransformer) pipeline.Middleware {
	return pipeline.OnRawRequest("verify_responses_continuation", func(ctx context.Context, request *httpclient.Request) (*httpclient.Request, error) {
		c := p.state.ResponsesContinuation
		if c == nil {
			return request, nil
		}
		origin := shared.ResponsesOrigin{ChannelID: p.GetCurrentChannel().ID, Endpoint: request.URL, Format: request.APIFormat, Model: p.GetCurrentModelID(), Auth: responses.ContinuationAuthIdentity(request)}
		if model := gjson.GetBytes(request.Body, "model").String(); model != "" {
			origin.Model = model
		}
		if c.complete && c.previous != "" && c.owner != nil && origin.ChannelID != c.owner.Origin.ChannelID &&
			gjson.GetBytes(request.Body, "previous_response_id").String() == c.previous {
			// The full input already contains this history. A later template must
			// not put the old provider's response ID back on a different channel.
			body, err := sjson.DeleteBytes(request.Body, "previous_response_id")
			if err != nil {
				return nil, err
			}
			request.Body = body
		}
		if c.previous != "" && !c.complete {
			if gjson.GetBytes(request.Body, "previous_response_id").String() != c.previous || !forwardsResponsesID(p.wrapped) {
				return nil, shared.ResponsesRecoveryError()
			}
			changedOrigin := c.owner != nil && !originMatches(c.owner.Origin, origin)
			lostSocket := c.owner != nil && c.lane != nil && origin.Format == string(llm.APIFormatOpenAIResponseWebSocket) && !c.lane.CanContinue(*c.owner)
			if changedOrigin || lostSocket {
				if c.lane == nil && c.owner != nil && c.owner.Snapshot == nil && c.owner.Origin.Generation != 0 {
					if err := c.loadHTTPSnapshot(ctx); err != nil {
						return nil, err
					}
				}
				if c.owner == nil || c.owner.Snapshot == nil {
					return nil, shared.ResponsesRecoveryError()
				}
				body, err := expandResponsesSnapshot(request.Body, c.owner.Snapshot)
				if err != nil {
					return nil, err
				}
				request.Body = body
				c.complete, c.parent = true, c.owner.Snapshot
				c.effective = body
				if c.lane != nil {
					// Late recovery (after plugin/header overrides) must release
					// the same old-ID binding as recovery during preparation.
					// The execution gate still protects requests that retain an ID.
					p.state.CurrentCandidate.continuationStrict = false
				}
			} else if c.firstOrigin != nil && !originMatches(*c.firstOrigin, origin) {
				return nil, shared.ResponsesRecoveryError()
			}
		}
		if c.firstOrigin == nil {
			c.firstOrigin = &origin
		}
		if responses.IsResponsesFormat(llm.APIFormat(request.APIFormat)) {
			c.captureAttemptBody(request.Body)
		}
		if attempt := shared.GetResponsesAttempt(ctx); attempt != nil {
			if c.owner != nil && originMatches(c.owner.Origin, origin) && gjson.GetBytes(request.Body, "previous_response_id").Exists() {
				origin.Generation = c.owner.Origin.Generation
			}
			attempt.SetOrigin(origin)
		}
		return request, nil
	})
}

func (c *responsesContinuation) captureAttemptBody(body []byte) {
	if c.complete && !gjson.GetBytes(body, "previous_response_id").Exists() {
		c.snapshotBody = body
	}
	// Validate the input that survived provider and channel rewrites.
	c.effective = body
	if c.retain && c.lane != nil {
		c.replaceInputAtCommit = false
		input := gjson.GetBytes(body, "input")
		retainedBytes := len(input.Raw)
		var items []json.RawMessage
		if c.complete && c.owner != nil && c.owner.Snapshot != nil {
			c.parent = nil
			if suffix, ok := retainedResponsesSuffix(input, c.owner.Snapshot); ok {
				c.parent, items = c.owner.Snapshot, suffix
				retainedBytes = rawResponseSessionValuesSize(suffix)
			}
		}
		if !c.lane.Budget().Replace(c.retentionBytes, retainedBytes) {
			c.releaseRetention()
			c.input = nil
			// A rewritten full input still exists in the outgoing request. Do
			// not allocate a second retained copy before the turn succeeds.
			c.replaceInputAtCommit = c.complete && c.owner != nil && c.owner.Snapshot != nil && c.parent == nil && retainedBytes <= 64<<20
			c.retain = c.replaceInputAtCommit
		} else {
			c.retentionBytes = retainedBytes
			if c.complete && c.parent != nil {
				c.input = items
			} else {
				c.input = responseSessionInputItems([]byte(input.Raw))
			}
		}
	}
}

// restoreNativeContinuation is invoked only after checkout proved no frame was
// written. It reuses the plugin-adjusted turn and its original execution.
func (p *PersistentOutboundTransformer) restoreNativeContinuation(ctx context.Context, raw *httpclient.Request) error {
	s := p.state
	c := s.ResponsesContinuation
	record := *c.owner
	if c.lane != nil {
		record = c.lane.Record()
		if record.Snapshot != c.owner.Snapshot {
			return shared.ResponsesRecoveryError()
		}
	} else if record.Snapshot == nil {
		if err := c.loadHTTPSnapshot(ctx); err != nil {
			return err
		}
		record = *c.owner
	}
	if record.ID != c.previous || record.Origin != c.owner.Origin || record.Snapshot == nil ||
		gjson.GetBytes(raw.Body, "previous_response_id").String() != record.ID {
		return shared.ResponsesRecoveryError()
	}
	body, err := expandResponsesSnapshot(raw.Body, record.Snapshot)
	if err != nil {
		return err
	}
	// The snapshot can include history captured before the channel's Page adapter.
	if ch := p.GetCurrentChannel(); ch.Settings != nil && ch.Settings.TransformOptions.CodexPageContextCompatibility {
		body, err = normalizeCodexPageContext(body)
		if err != nil {
			return err
		}
	}
	restored := *raw
	restored.Body, restored.JSONBody = body, body
	if !s.budgetUnreserved && s.APIKey != nil {
		service := s.UsageLogService.BudgetService
		estimate, quoteErr := service.Estimate(ctx, s.RequestExec, s.LlmRequest, &restored, estimatePromptTokens(s.LlmRequest))
		if err := service.ResizeUnwrittenAttempt(ctx, s.RequestExec.ID, estimate, quoteErr); err != nil {
			denied, ok := errors.AsType[*biz.BudgetDenied](err)
			if !ok {
				denied = &biz.BudgetDenied{Reason: "accounting_unavailable"}
			}
			if !denied.CandidateOnly {
				s.BudgetStop = denied
			}
			return denied
		}
	}
	if err := s.RequestService.UpdateRecoveredExecutionBody(ctx, s.RequestExec, body); err != nil {
		return fmt.Errorf("record recovered Responses request: %w", err)
	}
	raw.Body, raw.JSONBody = body, body
	c.complete, c.parent = true, record.Snapshot
	c.captureAttemptBody(body)
	s.CurrentCandidate.continuationStrict = false
	s.ExecutionTransport.mu.Lock()
	s.ExecutionTransport.bytes = len(body)
	s.ExecutionTransport.mu.Unlock()
	return nil
}

func (c *responsesContinuation) loadHTTPSnapshot(ctx context.Context) error {
	if c.store == nil {
		return shared.ResponsesRecoveryError()
	}
	loaded, err := c.store.lookupOrLoad(ctx, c.previous)
	if err != nil {
		return err
	}
	if loaded == nil || !loaded.complete || loaded.origin != c.owner.Origin {
		return shared.ResponsesRecoveryError()
	}
	c.owner.Snapshot = httpRecoverySnapshot(loaded)
	return nil
}

func httpRecoverySnapshot(record *responsesSessionRecord) *shared.ResponsesSnapshot {
	input, _ := normalizeResponseSessionItems(record.input)
	output, _ := normalizeResponseSessionItems(record.output)
	return shared.NewResponsesSnapshot(nil, input, output)
}
