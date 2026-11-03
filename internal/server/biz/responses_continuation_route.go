package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/responsesorigin"
	"github.com/looplj/axonhub/llm"
	"github.com/tidwall/gjson"
)

var ErrResponsesOriginConflict = errors.New("Responses history contains incompatible origins")

// ResponsesContinuationRoute identifies the execution that produced the actual
// history. A trace or thread's latest request is never evidence of provenance.
type ResponsesContinuationRoute struct {
	ChannelID       int
	KeyRef          string
	KeyHint         string
	UpstreamRef     string
	UnavailableCode string
}

func responsesDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// ResponsesUpstreamRef identifies the endpoint and account, not rotating OAuth tokens.
func ResponsesUpstreamRef(address string, headers http.Header) string {
	u, err := url.Parse(address)
	if err != nil {
		return ""
	}
	u.Scheme = strings.Replace(u.Scheme, "ws", "http", 1)
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/compact")
	u.RawPath = ""
	return responsesDigest(u.String() + "\x00" + headers.Get("Chatgpt-Account-Id") + "\x00" + headers.Get("OpenAI-Organization") + "\x00" + headers.Get("OpenAI-Project"))
}

// RecordResponsesOrigin retains completed items even when generation is canceled.
// Body-storage switches and debug expiry do not affect this credential-free metadata.
func (s *RequestService) RecordResponsesOrigin(ctx context.Context, execution *ent.RequestExecution, requestBody, responseBody []byte) error {
	if execution == nil || execution.Selection == nil {
		return nil
	}
	projectID, apiKeyID, ok := responsesOriginScope(ctx)
	if !ok {
		return nil
	}
	status := gjson.GetBytes(responseBody, "status").String()
	compact := gjson.GetBytes(responseBody, "object").String() == "response.compaction"
	// Successful non-stream responses and terminal events may omit status,
	// as accepted by the Responses transformer. Explicit unfinished/failed
	// responses must still be excluded.
	if status != "" && status != "completed" && status != "incomplete" && !compact {
		return nil
	}
	input := map[string]bool{}
	for _, item := range gjson.GetBytes(requestBody, "input").Array() {
		if value := item.Get("encrypted_content").String(); value != "" {
			input[value] = true
		}
	}
	fingerprints := map[string]bool{}
	if id := gjson.GetBytes(responseBody, "id").String(); id != "" {
		fingerprints[responsesDigest("response:"+id)] = true
	}
	for _, item := range gjson.GetBytes(responseBody, "output").Array() {
		value, itemStatus := item.Get("encrypted_content").String(), item.Get("status").String()
		if value != "" && !input[value] && (itemStatus == "" || itemStatus == "completed") {
			fingerprints[responsesDigest("encrypted:"+value)] = true
		}
	}
	selection := execution.Selection
	// OAuth's masked access token changes on refresh; the account identity is
	// already in UpstreamRef. API credentials use their full irreversible ref.
	keyHint := selection.KeyHint
	if selection.KeyRef == "" {
		keyHint = ""
	}
	originRef := responsesDigest(fmt.Sprintf("%d:%s:%s", execution.ChannelID, selection.KeyRef, selection.UpstreamRef))
	client := s.entFromContext(ctx)
	for fingerprint := range fingerprints {
		if err := client.ResponsesOrigin.Create().SetProjectID(projectID).SetAPIKeyID(apiKeyID).
			SetFingerprint(fingerprint).SetOriginRef(originRef).SetChannelID(execution.ChannelID).
			SetKeyRef(selection.KeyRef).SetKeyHint(keyHint).SetUpstreamRef(selection.UpstreamRef).
			SetRequestID(execution.RequestID).SetExecutionID(execution.ID).
			OnConflictColumns(responsesorigin.FieldProjectID, responsesorigin.FieldAPIKeyID, responsesorigin.FieldFingerprint, responsesorigin.FieldOriginRef).Ignore().Exec(ctx); err != nil {
			return fmt.Errorf("save Responses origin: %w", err)
		}
	}
	return nil
}

func responsesOriginScope(ctx context.Context) (int, int, bool) {
	projectID, ok := contexts.GetProjectID(ctx)
	apiKeyID := 0
	if key, found := contexts.GetAPIKey(ctx); found && key != nil {
		apiKeyID = key.ID
	}
	return projectID, apiKeyID, ok
}

func (s *RequestService) GetResponsesContinuationRoute(ctx context.Context, previousResponseID string, encryptedContents ...string) (ResponsesContinuationRoute, bool, error) {
	projectID, apiKeyID, ok := responsesOriginScope(ctx)
	if !ok {
		return ResponsesContinuationRoute{}, false, nil
	}
	fingerprints := make([]string, 0, len(encryptedContents)+1)
	if previousResponseID != "" {
		fingerprints = append(fingerprints, responsesDigest("response:"+previousResponseID))
	}
	for _, value := range encryptedContents {
		if value != "" {
			fingerprints = append(fingerprints, responsesDigest("encrypted:"+value))
		}
	}
	var result ResponsesContinuationRoute
	found := false
	merge := func(route ResponsesContinuationRoute) error {
		if found && (route.ChannelID != result.ChannelID ||
			route.KeyRef != "" && result.KeyRef != "" && route.KeyRef != result.KeyRef ||
			(route.KeyRef == "" || result.KeyRef == "") && route.KeyHint != "" && result.KeyHint != "" && route.KeyHint != result.KeyHint ||
			route.UpstreamRef != "" && result.UpstreamRef != "" && route.UpstreamRef != result.UpstreamRef) {
			return ErrResponsesOriginConflict
		}
		result.ChannelID, found = route.ChannelID, true
		if result.KeyRef == "" {
			result.KeyRef = route.KeyRef
		}
		if result.KeyHint == "" {
			result.KeyHint = route.KeyHint
		}
		if result.UpstreamRef == "" {
			result.UpstreamRef = route.UpstreamRef
		}
		return nil
	}
	client := s.entFromContext(ctx)
	previousFound := false
	// Long imported histories must also work within SQLite's parameter limit.
	for start := 0; start < len(fingerprints); start += 500 {
		rows, err := client.ResponsesOrigin.Query().Where(responsesorigin.ProjectIDEQ(projectID),
			responsesorigin.APIKeyIDEQ(apiKeyID), responsesorigin.FingerprintIn(fingerprints[start:min(start+500, len(fingerprints))]...)).All(ctx)
		if err != nil {
			return result, found, fmt.Errorf("load Responses origin: %w", err)
		}
		for _, row := range rows {
			previousFound = previousFound || previousResponseID != "" && row.Fingerprint == fingerprints[0]
			if err := merge(ResponsesContinuationRoute{ChannelID: row.ChannelID, KeyRef: row.KeyRef, KeyHint: row.KeyHint, UpstreamRef: row.UpstreamRef}); err != nil {
				return result, found, err
			}
		}
	}
	// Released versions retained exact response IDs and execution credentials.
	// This is the only legacy evidence accepted; never infer from time or trace.
	if previousResponseID != "" && !previousFound {
		route, exists, err := s.legacyResponsesOrigin(ctx, projectID, apiKeyID, previousResponseID)
		if err != nil {
			return result, found, err
		}
		if exists {
			if err := merge(route); err != nil {
				return result, found, err
			}
		}
	}
	if found {
		source, err := client.Channel.Query().Where(channel.IDEQ(result.ChannelID)).Select(channel.FieldStatus).Only(ctx)
		switch {
		case ent.IsNotFound(err):
			result.UnavailableCode = "continuation_channel_missing"
		case err != nil:
			return result, found, err
		case source.Status != channel.StatusEnabled:
			result.UnavailableCode = "continuation_channel_disabled"
		}
	}
	return result, found, nil
}

func (s *RequestService) legacyResponsesOrigin(ctx context.Context, projectID, apiKeyID int, responseID string) (ResponsesContinuationRoute, bool, error) {
	query := s.entFromContext(ctx).Request.Query().Where(request.ProjectIDEQ(projectID), request.ExternalIDEQ(responseID),
		request.StatusEQ(request.StatusCompleted), request.ChannelIDNEQ(0),
		request.FormatIn(string(llm.APIFormatOpenAIResponse), string(llm.APIFormatOpenAIResponseWebSocket), string(llm.APIFormatOpenAIResponseCompact)))
	if apiKeyID == 0 {
		query.Where(request.APIKeyIDIsNil())
	} else {
		query.Where(request.APIKeyIDEQ(apiKeyID))
	}
	req, err := query.Only(ctx)
	if ent.IsNotFound(err) {
		return ResponsesContinuationRoute{}, false, nil
	}
	if ent.IsNotSingular(err) {
		return ResponsesContinuationRoute{}, false, ErrResponsesOriginConflict
	}
	if err != nil {
		return ResponsesContinuationRoute{}, false, err
	}
	route := ResponsesContinuationRoute{ChannelID: req.ChannelID}
	execution, err := s.entFromContext(ctx).RequestExecution.Query().Where(requestexecution.RequestIDEQ(req.ID),
		requestexecution.ChannelIDEQ(req.ChannelID), requestexecution.StatusEQ(requestexecution.StatusCompleted)).Order(ent.Desc(requestexecution.FieldID)).First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return route, false, err
	}
	if execution != nil && execution.Selection != nil {
		route.KeyRef, route.KeyHint, route.UpstreamRef = execution.Selection.KeyRef, execution.Selection.KeyHint, execution.Selection.UpstreamRef
	}
	return route, true, nil
}
