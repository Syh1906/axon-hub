package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/biz/upstream"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

type firstContentSort struct {
	ctx         context.Context
	state       *PersistenceState
	request     *llm.Request
	session     uint64
	callerRates map[int]string
}

func currentFirstContentSort(ctx context.Context) *firstContentSort {
	if s, _ := ctx.Value(firstContentStateKey{}).(*PersistenceState); s != nil {
		return s.routeSort
	}
	return nil
}

// Admission and trace ownership have already established the project's candidate set.
// A continuation or a session without a proven new ownership decision never enters a trial.
func newSessionForContent(ctx context.Context, s *PersistenceState, req *llm.Request) (uint64, bool) {
	if s == nil || s.routeHistory == nil || s.Request == nil || s.APIKey == nil || req == nil || req.Stream == nil || !*req.Stream ||
		contexts.StreamFormat(ctx) == "" || contexts.GetRequestClock(ctx) == nil || req.PreviousResponseID != nil ||
		s.ResponsesContinuation != nil && s.ResponsesContinuation.previous != "" || s.titleTask || s.hedge != nil ||
		s.PluginHooks.Request != nil || s.PluginHooks.Attempt != nil {
		return 0, false
	}
	if previous, _ := ctx.Value(responsesPreviousIDContextKey{}).(string); previous != "" {
		return 0, false
	}
	if s.RoutingPolicy.LoadBalancerStrategy != biz.LoadBalancerStrategyAdaptive || s.contentBoundHistory || !s.RequestService.PreviousChannelCacheEnabled() {
		return 0, false
	}
	trace, ok := contexts.GetTrace(ctx)
	if !ok || trace == nil || trace.ID == 0 || trace.ID != s.Request.TraceID || trace.ProjectID != s.Request.ProjectID || s.Request.APIKeyID != s.APIKey.ID {
		return 0, false
	}
	if trace.ThreadID != 0 {
		return xxhash.Sum64String("thread/" + strconv.Itoa(trace.ThreadID)), true
	}
	return xxhash.Sum64String(trace.TraceID), true
}

func newFirstContentSort(ctx context.Context, s *PersistenceState, req *llm.Request, group []*ChannelModelsCandidate) *firstContentSort {
	if len(group) < 2 || group[0].SessionState != "new" {
		return nil
	}
	session, ok := newSessionForContent(ctx, s, req)
	if !ok {
		return nil
	}
	rates, err := s.RequestService.CallerRatesForRouting(ctx, s.APIKey)
	if err != nil {
		return nil
	}
	return &firstContentSort{ctx: ctx, state: s, request: req, session: session, callerRates: rates}
}

func (s *PersistenceState) contentRoute(ctx context.Context, c *ChannelModelsCandidate, req *llm.Request, size int, source string) (firstContentRoute, bool) {
	if len(c.Models) == 0 || !c.Channel.SessionReuseEnabled() || c.TraceSticky || c.continuationBound || c.continuationStrict || c.continuationUpstreamRef != "" ||
		c.healthGate != nil || c.SessionState != "new" {
		return firstContentRoute{}, false
	}
	switch llm.APIFormat(c.APIFormat) {
	case llm.APIFormatOpenAIResponse, llm.APIFormatOpenAIChatCompletion, llm.APIFormatAnthropicMessage, llm.APIFormatGeminiContents:
	default:
		return firstContentRoute{}, false
	}
	ch := c.Channel
	for _, ep := range ch.Endpoints {
		if ep.WebSocketsSupported() || ep.APIFormat == string(llm.APIFormatOpenAIResponseWebSocket) {
			return firstContentRoute{}, false
		}
	}
	tier := ""
	if req.ServiceTier != nil {
		tier = *req.ServiceTier
	}
	if responsesTierFormat(llm.APIFormat(c.APIFormat)) || convertedResponsesTier(ch.Settings, req.APIFormat, llm.APIFormat(c.APIFormat)) {
		var ok bool
		tier, ok = outboundServiceTier(ch.Settings, llm.APIFormat(c.APIFormat), tier)
		if !ok {
			return firstContentRoute{}, false
		}
	} else if c.APIFormat != string(llm.APIFormatOpenAIChatCompletion) && tier != "" {
		return firstContentRoute{}, false
	}
	load := 0
	if s.routeLimiter != nil {
		active, waiting, _ := s.routeLimiter.Stats(ch.ID)
		if capacity := extractLimiterConfig(ch).capacity; waiting > 0 || capacity > 0 && active >= capacity {
			return firstContentRoute{}, false
		}
		load = bitsForLoad(active)
	}
	return firstContentRoute{
		work: firstContentWork{
			project: s.Request.ProjectID, apiKey: s.APIKey.ID, priority: c.Priority,
			size: routeSizeBucket(size), load: load, model: c.Models[0].ActualModel,
			effort: req.ReasoningEffort, tier: tier, format: c.APIFormat, ingress: string(contexts.StreamFormat(ctx)), websocket: shared.IsResponsesWebSocket(ctx),
		},
		channel: ch.ID, source: source, config: ch.UpdatedAt.UnixNano(),
	}, true
}

func bitsForLoad(active int) int {
	if active <= 1 {
		return 0
	}
	if active <= 4 {
		return 1
	}
	if active <= 16 {
		return 2
	}
	return 3
}

func (s *firstContentSort) channelKey(c *ChannelModelsCandidate) (firstContentRoute, bool) {
	ch := c.Channel
	keys := ch.GetEnabledAPIKeys()
	if len(keys) != 1 || ch.Credentials.IsOAuth() || len(c.Models) != 1 || c.Models[0].UseActualModelPrice ||
		len(ch.GetBodyOverrideOperations()) != 0 || len(ch.GetHeaderOverrideOperations()) != 0 ||
		ch.Settings != nil && len(ch.Settings.UpstreamAddresses) > 0 {
		return firstContentRoute{}, false
	}
	for _, ep := range ch.Endpoints {
		if ep.BaseURL != "" {
			return firstContentRoute{}, false
		}
	}
	if !ch.SameFirstContentPrice(ch, c.Models[0].ActualModel, c.ModelCard, c.ModelCard) {
		return firstContentRoute{}, false
	}
	return s.state.contentRoute(s.ctx, c, s.request, len(s.state.RawRequest.Body), upstream.KeyRef(keys[0]))
}

func moveFirstContentChoice[T any](items []T, index int) {
	if index > 0 {
		value := items[index]
		copy(items[1:index+1], items[:index])
		items[0] = value
	}
}

func (s *firstContentSort) selectChannel(n int, at func(int) (*ChannelModelsCandidate, bool)) int {
	base, ok := at(0)
	if !ok || s.state.RawRequest == nil {
		return 0
	}
	baseKey, ok := s.channelKey(base)
	if !ok {
		return 0
	}
	keys := []firstContentRoute{baseKey}
	indices := []int{0}
	for i := 1; i < n; i++ {
		c, eligible := at(i)
		if !eligible || c.Channel.OrderingWeight != base.Channel.OrderingWeight || c.Channel.Type != base.Channel.Type || c.DefaultMaxTokens != base.DefaultMaxTokens {
			continue
		}
		key, ok := s.channelKey(c)
		if !ok || s.callerRates[c.Channel.ID] != s.callerRates[base.Channel.ID] || key.work != baseKey.work || !base.Channel.SameFirstContentPrice(c.Channel, key.work.model, base.ModelCard, c.ModelCard) ||
			!sameFirstContentSettings(base.Channel.Settings, c.Channel.Settings) {
			continue
		}
		keys = append(keys, key)
		indices = append(indices, i)
	}
	if len(keys) < 2 {
		return 0
	}
	p, gain := s.state.RequestService.PayloadService.FirstContentTrial()
	d := s.state.routeHistory.choose(keys, s.session, p, gain, time.Now())
	s.state.channelDecision = d
	return indices[d.chosen]
}

func sameFirstContentSettings(a, b *objects.ChannelSettings) bool {
	if a == nil {
		a = &objects.ChannelSettings{}
	}
	if b == nil {
		b = &objects.ChannelSettings{}
	}
	return equalSetting(a.CacheWriteBilling, b.CacheWriteBilling) && equalSetting(a.ServiceTierBilling, b.ServiceTierBilling) &&
		a.ForceOneHourPromptCache == b.ForceOneHourPromptCache && equalSetting(a.PassThroughBody, b.PassThroughBody) &&
		equalSetting(a.AllowWebSearchTool, b.AllowWebSearchTool) && equalSetting(a.AllowImageGenerationTool, b.AllowImageGenerationTool) && reflect.DeepEqual(a.TransformOptions, b.TransformOptions)
}

func equalSetting[T comparable](a, b *T) bool {
	return a == b || a != nil && b != nil && *a == *b
}

func (p *PersistentOutboundTransformer) selectContentAddress(ctx context.Context, raw *httpclient.Request, addresses []objects.ChannelUpstreamAddress) []objects.ChannelUpstreamAddress {
	s, c := p.state, p.state.CurrentCandidate
	if len(addresses) < 2 || addresses[0].Priority != addresses[1].Priority || p.addressIndex != 0 || p.continuationAddresses != nil ||
		len(c.Models) == 0 || !c.Channel.SessionReuseEnabled() || c.continuationBound || c.continuationStrict || c.continuationUpstreamRef != "" ||
		s.RequestExec != nil || s.CurrentCandidateIndex != 0 || s.CurrentModelIndex != 0 {
		return addresses
	}
	percent, gain := s.RequestService.PayloadService.FirstContentTrial()
	if c.TraceSticky && percent == 0 || !c.TraceSticky && c.SessionState != "new" {
		return addresses
	}
	session, ok := newSessionForContent(ctx, s, s.LlmRequest)
	if !ok || c.TraceSticky && session%100 >= uint64(percent) {
		return addresses
	}
	u, err := url.Parse(raw.URL)
	if err != nil || u.User != nil || u.Fragment != "" || len(raw.Query) != 0 || u.RawQuery != "" && !(c.APIFormat == string(llm.APIFormatGeminiContents) && u.RawQuery == "alt=sse") {
		return addresses
	}
	keyRef := ""
	if key, ok := contexts.GetChannelAPIKey(ctx); ok && key != "" {
		keyRef = upstream.KeyRef(key)
	}
	keysForChannel := c.Channel.GetEnabledAPIKeys()
	if keyRef == "" || c.Channel.Credentials.IsOAuth() || len(keysForChannel) != 1 || upstream.KeyRef(keysForChannel[0]) != keyRef {
		return addresses
	}
	var owner string
	if percent > 0 {
		owner = fmt.Sprintf("%d/%d/%d/%d/%d/%s/%s/%s/%s", s.Request.ProjectID, s.APIKey.ID, session,
			c.Channel.ID, c.Channel.UpdatedAt.UnixNano(), c.APIFormat, c.Models[0].ActualModel, keyRef, biz.ResponsesUpstreamRef(raw.URL, raw.Headers))
	}
	if c.TraceSticky && owner != "" {
		position := s.RequestService.TrialAddress(ctx, owner)
		if position > 0 && position <= len(addresses) && addresses[position-1].Priority == addresses[0].Priority {
			p.trackContentAddress(owner, keyRef)
			addresses = slices.Clone(addresses)
			moveFirstContentChoice(addresses, position-1)
			p.continuationAddresses = addresses
		}
		return addresses
	}
	keys := make([]firstContentRoute, 0, len(addresses))
	for _, a := range addresses {
		if a.Priority != addresses[0].Priority {
			break
		}
		base, err := url.Parse(c.Channel.NormalizeUpstreamAddress(a.URL, c.APIFormat))
		if err != nil {
			return addresses
		}
		key, ok := s.contentRoute(ctx, c, s.LlmRequest, len(raw.Body), keyRef+"/"+biz.ResponsesUpstreamRef(base.String(), raw.Headers)+"/"+u.EscapedPath())
		if !ok {
			return addresses
		}
		keys = append(keys, key)
	}
	d := s.routeHistory.choose(keys, session, percent, gain, time.Now())
	d.addresses = make(map[string]firstContentRoute, len(keys))
	for i, key := range keys {
		d.addresses[addresses[i].URL] = key
	}
	s.routeDecision = d
	if d.chosen > 0 {
		p.trackContentAddress(owner, keyRef)
		addresses = slices.Clone(addresses)
		moveFirstContentChoice(addresses, d.chosen)
		p.continuationAddresses = addresses
	}
	return addresses
}

func (p *PersistentOutboundTransformer) trackContentAddress(owner, keyRef string) {
	s := p.state
	s.contentAddressOwner, s.contentAddressKey, s.contentAddressChannel = owner, keyRef, s.CurrentCandidate.Channel
}

// The pipeline has accepted the final upstream stream and exhausted pre-commit
// retries. Publish its origin before a client can read even the first event.
func (s *PersistenceState) rememberAcceptedAddress(ctx context.Context) {
	ch := s.contentAddressChannel
	if ch == nil || s.RequestExec == nil || s.RequestExec.ChannelID != ch.ID || s.RequestExec.Selection == nil ||
		s.RequestExec.Selection.KeyRef != s.contentAddressKey || !s.CurrentCandidate.Channel.UpdatedAt.Equal(ch.UpdatedAt) {
		return
	}
	s.RequestService.RememberTrialAddress(ctx, s.contentAddressOwner, s.contentAddressPosition)
}

// Read provider evidence only from usage-bearing frames after the stream ends.
// Several adapters normalize a reported zero and an absent counter to the same value.
func reportedCacheTokens(format llm.APIFormat, chunks []*httpclient.StreamEvent) (int64, bool) {
	// Anthropic normally reports the input breakdown in message_start.
	// Other adapters send final usage at the end, sometimes just before [DONE].
	for offset := range chunks {
		index := len(chunks) - 1 - offset
		if format == llm.APIFormatAnthropicMessage {
			index = offset
		}
		v := cacheTokenField(format, chunks[index])
		if v.Exists() {
			return parseCacheTokenCount(v)
		}
	}
	return 0, false
}

func parseCacheTokenCount(v gjson.Result) (int64, bool) {
	n, err := strconv.ParseInt(v.Raw, 10, 64)
	return n, v.Type == gjson.Number && err == nil && n >= 0
}

// Keep absence distinct from a present invalid count: the latest reported
// field wins even when it invalidates an earlier observation.
func cacheTokenField(format llm.APIFormat, e *httpclient.StreamEvent) gjson.Result {
	var v gjson.Result
	switch format {
	case llm.APIFormatOpenAIResponse:
		kind := gjson.GetBytes(e.Data, "type").String()
		if kind != "response.completed" && kind != "response.done" {
			return gjson.Result{}
		}
		v = gjson.GetBytes(e.Data, "response.usage.input_tokens_details.cached_tokens")
	case llm.APIFormatOpenAIChatCompletion:
		usage := gjson.GetBytes(e.Data, "usage")
		v = usage.Get("prompt_tokens_details.cached_tokens")
		if !v.Exists() {
			v = usage.Get("cached_tokens")
		}
	case llm.APIFormatAnthropicMessage:
		usage := gjson.GetBytes(e.Data, "usage")
		if !usage.Exists() && gjson.GetBytes(e.Data, "type").String() == "message_start" {
			usage = gjson.GetBytes(e.Data, "message.usage")
		}
		v = usage.Get("cache_read_input_tokens")
		if !v.Exists() {
			v = usage.Get("cached_tokens")
		}
	case llm.APIFormatGeminiContents:
		v = gjson.GetBytes(e.Data, "usageMetadata.cachedContentTokenCount")
	}
	return v
}

func (s *PersistenceState) observeFirstContentChoice(ctx context.Context, exec *ent.RequestExecution) {
	d := s.routeDecision
	if d == nil {
		d = s.channelDecision
	}
	if d == nil || s.CurrentCandidateIndex != 0 || s.CurrentModelIndex != 0 || s.routeObserverSet {
		return
	}
	s.routeObserverSet = true
	clock := contexts.GetRequestClock(ctx)
	key := d.keys[d.chosen]
	effort := ""
	if exec.ReasoningEffort != nil {
		effort = *exec.ReasoningEffort
	}
	wireMatches := exec.ChannelID == key.channel && exec.ModelID == key.work.model && effort == key.work.effort && exec.CostSnapshot != nil
	var costTier *objects.ServiceTierEvidence
	if exec.CostSnapshot != nil {
		costTier = exec.CostSnapshot.ServiceTier
	}
	if key.work.format == string(llm.APIFormatOpenAIChatCompletion) || key.work.format == string(llm.APIFormatOpenAIResponse) {
		wireMatches = wireMatches && costTier != nil && costTier.Outbound == key.work.tier
	}
	if s.routeDecision == nil && (exec.Selection == nil || exec.Selection.KeyRef != key.source) {
		wireMatches = false
	}
	percent, _ := s.RequestService.PayloadService.FirstContentTrial()
	client := ctx
	if lifecycle := streamLifecycleFromContext(ctx); lifecycle != nil {
		client = lifecycle.downstream
	}
	clock.OnFinish(func(t contexts.RequestTiming, failed bool) {
		sample := firstContentSample{at: time.Now(), session: d.session, mismatched: !wireMatches}
		if t.FirstWriteContent != nil {
			sample.delay = t.FirstWriteContent.At.Sub(t.BodyRead)
			if len(t.Attempts) == 1 && t.Attempts[0].Dispatched != nil {
				sample.fromDispatch = t.FirstWriteContent.At.Sub(*t.Attempts[0].Dispatched)
			}
		}
		sample.failed = failed || len(t.Attempts) != 1 || t.Delivered == nil || !s.StreamCompleted || s.StreamTerminalError != nil || sample.delay <= 0
		if !sample.failed {
			final := s.routeCompletion
			sample.tokens, sample.cachePercent, sample.reportedTier = final.tokens, final.cache, final.tier
			if costTier == nil && key.work.format != string(llm.APIFormatOpenAIChatCompletion) && key.work.format != string(llm.APIFormatOpenAIResponse) {
				sample.reportedTier = "(not-applicable)"
			}
			sample.mismatched = sample.mismatched || final.model != "" && final.model != key.work.model
			sample.unverified = !final.valid || final.model == "" || sample.reportedTier == ""
		}
		s.routeHistory.record(key, sample)
		emit := log.Debug
		if percent > 0 || d.suggested != 0 {
			emit = log.Info
		}
		emit(ctx, "首内容选路对照", log.Int("request_id", s.Request.ID), log.Int("execution_id", exec.ID),
			log.String("mode", d.mode), log.Bool("trial_group", percent > 0 && d.session%100 < uint64(percent)),
			log.Int("selected_channel", key.channel), log.Int("suggested_channel", d.keys[d.suggested].channel),
			log.Int("selected_index", d.chosen), log.Int("suggested_index", d.suggested),
			log.Int("attempts", len(t.Attempts)), log.Bool("content_written", t.FirstWriteContent != nil),
			log.Int64("first_content_ms", sample.delay.Milliseconds()), log.Bool("completed", t.Delivered != nil && s.StreamCompleted),
			log.Bool("canceled", t.Delivered == nil && errors.Is(client.Err(), context.Canceled)),
			log.Bool("comparable", !sample.failed && !sample.unverified && !sample.mismatched))
	})
}
