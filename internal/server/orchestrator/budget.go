package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/pkg/budget"
	"github.com/looplj/axonhub/internal/pkg/xcontext"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
)

type budgetAdmission struct {
	pipeline.DummyMiddleware
	outbound *PersistentOutboundTransformer
}

func withBudgetAdmission(outbound *PersistentOutboundTransformer) pipeline.Middleware {
	return &budgetAdmission{outbound: outbound}
}
func (*budgetAdmission) Name() string { return "budget-admission" }
func (m *budgetAdmission) OnOutboundRawRequest(ctx context.Context, raw *httpclient.Request) (*httpclient.Request, error) {
	s := m.outbound.state
	s.budgetUnreserved = false
	if s.APIKey == nil {
		return raw, nil
	}
	if s.Request == nil || s.RequestExec == nil {
		return nil, &biz.BudgetDenied{Reason: "accounting_unavailable"}
	}
	service := s.UsageLogService.BudgetService
	var estimate budget.Amount
	var quoteErr error
	continuation := s.ResponsesContinuation
	needsHistory := continuation != nil && continuation.lane == nil && !continuation.complete && continuation.owner != nil &&
		continuation.owner.Origin.Generation != 0
	if needsHistory {
		quoteErr = biz.ErrBudgetNeedsHistoryQuote
	} else if s.LlmRequest == nil || s.LlmRequest.RequestType != llm.RequestTypeTokenCount {
		estimate, quoteErr = service.Estimate(ctx, s.RequestExec, s.LlmRequest, raw, estimatePromptTokens(s.LlmRequest))
	}
	held, err := service.ReserveAttempt(ctx, s.APIKey.ProjectID, s.APIKey.ID, s.Request.ID, s.RequestExec.ID, estimate, quoteErr)
	if errors.Is(err, biz.ErrBudgetNeedsHistoryQuote) {
		if continuation.owner.Snapshot == nil {
			quoteErr = continuation.loadHTTPSnapshot(ctx)
		} else {
			quoteErr = nil
		}
		if quoteErr == nil {
			full, restoreErr := expandResponsesSnapshot(raw.Body, continuation.owner.Snapshot)
			quoteErr = restoreErr
			if restoreErr == nil {
				input := *raw
				input.Body, input.JSONBody = full, full
				estimate, quoteErr = service.Estimate(ctx, s.RequestExec, s.LlmRequest, &input, estimatePromptTokens(s.LlmRequest))
			}
		}
		held, err = service.ReserveAttempt(ctx, s.APIKey.ProjectID, s.APIKey.ID, s.Request.ID, s.RequestExec.ID, estimate, quoteErr)
	}
	if err == nil {
		s.budgetUnreserved = !held
		if held {
			s.BudgetAttempt = service.KeepAttemptAlive(ctx, s.RequestExec.ID)
		}
		return raw, nil
	}
	var denied *biz.BudgetDenied
	if !errors.As(err, &denied) {
		log.Error(ctx, "budget admission failed", log.Cause(err))
		denied = &biz.BudgetDenied{Reason: "accounting_unavailable"}
	}
	if !denied.CandidateOnly {
		s.BudgetStop = denied
	}
	return nil, denied
}
func (m *budgetAdmission) OnOutboundRawError(ctx context.Context, _ error) {
	if err := m.outbound.endBudgetAttempt(ctx); err != nil {
		log.Error(ctx, "close budget attempt", log.Cause(err))
	}
}
func (p *PersistentOutboundTransformer) endBudgetAttempt(ctx context.Context) error {
	if p.state.BudgetAttempt != nil {
		p.state.BudgetAttempt.Stop()
		p.state.BudgetAttempt = nil
	}
	if p.state.budgetUnreserved || p.state.APIKey == nil || p.state.RequestExec == nil {
		return nil
	}
	persistCtx, cancel := xcontext.DetachWithTimeout(ctx, 10*time.Second)
	defer cancel()
	lost, err := p.state.UsageLogService.BudgetService.EndOwnedAttempt(persistCtx, p.state.RequestExec.ID)
	if lost {
		p.state.BudgetStop = fmt.Errorf("%w: %w", httpclient.ErrRequestMayHaveExecuted, &biz.BudgetDenied{Reason: "accounting_unavailable"})
		return p.state.BudgetStop
	}
	if err == nil {
		err = p.state.settleTokenCountBudget(persistCtx)
	}

	return err
}

func (s *PersistenceState) settleTokenCountBudget(ctx context.Context) error {
	if s.budgetUnreserved || s.APIKey == nil || s.RequestExec == nil || s.LlmRequest == nil || s.LlmRequest.RequestType != llm.RequestTypeTokenCount {
		return nil
	}
	zero := budget.Amount(0)
	_, _, err := s.UsageLogService.BudgetService.Settle(ctx, s.RequestExec.ID, &zero, nil)
	return err
}

type budgetExecutor struct {
	pipeline.Executor
	service     *biz.BudgetService
	executionID int
	attempt     *biz.BudgetAttempt
}

func (p *PersistentOutboundTransformer) budgetExecutor(executor pipeline.Executor) pipeline.Executor {
	if p.state.budgetUnreserved || p.state.APIKey == nil || p.state.RequestExec == nil {
		return executor
	}
	return &budgetExecutor{executor, p.state.UsageLogService.BudgetService, p.state.RequestExec.ID, p.state.BudgetAttempt}
}

func (e *budgetExecutor) Do(ctx context.Context, request *httpclient.Request) (*httpclient.Response, error) {
	if e.attempt != nil {
		var cancel context.CancelFunc
		ctx, cancel = e.attempt.Bind(ctx)
		defer cancel()
	}
	if err := e.service.MarkSent(ctx, e.executionID); err != nil {
		return nil, &biz.BudgetDenied{Reason: "accounting_unavailable"}
	}
	response, err := e.Executor.Do(ctx, request)
	if ownerErr := budgetOwnerError(e.attempt); ownerErr != nil {
		if request.OnReplayUnsafe != nil {
			request.OnReplayUnsafe()
		}
		return nil, ownerErr
	}
	return response, e.releaseUnsent(ctx, err)
}
func (e *budgetExecutor) DoStream(ctx context.Context, request *httpclient.Request) (streams.Stream[*httpclient.StreamEvent], error) {
	cancel := func() {}
	if e.attempt != nil {
		ctx, cancel = e.attempt.Bind(ctx)
	}
	if err := e.service.MarkSent(ctx, e.executionID); err != nil {
		cancel()
		return nil, &biz.BudgetDenied{Reason: "accounting_unavailable"}
	}
	stream, err := e.Executor.DoStream(ctx, request)
	if ownerErr := budgetOwnerError(e.attempt); ownerErr != nil {
		if stream != nil {
			_ = stream.Close()
		}
		cancel()
		if request.OnReplayUnsafe != nil {
			request.OnReplayUnsafe()
		}
		return nil, ownerErr
	}
	if err != nil {
		err = e.releaseUnsent(ctx, err)
		cancel()
		return nil, err
	}
	return &budgetStream{Stream: stream, ctx: ctx, service: e.service, executionID: e.executionID, attempt: e.attempt, cancel: cancel}, nil
}

// MarkSent fences the transport before checkout. Only the transport can
// attest that a local continuation failure occurred before any provider write.
func (e *budgetExecutor) releaseUnsent(ctx context.Context, cause error) error {
	if !errors.Is(cause, httpclient.ErrRequestNotSent) {
		return cause
	}
	persistCtx, cancel := xcontext.DetachWithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := e.service.ReleaseUnwrittenAttempt(persistCtx, e.executionID); err != nil {
		return errors.Join(cause, &biz.BudgetDenied{Reason: "accounting_unavailable"})
	}
	return cause
}

type budgetStream struct {
	streams.Stream[*httpclient.StreamEvent]
	ctx         context.Context
	service     *biz.BudgetService
	executionID int
	attempt     *biz.BudgetAttempt
	cancel      context.CancelFunc
}

func (s *budgetStream) Close() error {
	if s.cancel != nil {
		defer s.cancel()
	}
	if s.attempt != nil {
		s.attempt.Stop()
	}
	err := s.Stream.Close()
	ctx, cancel := xcontext.DetachWithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	lost, endErr := s.service.EndOwnedAttempt(ctx, s.executionID)
	if lost {
		endErr = fmt.Errorf("%w: %w", httpclient.ErrRequestMayHaveExecuted, &biz.BudgetDenied{Reason: "accounting_unavailable"})
	}
	if endErr != nil {
		log.Error(ctx, "close streaming budget attempt", log.Cause(endErr))
		if err == nil {
			return endErr
		}
	}
	return err
}

func budgetOwnerError(attempt *biz.BudgetAttempt) error {
	if attempt != nil && attempt.Err() != nil {
		return fmt.Errorf("%w: %w", httpclient.ErrRequestMayHaveExecuted, attempt.Err())
	}
	return nil
}

func (s *budgetStream) Err() error {
	if err := budgetOwnerError(s.attempt); err != nil {
		return err
	}
	return s.Stream.Err()
}
