package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
	"time"
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
	if s.APIKey == nil {
		return raw, nil
	}
	if s.Request == nil || s.RequestExec == nil {
		return nil, &biz.BudgetDenied{Reason: "accounting_unavailable"}
	}
	service := s.UsageLogService.BudgetService
	estimate, quoteErr := service.Estimate(ctx, s.RequestExec, s.LlmRequest, raw, estimatePromptTokens(s.LlmRequest))
	err := service.Reserve(ctx, s.APIKey.ProjectID, s.APIKey.ID, s.Request.ID, s.RequestExec.ID, estimate, quoteErr)
	if err == nil {
		s.BudgetAttempt = service.KeepAttemptAlive(ctx, s.RequestExec.ID)
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
	if p.state.APIKey == nil || p.state.RequestExec == nil {
		return nil
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	lost, err := p.state.UsageLogService.BudgetService.EndOwnedAttempt(persistCtx, p.state.RequestExec.ID)
	if lost {
		p.state.BudgetStop = fmt.Errorf("%w: %w", httpclient.ErrRequestMayHaveExecuted, &biz.BudgetDenied{Reason: "accounting_unavailable"})
		return p.state.BudgetStop
	}
	return err
}

type budgetExecutor struct {
	pipeline.Executor
	service     *biz.BudgetService
	executionID int
	attempt     *biz.BudgetAttempt
}

func (p *PersistentOutboundTransformer) budgetExecutor(executor pipeline.Executor) pipeline.Executor {
	if p.state.APIKey == nil || p.state.RequestExec == nil {
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
	return response, err
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
		cancel()
		return nil, err
	}
	return &budgetStream{Stream: stream, ctx: ctx, service: e.service, executionID: e.executionID, attempt: e.attempt, cancel: cancel}, nil
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
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 10*time.Second)
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
