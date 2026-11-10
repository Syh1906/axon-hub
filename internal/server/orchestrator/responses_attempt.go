package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

type responsesContinuation struct {
	store          *responsesSessionStore
	original       []byte
	effective      []byte
	snapshotBody   []byte
	previous       string
	input          []json.RawMessage
	parent         *shared.ResponsesSnapshot
	complete       bool
	retain         bool
	retentionBytes int
	lane           *shared.ResponsesLane
	firstOrigin    *shared.ResponsesOrigin
	owner          *shared.ResponsesRecord
	capture        *responsesCompletion
}

func newResponsesContinuation(store *responsesSessionStore, body []byte) *responsesContinuation {
	return &responsesContinuation{store: store, original: body, previous: gjson.GetBytes(body, "previous_response_id").String(), capture: &responsesCompletion{}}
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
	c.lane = shared.GetResponsesLane(ctx)
	body := c.original
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse Responses continuation: %w", err)
	}
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
		} else if c.store != nil {
			c.owner = loadResponsesContinuationOwner(ctx, c)
		}
		nativeContinuation := c.owner != nil && target.forwardsID &&
			target.origin.Format == string(llm.APIFormatOpenAIResponseWebSocket) &&
			originMatches(c.owner.Origin, target.origin) && c.lane != nil && c.lane.CanContinue(*c.owner)
		if nativeContinuation {
			c.parent = c.owner.Snapshot
		} else if c.owner != nil && c.owner.Snapshot != nil {
			var err error
			body, err = expandResponsesSnapshot(body, c.owner.Snapshot)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				return nil, err
			}
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
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		c.snapshotBody = encoded
	}
	c.input = nil
	// Only Responses carries lossless input/output items for this recovery
	// contract. Converted providers must not certify missing native input.
	c.retain = responses.IsResponsesFormat(llm.APIFormat(target.origin.Format)) &&
		(c.complete || c.parent != nil) && len(payload["input"]) <= 64<<20
	if c.retain {
		if lane := shared.GetResponsesLane(ctx); lane != nil {
			c.lane = lane
			c.retentionBytes = len(payload["input"])
			if !lane.Budget().Replace(0, c.retentionBytes) {
				c.retentionBytes, c.retain = 0, false
			}
		}
	}
	// Lane snapshots capture the final input after plugins/overrides. Avoid
	// decoding and copying the same (potentially large) input a second time.
	if c.retain && c.lane == nil {
		c.input = responseSessionInputItems(payload["input"])
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
	record, err := c.store.lookupOrLoad(ctx, c.previous)
	if err != nil {
		return nil
	}
	if record == nil {
		return nil
	}
	owner := &shared.ResponsesRecord{ID: c.previous, Origin: record.origin}
	if record.complete {
		owner.Snapshot = shared.NewResponsesSnapshot(nil, record.input, record.output)
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
		for modelIndex := range candidate.Models {
			p.refreshCandidateAPIFormat(ctx, candidate, modelIndex, request)
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
	var expandedPayload map[string]json.RawMessage
	if err := json.Unmarshal(body, &expandedPayload); err != nil {
		return nil, err
	}
	var originalPayload map[string]json.RawMessage
	if err := json.Unmarshal(p.state.ResponsesContinuation.original, &originalPayload); err != nil {
		return nil, err
	}
	expandedItems := responseSessionInputItems(expandedPayload["input"])
	originalItems := responseSessionInputItems(originalPayload["input"])
	if len(expandedItems) < len(originalItems) {
		return nil, shared.ResponsesRecoveryError()
	}
	historyItems := expandedItems[:len(expandedItems)-len(originalItems)]
	var historyMessages []llm.Message
	if len(historyItems) > 0 {
		historyPayload := maps.Clone(expandedPayload)
		historyInput, err := json.Marshal(historyItems)
		if err != nil {
			return nil, err
		}
		historyPayload["input"] = historyInput
		delete(historyPayload, "instructions")
		historyBody, err := json.Marshal(historyPayload)
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
	return &cloned, nil
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
		if c.previous != "" && !c.complete {
			if gjson.GetBytes(request.Body, "previous_response_id").String() != c.previous || !forwardsResponsesID(p.wrapped) {
				return nil, shared.ResponsesRecoveryError()
			}
			changedOrigin := c.owner != nil && !originMatches(c.owner.Origin, origin)
			lostSocket := c.owner != nil && c.lane != nil && origin.Format == string(llm.APIFormatOpenAIResponseWebSocket) && !c.lane.CanContinue(*c.owner)
			if changedOrigin || lostSocket {
				if c.owner == nil || c.owner.Snapshot == nil {
					return nil, shared.ResponsesRecoveryError()
				}
				body, err := expandResponsesSnapshot(request.Body, c.owner.Snapshot)
				if err != nil {
					return nil, err
				}
				request.Body = body
				c.complete, c.parent = true, nil
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
			if c.complete && !gjson.GetBytes(request.Body, "previous_response_id").Exists() {
				c.snapshotBody = request.Body
			}
			// Capture the final, plugin/override-adjusted input once. A live native
			// continuation keeps only this turn; recovery starts a new complete root.
			c.effective = request.Body
			if c.retain {
				input := gjson.GetBytes(request.Body, "input")
				if c.lane != nil && !c.lane.Budget().Replace(c.retentionBytes, len(input.Raw)) {
					c.releaseRetention()
					c.retain, c.input = false, nil
				} else {
					if c.lane != nil {
						c.retentionBytes = len(input.Raw)
					}
					c.input = responseSessionInputItems([]byte(input.Raw))
				}
			}
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
