package datamigrate

import (
	"context"
	"encoding/json"
	"net/http"

	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/clientsession"
	"github.com/looplj/axonhub/internal/pkg/requestreasoning"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/transformer/anthropic/claudecode"
	"github.com/looplj/axonhub/llm/transformer/openai/codex"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/looplj/axonhub/llm/transformer/opencode"
)

type V1_0_0_RC1_Fork_18 struct{}

func NewV1_0_0_RC1_Fork_18() DataMigrator   { return &V1_0_0_RC1_Fork_18{} }
func (*V1_0_0_RC1_Fork_18) Version() string { return "v1.0.0-rc1-fork.18" }

func (*V1_0_0_RC1_Fork_18) Migrate(ctx context.Context, db *ent.Client) error {
	ctx = authz.WithSystemBypass(ctx, "request-session-and-effort-upgrade")
	if err := backfillClientSessions(ctx, db); err != nil {
		return err
	}
	return backfillReportedEfforts(ctx, db)
}

func backfillClientSessions(ctx context.Context, db *ent.Client) error {
	for lastID := 0; ; {
		var rows []struct {
			ID             int
			Format         string
			RequestHeaders []byte  `json:"request_headers"`
			ClaudeUserID   *string `json:"claude_id"`
		}
		err := db.Request.Query().Where(request.IDGT(lastID), request.ClientSessionIDIsNil()).
			Order(ent.Asc(request.FieldID)).Limit(128).Modify(func(q *sql.Selector) {
			q.Select(q.C(request.FieldID), q.C(request.FieldFormat), q.C(request.FieldRequestHeaders)).
				AppendSelectExprAs(sqljson.ValuePath(q.C(request.FieldRequestBody), sqljson.Path("metadata", "user_id"), sqljson.Unquote(true)), "claude_id")
		}).Scan(ctx, &rows)
		if err != nil || len(rows) == 0 {
			return err
		}
		for _, row := range rows {
			lastID = row.ID
			var h http.Header
			_ = json.Unmarshal(row.RequestHeaders, &h)
			id := clientsession.Normalize(h.Get(clientsession.Header))
			if id == "" {
				id = clientsession.Normalize(opencode.GetSessionIDFromHeaders(h))
			}
			if id == "" && row.Format == string(llm.APIFormatAnthropicMessage) && row.ClaudeUserID != nil {
				if uid := claudecode.ParseUserID(*row.ClaudeUserID); uid != nil {
					id = clientsession.Normalize(uid.SessionID)
				}
			}
			if id == "" {
				id = clientsession.Normalize(codex.GetSessionIDFromHeaders(h))
			}
			if id == "" {
				continue
			}
			err := db.Request.Update().Where(request.IDEQ(row.ID), request.ClientSessionIDIsNil()).SetClientSessionID(id).
				Modify(func(q *sql.UpdateBuilder) { q.Set(request.FieldUpdatedAt, sql.Expr(request.FieldUpdatedAt)) }).Exec(ctx)
			if err != nil {
				return err
			}
		}
	}
}

// Only read inline wire evidence; never treat a cache reference, a trace ID, or
// an old denormalized effort as proof of what a provider received or returned.
func backfillReportedEfforts(ctx context.Context, db *ent.Client) error {
	for lastID := 0; ; {
		rows, err := db.RequestExecution.Query().Where(requestexecution.IDGT(lastID), requestexecution.ReasoningEvidenceIsNil(),
			requestexecution.FormatIn(string(llm.APIFormatOpenAIResponse), string(llm.APIFormatOpenAIResponseWebSocket), string(llm.APIFormatOpenAIResponseCompact),
				string(llm.APIFormatOpenAIChatCompletion), string(llm.APIFormatAnthropicMessage), string(llm.APIFormatGeminiContents)), func(q *sql.Selector) {
				q.Where(sql.Or(sqljson.HasKey(q.C(requestexecution.FieldRequestBody), sqljson.Path("input")),
					sqljson.HasKey(q.C(requestexecution.FieldRequestBody), sqljson.Path("reasoning")),
					sqljson.HasKey(q.C(requestexecution.FieldRequestBody), sqljson.Path("reasoning_effort")),
					sqljson.HasKey(q.C(requestexecution.FieldRequestBody), sqljson.Path("thinking")),
					sqljson.HasKey(q.C(requestexecution.FieldRequestBody), sqljson.Path("output_config", "effort")),
					sqljson.HasKey(q.C(requestexecution.FieldRequestBody), sqljson.Path("generationConfig", "thinkingConfig")),
					sqljson.HasKey(q.C(requestexecution.FieldRequestBody), sqljson.Path("request", "generationConfig", "thinkingConfig")),
					sql.And(sql.In(q.C(requestexecution.FieldFormat), string(llm.APIFormatOpenAIResponse), string(llm.APIFormatOpenAIResponseWebSocket)), sql.Or(
						sqljson.HasKey(q.C(requestexecution.FieldResponseBody), sqljson.Path("reasoning", "effort")),
						sqljson.HasKey(q.C(requestexecution.FieldResponseChunks), sqljson.Path("[0]", "event"))))))
			}).Select(requestexecution.FieldID, requestexecution.FieldFormat, requestexecution.FieldRequestBody, requestexecution.FieldResponseBody, requestexecution.FieldResponseChunks).
			Order(ent.Asc(requestexecution.FieldID)).Limit(8).All(ctx)
		if err != nil || len(rows) == 0 {
			return err
		}
		for _, row := range rows {
			lastID = row.ID
			format := llm.APIFormat(row.Format)
			e := &objects.RequestReasoningEvidence{}
			if requested := requestreasoning.Requested(row.RequestBody, format); requested != nil {
				e.Requested = *requested
			}
			if responses.IsResponsesFormat(format) {
				e.Reported = responses.ReportedReasoningEffort(row.ResponseBody, "", false)
			}
			for i := len(row.ResponseChunks) - 1; responses.IsResponsesFormat(format) && e.Reported == "" && i >= 0; i-- {
				data := gjson.GetBytes(row.ResponseChunks[i], "data")
				e.Reported = responses.ReportedReasoningEffort([]byte(data.Raw), gjson.GetBytes(row.ResponseChunks[i], "event").String(), true)
			}
			if e.Reported == "" && e.Requested == "" {
				continue
			}
			if err := db.RequestExecution.Update().Where(requestexecution.IDEQ(row.ID), requestexecution.ReasoningEvidenceIsNil()).SetReasoningEvidence(e).
				Modify(func(q *sql.UpdateBuilder) {
					q.Set(requestexecution.FieldUpdatedAt, sql.Expr(requestexecution.FieldUpdatedAt))
				}).Exec(ctx); err != nil {
				return err
			}
		}
	}
}
