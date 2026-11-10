package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/pkg/xcontext"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

const titleHedgeDelay = 10 * time.Second

type titleHedgeAttempt struct {
	winner atomic.Bool
}

type titleRequestContextKey struct{}

// A local race cancellation must not erase an already observed provider error.
// Actual client cancellation and errors caused by cancellation stay neutral.
func titleHealthContext(ctx context.Context, err error) context.Context {
	parent, _ := ctx.Value(titleRequestContextKey{}).(context.Context)
	if parent != nil && parent.Err() == nil && err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return context.WithoutCancel(ctx)
	}
	return ctx
}

func isCodexTitleRequest(request *httpclient.Request) bool {
	if request == nil {
		return false
	}
	metadata := request.Headers.Get("X-Codex-Turn-Metadata")
	return gjson.Valid(metadata) && gjson.Get(metadata, "thread_source").String() == "thread_title"
}

func canHedgeTitle(ctx context.Context, state *PersistenceState, request *llm.Request, policy *biz.RetryPolicy) bool {
	return state.titleTask && request.Stream != nil && *request.Stream &&
		policy.Enabled && policy.MaxChannelRetries > 0 && len(state.ChannelModelsCandidates) > 1 &&
		!shared.IsResponsesWebSocket(ctx) && (request.PreviousResponseID == nil || *request.PreviousResponseID == "")
}

type titleAttemptResult struct {
	index     int
	state     *PersistenceState
	outbound  *PersistentOutboundTransformer
	tracker   *healthGateAttemptTracker
	ctx       context.Context
	lifecycle *streamLifecycle
	result    *pipeline.Result
	err       error
}

type titleAttemptDecision struct {
	chosen     bool
	superseded bool
}

type titleDispatchGuard struct {
	mu  sync.Mutex
	err error
}

func (g *titleDispatchGuard) stop(err error) {
	// A backup's unaffordable estimate only rejects that optional attempt.
	// Uncertain execution, even when wrapping a budget error, forbids replay.
	var denied *biz.BudgetDenied
	if !titleHedgeStop(err) || errors.As(err, &denied) && !errors.Is(err, httpclient.ErrRequestMayHaveExecuted) {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.err == nil {
		g.err = err
	}
}

func (g *titleDispatchGuard) check() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.err
}

// Title attempts use the normal candidate/model/retry pipeline. One lane owns
// the preferred channel; a delayed second lane owns the remaining candidates.
// There are at most two live upstream attempts and one delayed launch, even
// when the fallback lane retries. The last remaining attempt is never timed out
// merely because the hedge delay elapsed.
func (processor *ChatCompletionOrchestrator) raceTitle(
	ctx context.Context, state *PersistenceState, request *llm.Request,
	primary preparedRequestPipeline, outbound *PersistentOutboundTransformer,
	tracker *healthGateAttemptTracker, policy *biz.RetryPolicy, delay time.Duration,
) titleAttemptResult {
	seed := *state
	var forkErr error
	if state.PluginHooks.ForkForHedge != nil {
		seed.PluginHooks, forkErr = state.PluginHooks.ForkForHedge()
	}
	backupStarted := false
	defer func() {
		if !backupStarted && seed.PluginHooks.Request != nil && state.PluginHooks.ForkForHedge != nil {
			seed.PluginHooks.Request.OnProcessEnd()
		}
	}()
	raw := *state.RawRequest
	raw.Headers = raw.Headers.Clone()
	requestRecord := *state.Request
	seed.Request = &requestRecord
	candidates := cloneTitleCandidates(state.ChannelModelsCandidates)
	state.ChannelModelsCandidates = candidates[:1:1]
	state.hedge = &titleHedgeAttempt{}
	results := make(chan titleAttemptResult, 2)
	decisions := []chan titleAttemptDecision{make(chan titleAttemptDecision, 1), make(chan titleAttemptDecision, 1)}
	cancels := make([]context.CancelFunc, 2)
	started, finished := 0, 0
	guard := &titleDispatchGuard{}

	launch := func(index int) {
		if index == 1 {
			backupStarted = true
		}
		attemptCtx, cancel := context.WithCancel(contexts.ForkRequest(ctx))
		cancels[index] = cancel
		owner, attemptCtx := newStreamLifecycle(attemptCtx)
		attemptCtx = context.WithValue(attemptCtx, titleRequestContextKey{}, ctx)
		attemptCtx = shared.WithResponsesAttempt(attemptCtx, &shared.ResponsesAttempt{})
		started++
		go func() {
			outcome := titleAttemptResult{index: index, state: state, outbound: outbound, tracker: tracker, ctx: attemptCtx, lifecycle: owner}
			defer func() {
				if recovered := recover(); recovered != nil {
					outcome.err = fmt.Errorf("title attempt panicked: %v", recovered)
					log.Error(attemptCtx, "title attempt panicked", log.Any("panic", recovered))
				}
				if outcome.err != nil {
					guard.stop(outcome.err)
					// Finalize actual failure before a later winner cancels this
					// context. Cleanup cancellation must not erase health evidence.
					healthErr := outcome.err
					var stopped *pipeline.DispatchStoppedError
					if errors.As(outcome.err, &stopped) {
						healthErr = stopped.LastAttemptError
						if healthErr == nil {
							healthErr = context.Canceled // no provider attempt ran in this lane
						}
					}
					finalErr := outcome.tracker.finalize(outcome.ctx, healthErr, false)
					if stopped == nil {
						outcome.err = finalErr
					}
					if err := outcome.outbound.endBudgetAttempt(outcome.ctx); err != nil {
						log.Warn(outcome.ctx, "close failed title budget attempt", log.Cause(err))
					}
				} else if outcome.result != nil && outcome.result.Stream {
					// Every returned stream owns its health ticket until Close,
					// including a stream that loses before the caller consumes it.
					_ = outcome.tracker.finalize(outcome.ctx, nil, true)
				}
				results <- outcome
				decision := <-decisions[index]
				if !decision.chosen {
					cancel()
					processor.closeRejectedTitle(outcome, decision.superseded)
				}
			}()
			if index == 0 {
				outcome.state, outcome.outbound, outcome.tracker = state, outbound, tracker
				outcome.result, outcome.err = primary.ProcessPrepared(attemptCtx, request, pipeline.WithStreamOutputCommit(), pipeline.WithDispatchGuard(guard.check))
				return
			}
			branch := seed
			branch.hedge = &titleHedgeAttempt{}
			branch.Perf = nil
			branch.ChannelModelsCandidates = cloneTitleCandidates(candidates[1:])
			branch.ResponsesContinuation = newResponsesContinuation(processor.responsesSessions, raw.Body)
			branchRaw := raw
			branchRaw.Body = bytes.Clone(raw.Body)
			branchRaw.Headers = raw.Headers.Clone()
			branchPipe, branchOutbound, branchTracker := processor.requestPipeline(&branch, policy)
			outcome.state, outcome.outbound, outcome.tracker = &branch, branchOutbound, branchTracker
			if forkErr != nil {
				outcome.err = forkErr
				return
			}
			var decision *healthGateRequestDecision
			attemptCtx = context.WithValue(attemptCtx, healthGateDecisionContextKey{}, &decision)
			outcome.ctx = attemptCtx
			prepared, err := branchPipe.Prepare(attemptCtx, &branchRaw)
			if err != nil {
				outcome.err = err
				return
			}
			outcome.result, outcome.err = branchPipe.ProcessPrepared(attemptCtx, prepared, pipeline.WithStreamOutputCommit(), pipeline.WithDispatchGuard(guard.check))
		}()
	}

	launch(0)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	var last titleAttemptResult
	ctxDone := ctx.Done()
	choose := func(outcome titleAttemptResult) titleAttemptResult {
		winner := outcome.index
		outcome.state.hedge.winner.Store(true)
		for index := range started {
			decisions[index] <- titleAttemptDecision{chosen: index == winner, superseded: outcome.err == nil}
			if index != winner {
				cancels[index]()
			}
		}
		return outcome
	}
	for {
		select {
		case outcome := <-results:
			finished++
			last = outcome
			if ctx.Err() != nil {
				if finished == started {
					last.err = ctx.Err()
					return choose(last)
				}
				continue
			}
			if outcome.err == nil {
				return choose(outcome)
			}
			if started == 1 {
				if titleHedgeStop(outcome.err) {
					return choose(outcome)
				}
				// Once the original fails, this is an ordinary channel retry.
				// Keep its configured backoff, distinct from speculative delay.
				timer.Reset(time.Duration(policy.RetryDelayMs) * time.Millisecond)
			}
			if started == 2 && finished == started {
				return choose(last)
			}
		case <-timer.C:
			if started == 1 && ctx.Err() == nil {
				launch(1)
			}
		case <-ctxDone:
			ctxDone = nil
			for index := range started {
				cancels[index]()
			}
			if finished == started {
				last.err = ctx.Err()
				return choose(last)
			}
		}
	}
}

func titleHedgeStop(err error) bool {
	var denied *biz.BudgetDenied
	return isPluginOverloaded(err) || errors.Is(err, httpclient.ErrRequestMayHaveExecuted) ||
		errors.Is(err, httpclient.ErrContinuationUnavailable) || errors.Is(err, httpclient.ErrContinuationRestoreFailed) || shared.IsResponsesRecoveryError(err) ||
		errors.Is(err, transformer.ErrUnsupportedConversion) || errors.Is(err, pipeline.ErrPreCommitBufferExceeded) || errors.Is(err, httpclient.ErrStreamEventTooLarge) ||
		(errors.As(err, &denied) && !denied.CandidateOnly)
}

func cloneTitleCandidates(candidates []*ChannelModelsCandidate) []*ChannelModelsCandidate {
	clones := make([]*ChannelModelsCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		clone := *candidate
		clone.Models = append([]biz.ChannelModelEntry(nil), candidate.Models...)
		clone.modelAPIFormats = append([]string(nil), candidate.modelAPIFormats...)
		if candidate.healthGate != nil {
			info := *candidate.healthGate
			if info.decision != nil {
				decision := *info.decision
				decision.record.Skipped = append(decision.record.Skipped[:0:0], decision.record.Skipped...)
				if decision.record.Owner != nil {
					owner := *decision.record.Owner
					decision.record.Owner = &owner
				}
				info.decision = &decision
			}
			clone.healthGate = &info
		}
		clones = append(clones, &clone)
	}
	return clones
}

func (processor *ChatCompletionOrchestrator) closeRejectedTitle(outcome titleAttemptResult, superseded bool) {
	if outcome.result != nil && outcome.result.EventStream != nil {
		_ = outcome.result.EventStream.Close()
	}
	// Closing the consumer alone does not settle a retained raw stream.
	// Wait for its finalization before releasing ownership or marking the loss.
	outcome.lifecycle.Close()
	if outcome.outbound != nil {
		if err := outcome.outbound.endBudgetAttempt(outcome.ctx); err != nil {
			log.Warn(outcome.ctx, "close losing title budget attempt", log.Cause(err))
		}
		if outcome.err == nil && (outcome.result == nil || !outcome.result.Stream) {
			_ = outcome.tracker.finalize(outcome.ctx, context.Canceled, false)
		}
	}
	if outcome.state != nil {
		if continuation := outcome.state.ResponsesContinuation; continuation != nil {
			continuation.releaseRetention()
			continuation.releaseCapture()
		}
		if execution := outcome.state.RequestExec; execution != nil && superseded && (outcome.err == nil || errors.Is(outcome.err, context.Canceled)) {
			ctx, cancel := xcontext.DetachWithTimeout(outcome.ctx, 10*time.Second)
			defer cancel()
			if err := processor.RequestService.MarkExecutionHedgeLost(ctx, execution.ID); err != nil {
				log.Warn(ctx, "record losing title attempt", log.Cause(err))
			}
		}
	}
}

type titleOwnedStream struct {
	streams.Stream[*httpclient.StreamEvent]
	owner *streamLifecycle
	once  sync.Once
}

func (s *titleOwnedStream) Close() error {
	err := s.Stream.Close()
	s.once.Do(s.owner.Close)
	return err
}
