package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"sync"
	"time"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

// Retryable interface for transformers that support channel switching.
type Retryable interface {
	// HasMoreChannels returns true if there are more channels to switch to.
	// It will only be called if the attempt count is less than maxRetries.
	HasMoreChannels() bool

	// NextChannel switches to the next channel.
	// It will be called if HasMoreChannels returns true.
	NextChannel(ctx context.Context) error
}

// RetryStopObserver receives the pipeline's actual terminal retry decision.
// Observers must not change routing or propagate diagnostic persistence failures.
type RetryStopObserver interface{ OnRetryStopped(context.Context, string) }

func (p *pipeline) notifyRetryStopped(ctx context.Context, reason string) {
	if observer, ok := p.Outbound.(RetryStopObserver); ok {
		observer.OnRetryStopped(ctx, reason)
	}
}

// ChannelRetryable interface for transformers that support same-channel retry.
type ChannelRetryable interface {
	// CanRetry returns true if the transformer can retry for current channel given the error that occurred.
	// It will only be called if the attempt count is less than maxSameChannelRetries.
	CanRetry(err error) bool

	// PrepareForRetry prepares the transformer for retry.
	// It will be called if CanRetry returns true.
	PrepareForRetry(ctx context.Context) error
}

// ChannelTimeoutRetryable permits a timeout retry only when the transformer can
// choose another address. Other transformers still skip same-channel timeouts.
type ChannelTimeoutRetryable interface {
	CanRetryAfterTimeout(err error) bool
}

// ChannelSameChannelRetryLimit is an optional interface a ChannelRetryable may
// also implement to override the global max same-channel retries for the current
// channel. It is checked on every same-channel retry decision.
type ChannelSameChannelRetryLimit interface {
	// SameChannelRetryLimit returns the max same-channel retries for the current
	// channel. ok is false when the channel sets no override, in which case the
	// global retry policy value is used.
	SameChannelRetryLimit() (limit int, ok bool)
}

// ChannelCustomizedExecutor interface for channel need custom the process of request.
// The customized executor will be used to execute the request.
// e.g. the aws bedrock process need a custom executor to handle the request.
type ChannelCustomizedExecutor interface {
	CustomizeExecutor(Executor) Executor
}

// ResponseTimeoutProvider lets an outbound transformer override the pipeline
// response timeouts for the current attempt. It is how per-channel timeout
// settings are applied: the outbound transformer knows which channel is
// currently selected, including after a retry switches channels.
//
// A nil return value means "keep the pipeline default" for that timeout, so a
// channel can override only one of the two timeouts. A non-nil zero duration
// disables the corresponding timeout.
type ResponseTimeoutProvider interface {
	ResponseTimeouts(ctx context.Context) (streamFirstEventTimeout, nonStreamTimeout *time.Duration)
}

// Option defines a pipeline configuration option.
type Option func(*pipeline)

// WithRetry configures both cross-channel and same-channel retry behavior for the pipeline.
// maxChannelRetries: the maximum number of times to switch to the next channel.
// maxSameChannelRetries: the maximum number of times to retry the same channel.
// retryDelay: the delay between retries.
func WithRetry(maxChannelRetries, maxSameChannelRetries int, retryDelay time.Duration) Option {
	return func(p *pipeline) {
		p.maxChannelRetries = maxChannelRetries
		p.maxSameChannelRetries = maxSameChannelRetries
		p.retryDelay = retryDelay
	}
}

// WithRetriesDisabled marks the pipeline as running with the global retry policy
// switched off. A channel-level override only adjusts the retry count while the
// policy is enabled; it must not switch retries back on.
func WithRetriesDisabled() Option {
	return func(p *pipeline) {
		p.retriesDisabled = true
	}
}

// WithMiddlewares configures decorators for the pipeline.
func WithMiddlewares(decorators ...Middleware) Option {
	return func(p *pipeline) {
		p.middlewares = append(p.middlewares, decorators...)
	}
}

// WithEmptyResponseDetection enables detection of empty streaming responses.
// When enabled, the pipeline pre-reads up to 3 events from the LLM stream to check
// if the response contains any meaningful content. If the stream ends without content,
// an ErrEmptyResponse error is returned so the existing retry flow can handle it as a failed attempt.
func WithEmptyResponseDetection() Option {
	return func(p *pipeline) {
		p.emptyResponseDetection = true
	}
}

func WithResponseTimeouts(streamFirstEventTimeout, nonStreamTimeout time.Duration) Option {
	return func(p *pipeline) {
		p.streamFirstEventTimeout = streamFirstEventTimeout
		p.nonStreamTimeout = nonStreamTimeout
	}
}

// WithStreamOutputCommit keeps reasoning-only and startup events private until
// user-visible output. It is used by callers racing short structured responses.
func WithStreamOutputCommit() Option {
	return func(p *pipeline) { p.streamOutputCommit = true }
}

// WithDispatchGuard prevents future attempts after a concurrent attempt has
// reported an outcome that forbids replay. It never cancels an in-flight call.
func WithDispatchGuard(guard func() error) Option {
	return func(p *pipeline) { p.dispatchGuard = guard }
}

// DispatchStoppedError distinguishes a logical dispatch stop from this lane's
// last provider outcome, so concurrent callers can finalize health accurately.
type DispatchStoppedError struct {
	Cause            error
	LastAttemptError error
}

func (e *DispatchStoppedError) Error() string { return e.Cause.Error() }
func (e *DispatchStoppedError) Unwrap() error { return e.Cause }

// Factory creates pipeline instances.
type Factory struct {
	Executor Executor
}

// NewFactory creates a new pipeline factory.
func NewFactory(executor Executor) *Factory {
	return &Factory{
		Executor: executor,
	}
}

// Pipeline creates a new pipeline with options.
func (f *Factory) Pipeline(
	inbound transformer.Inbound,
	outbound transformer.Outbound,
	opts ...Option,
) *pipeline {
	p := &pipeline{
		Executor: f.Executor,
		Inbound:  inbound,
		Outbound: outbound,
	}

	// Apply options
	for _, opt := range opts {
		opt(p)
	}

	return p
}

// pipeline implements the main pipeline logic with retry capabilities.
type pipeline struct {
	cleanupOnce             sync.Once
	Executor                Executor
	Inbound                 transformer.Inbound
	Outbound                transformer.Outbound
	middlewares             []Middleware
	maxChannelRetries       int
	maxSameChannelRetries   int
	retriesDisabled         bool
	retryDelay              time.Duration
	emptyResponseDetection  bool
	streamFirstEventTimeout time.Duration
	nonStreamTimeout        time.Duration
	streamOutputCommit      bool
	dispatchGuard           func() error
}

type Result struct {
	// Stream indicates whether the response is a stream
	Stream bool

	// Response is the final HTTP response, if Stream is false
	Response *httpclient.Response

	// EventStream is the stream of events, if Stream is true
	EventStream streams.Stream[*httpclient.StreamEvent]
}

func (p *pipeline) applyBeforeRequestMiddlewares(ctx context.Context, request *llm.Request) (*llm.Request, error) {
	var err error

	for _, dec := range p.middlewares {
		request, err = dec.OnInboundLlmRequest(ctx, request)
		if err != nil {
			return nil, err
		}
	}

	return request, nil
}

func (p *pipeline) applyInboundRawResponseMiddlewares(ctx context.Context, response *httpclient.Response) (*httpclient.Response, error) {
	var err error

	for _, dec := range p.middlewares {
		response, err = dec.OnInboundRawResponse(ctx, response)
		if err != nil {
			return nil, err
		}
	}

	return response, nil
}

func (p *pipeline) applyInboundRawStreamMiddlewares(ctx context.Context, stream streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*httpclient.StreamEvent], error) {
	var err error

	for _, dec := range p.middlewares {
		stream, err = dec.OnInboundRawStream(ctx, stream)
		if err != nil {
			return nil, err
		}
	}

	return stream, nil
}

func (p *pipeline) applyRawRequestMiddlewares(ctx context.Context, request *httpclient.Request) (*httpclient.Request, error) {
	var err error

	for _, dec := range p.middlewares {
		request, err = dec.OnOutboundRawRequest(ctx, request)
		if err != nil {
			return nil, err
		}
	}

	return request, nil
}

func (p *pipeline) applyRawResponseMiddlewares(ctx context.Context, response *httpclient.Response) (*httpclient.Response, error) {
	var err error

	// Response middlewares should be applied in reverse order (last to first)
	for i := len(p.middlewares) - 1; i >= 0; i-- {
		response, err = p.middlewares[i].OnOutboundRawResponse(ctx, response)
		if err != nil {
			return nil, err
		}
	}

	return response, nil
}

func (p *pipeline) applyRawStreamMiddlewares(ctx context.Context, stream streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*httpclient.StreamEvent], error) {
	var err error

	// Stream middlewares should be applied in reverse order (last to first)
	for i := len(p.middlewares) - 1; i >= 0; i-- {
		stream, err = p.middlewares[i].OnOutboundRawStream(ctx, stream)
		if err != nil {
			return nil, err
		}
	}

	return stream, nil
}

func (p *pipeline) applyRawErrorResponseMiddlewares(ctx context.Context, err error) {
	// Error response middlewares should be applied in reverse order (last to first)
	for i := len(p.middlewares) - 1; i >= 0; i-- {
		p.middlewares[i].OnOutboundRawError(ctx, err)
	}
}

func (p *pipeline) applyLlmResponseMiddlewares(ctx context.Context, response *llm.Response) (*llm.Response, error) {
	var err error

	// LLM response middlewares should be applied in reverse order (last to first)
	for i := len(p.middlewares) - 1; i >= 0; i-- {
		response, err = p.middlewares[i].OnOutboundLlmResponse(ctx, response)
		if err != nil {
			return nil, err
		}
	}

	return response, nil
}

func (p *pipeline) applyLlmStreamMiddlewares(ctx context.Context, stream streams.Stream[*llm.Response]) (streams.Stream[*llm.Response], error) {
	var err error

	// LLM stream middlewares should be applied in reverse order (last to first)
	for i := len(p.middlewares) - 1; i >= 0; i-- {
		stream, err = p.middlewares[i].OnOutboundLlmStream(ctx, stream)
		if err != nil {
			return nil, err
		}
	}

	return stream, nil
}

func (p *pipeline) finishProcess() {
	p.cleanupOnce.Do(func() {
		for _, middleware := range p.middlewares {
			defer middleware.OnProcessEnd()
		}
	})
}

func (p *pipeline) Process(ctx context.Context, request *httpclient.Request) (*Result, error) {
	defer p.finishProcess()
	llmRequest, err := p.Prepare(ctx, request)
	if err != nil {
		return nil, err
	}
	return p.ProcessPrepared(ctx, llmRequest)
}

// Prepare validates and selects candidates once before dispatch. Callers that
// schedule independent attempts must give each attempt its own pipeline/state.
func (p *pipeline) Prepare(ctx context.Context, request *httpclient.Request) (*llm.Request, error) {
	prepared := false
	defer func() {
		if !prepared {
			p.finishProcess()
		}
	}()
	// Step 1: Transform httpclient.Request to llm.Request using inbound transformer
	llmRequest, err := p.Inbound.TransformRequest(ctx, request)
	if err != nil {
		return nil, err
	}

	// Make the original raw request available before request middlewares run.
	llmRequest.RawRequest = request

	// Step 2: Apply before request middlewares
	llmRequest, err = p.applyBeforeRequestMiddlewares(ctx, llmRequest)
	if err != nil {
		return nil, err
	}

	prepared = true
	return llmRequest, nil
}

// ProcessPrepared runs the ordinary retry policy for an already prepared request.
func (p *pipeline) ProcessPrepared(ctx context.Context, llmRequest *llm.Request, options ...Option) (*Result, error) {
	defer p.finishProcess()
	for _, option := range options {
		option(p)
	}
	originalStream := llmRequest.Stream

	var lastErr error

	channelSwitches := 0
	sameChannelRetries := 0

	// Step 3: Process the request
	for {
		if p.dispatchGuard != nil {
			if err := p.dispatchGuard(); err != nil {
				p.notifyRetryStopped(ctx, "concurrent_attempt_stopped")
				return nil, &DispatchStoppedError{Cause: err, LastAttemptError: lastErr}
			}
		}
		llmRequest.Stream = originalStream

		result, err := p.processRequest(ctx, llmRequest)
		if err == nil {
			return result, nil
		}

		lastErr = err
		if errors.Is(lastErr, httpclient.ErrContinuationUnavailable) || errors.Is(lastErr, httpclient.ErrContinuationRestoreFailed) {
			p.notifyRetryStopped(ctx, "continuation_unavailable")
			return nil, lastErr
		}
		if errors.Is(lastErr, httpclient.ErrRequestMayHaveExecuted) {
			p.notifyRetryStopped(ctx, "upstream_outcome_unknown")
			return nil, lastErr
		}
		if shared.IsResponsesRecoveryError(lastErr) {
			return nil, shared.ResponsesRecoveryError()
		}
		if errors.Is(lastErr, transformer.ErrUnsupportedConversion) {
			return nil, lastErr
		}
		if errors.Is(lastErr, ErrPreCommitBufferExceeded) || errors.Is(lastErr, httpclient.ErrStreamEventTooLarge) {
			p.notifyRetryStopped(ctx, "response_size_limit")
			return nil, lastErr
		}

		// Stop retrying if the context is canceled or the deadline is exceeded.
		if ctx.Err() != nil {
			p.notifyRetryStopped(ctx, "context_done")
			return nil, lastErr
		}

		// Determine retry strategy
		canRetry := false
		timeoutRetry := isResponseTimeoutError(lastErr)

		// 1. Try same-channel retry first if supported
		if channelRetryable, ok := p.Outbound.(ChannelRetryable); ok && sameChannelRetries < p.maxSameChannelRetriesFor() {
			allowSameChannel := false
			if timeoutRetry {
				if timeoutRetryable, ok := p.Outbound.(ChannelTimeoutRetryable); ok {
					allowSameChannel = timeoutRetryable.CanRetryAfterTimeout(lastErr)
				}
			} else {
				allowSameChannel = channelRetryable.CanRetry(lastErr)
			}
			if allowSameChannel {
				if err := channelRetryable.PrepareForRetry(ctx); err == nil {
					sameChannelRetries++
					canRetry = true

					slog.DebugContext(ctx, "retrying same channel",
						slog.Int("same_channel_attempt", sameChannelRetries),
						slog.Int("max_same_channel_retries", p.maxSameChannelRetriesFor()),
					)
				} else {
					slog.WarnContext(ctx, "failed to prepare same channel retry, will try channel switch", slog.Any("error", err))
				}
			}
		}

		// 2. If same-channel retry not possible/exhausted, try channel switching
		if !canRetry {
			if retryable, ok := p.Outbound.(Retryable); ok {
				if channelSwitches < p.maxChannelRetries && retryable.HasMoreChannels() {
					if err := retryable.NextChannel(ctx); err == nil {
						channelSwitches++
						sameChannelRetries = 0 // Reset same-channel attempts for new channel
						canRetry = true

						slog.DebugContext(ctx, "switched to next channel",
							slog.Int("channel_switch_attempt", channelSwitches),
							slog.Int("max_channel_retries", p.maxChannelRetries),
						)
					} else {
						slog.WarnContext(ctx, "failed to switch to next channel", slog.Any("error", err))
					}
				}
			}
		}

		// If no retry strategy worked, break and return last error
		if !canRetry {
			reason := "no_retry_available"
			if p.maxChannelRetries == 0 && p.getMaxSameChannelRetries() == 0 {
				reason = "retry_disabled"
			} else if channelSwitches >= p.maxChannelRetries && sameChannelRetries >= p.getMaxSameChannelRetries() {
				reason = "retry_limit"
			} else if retryable, ok := p.Outbound.(Retryable); ok && !retryable.HasMoreChannels() {
				reason = "candidates_exhausted"
			}
			p.notifyRetryStopped(ctx, reason)
			break
		}

		// Add retry delay if configured
		if p.retryDelay > 0 {
			timer := time.NewTimer(p.retryDelay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return nil, lastErr
			}
		}

		slog.WarnContext(ctx, "request process failed, retrying...",
			slog.Any("error", lastErr),
			slog.Int("channel_switches", channelSwitches),
			slog.Int("same_channel_retries", sameChannelRetries),
		)
	}

	return nil, lastErr
}

func (p *pipeline) processRequest(ctx context.Context, request *llm.Request) (_ *Result, resultErr error) {
	originalWantStream := request.Stream != nil && *request.Stream
	metadata := maps.Clone(request.TransformerMetadata)

	httpReq, err := p.Outbound.TransformRequest(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to transform request: %w", err)
	}

	httpReq.TransformerMetadata = mergeTransformerMetadata(metadata, httpReq.TransformerMetadata)
	httpReq = httpclient.MergeInboundRequest(httpReq, request.RawRequest)

	httpReq, err = httpclient.FinalizeAuthHeaders(httpReq)
	if err != nil {
		return nil, fmt.Errorf("invalid authentication config: %w", err)
	}

	// Apply raw request middlewares
	httpReq, err = p.applyRawRequestMiddlewares(ctx, httpReq)
	if err != nil {
		p.applyRawErrorResponseMiddlewares(ctx, err)

		return nil, fmt.Errorf("failed to apply raw request middlewares: %w", err)
	}

	// Keep delivery state outside error transformations: timeouts, aggregation,
	// and provider error decoding must not make an ambiguous WS send replayable.
	replayUnsafe := false
	httpReq.OnReplayUnsafe = func() { replayUnsafe = true }
	defer func() {
		if replayUnsafe && resultErr != nil && !errors.Is(resultErr, httpclient.ErrRequestMayHaveExecuted) {
			resultErr = fmt.Errorf("%w: %w", httpclient.ErrRequestMayHaveExecuted, resultErr)
		}
	}()

	executor := p.Executor
	if c, ok := p.Outbound.(ChannelCustomizedExecutor); ok {
		executor = c.CustomizeExecutor(executor)
	}

	effectiveWantStream := request.Stream != nil && *request.Stream
	// Resolve the timeouts after TransformRequest so the outbound transformer
	// has selected the channel for this attempt and can apply channel overrides.
	streamFirstEventTimeout, nonStreamTimeout := p.effectiveResponseTimeouts(ctx)

	var result *Result
	switch {
	case originalWantStream:
		result = &Result{
			Stream: true,
		}

		stream, err := p.stream(ctx, executor, httpReq, streamFirstEventTimeout)
		if err != nil {
			return nil, fmt.Errorf("failed to stream request: %w", err)
		}

		result.EventStream = stream
	case effectiveWantStream:
		result = &Result{
			Stream: false,
		}

		timeoutCtx, cancel := withNonStreamTimeout(ctx, nonStreamTimeout)
		response, err := p.autoAggregateStream(timeoutCtx, executor, httpReq)
		cancel()
		if err != nil {
			if isNonStreamTimeout(timeoutCtx, nonStreamTimeout) {
				return nil, ErrNonStreamResponseTimeout
			}

			return nil, fmt.Errorf("failed to auto-aggregate streaming response: %w", err)
		}

		result.Response = response
	default:
		result = &Result{
			Stream: false,
		}

		timeoutCtx, cancel := withNonStreamTimeout(ctx, nonStreamTimeout)
		response, err := p.notStream(timeoutCtx, executor, httpReq)
		cancel()
		if err != nil {
			if isNonStreamTimeout(timeoutCtx, nonStreamTimeout) {
				return nil, ErrNonStreamResponseTimeout
			}

			return nil, err
		}

		result.Response = response
	}

	return result, nil
}

// effectiveResponseTimeouts returns the timeouts to use for the current attempt,
// letting the outbound transformer override the pipeline-level defaults.
func (p *pipeline) effectiveResponseTimeouts(ctx context.Context) (time.Duration, time.Duration) {
	streamFirstEventTimeout := p.streamFirstEventTimeout
	nonStreamTimeout := p.nonStreamTimeout

	provider, ok := p.Outbound.(ResponseTimeoutProvider)
	if !ok {
		return streamFirstEventTimeout, nonStreamTimeout
	}

	streamOverride, nonStreamOverride := provider.ResponseTimeouts(ctx)
	if streamOverride != nil {
		streamFirstEventTimeout = *streamOverride
	}

	if nonStreamOverride != nil {
		nonStreamTimeout = *nonStreamOverride
	}

	return streamFirstEventTimeout, nonStreamTimeout
}

// getMaxSameChannelRetries returns the maximum number of same-channel retries.
func (p *pipeline) getMaxSameChannelRetries() int {
	return p.maxSameChannelRetries
}

// maxSameChannelRetriesFor returns the effective same-channel retry limit: a
// channel-level override when retries are enabled and the outbound transformer
// provides one, otherwise the global retry policy value. The override only
// adjusts the count; it cannot enable retries that the global policy disabled.
func (p *pipeline) maxSameChannelRetriesFor() int {
	if p.retriesDisabled {
		return 0
	}

	if override, ok := p.Outbound.(ChannelSameChannelRetryLimit); ok {
		if limit, ok := override.SameChannelRetryLimit(); ok {
			return limit
		}
	}

	return p.maxSameChannelRetries
}

func isResponseTimeoutError(err error) bool {
	return errors.Is(err, ErrStreamFirstEventTimeout) || errors.Is(err, ErrNonStreamResponseTimeout)
}

func withNonStreamTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}

	return context.WithTimeoutCause(ctx, timeout, ErrNonStreamResponseTimeout)
}

func isNonStreamTimeout(ctx context.Context, timeout time.Duration) bool {
	return timeout > 0 && errors.Is(context.Cause(ctx), ErrNonStreamResponseTimeout)
}
