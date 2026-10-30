package biz

import (
	"context"
	"net/url"
	"strings"
)

func (s *RequestService) SetExecutionRetryDecision(ctx context.Context, executionID int, decision string) error {
	return s.entFromContext(ctx).RequestExecution.UpdateOneID(executionID).SetRetryDecision(decision).Exec(ctx)
}

func (s *RequestService) SetRequestClientStatus(ctx context.Context, requestID, status int) error {
	return s.entFromContext(ctx).Request.UpdateOneID(requestID).SetClientStatusCode(status).Exec(ctx)
}

func (s *RequestService) SetExecutionUpstreamStatus(ctx context.Context, executionID, status int) error {
	return s.entFromContext(ctx).RequestExecution.UpdateOneID(executionID).SetResponseStatusCode(status).Exec(ctx)
}

func diagnosticRequestURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	parsed.User = nil
	query := parsed.Query()
	for key := range query {
		switch strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key)) {
		case "key", "apikey", "token", "accesstoken", "refreshtoken", "authorization", "password", "secret":
			query.Set(key, "[REDACTED]")
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
