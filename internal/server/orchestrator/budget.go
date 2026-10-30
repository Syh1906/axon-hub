package orchestrator

import (
	"context"
	"errors"
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
	if p.state.APIKey == nil || p.state.RequestExec == nil {
		return nil
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return p.state.UsageLogService.BudgetService.EndAttempt(persistCtx, p.state.RequestExec.ID)
}

type budgetExecutor struct {
	pipeline.Executor
	service     *biz.BudgetService
	executionID int
}

func (p *PersistentOutboundTransformer) budgetExecutor(executor pipeline.Executor) pipeline.Executor {
	if p.state.APIKey == nil || p.state.RequestExec == nil {
		return executor
	}
	return &budgetExecutor{executor, p.state.UsageLogService.BudgetService, p.state.RequestExec.ID}
}
func (e *budgetExecutor) Do(ctx context.Context, request *httpclient.Request) (*httpclient.Response, error) {
	if err := e.service.MarkSent(ctx, e.executionID); err != nil {
		return nil, &biz.BudgetDenied{Reason: "accounting_unavailable"}
	}
	return e.Executor.Do(ctx, request)
}
func (e *budgetExecutor) DoStream(ctx context.Context, request *httpclient.Request) (streams.Stream[*httpclient.StreamEvent], error) {
	if err := e.service.MarkSent(ctx, e.executionID); err != nil {
		return nil, &biz.BudgetDenied{Reason: "accounting_unavailable"}
	}
	stream, err := e.Executor.DoStream(ctx, request)
	if err != nil {
		return nil, err
	}
	return &budgetStream{Stream: stream, ctx: ctx, service: e.service, executionID: e.executionID}, nil
}

type budgetStream struct {
	streams.Stream[*httpclient.StreamEvent]
	ctx         context.Context
	service     *biz.BudgetService
	executionID int
}

func (s *budgetStream) Close() error {
	err := s.Stream.Close()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 10*time.Second)
	defer cancel()
	if endErr := s.service.EndAttempt(ctx, s.executionID); endErr != nil {
		log.Error(ctx, "close streaming budget attempt", log.Cause(endErr))
		if err == nil {
			return endErr
		}
	}
	return err
}
