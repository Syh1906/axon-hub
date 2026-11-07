//nolint:nilerr // Checked.
package biz

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eko/gocache/lib/v4/store"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/metrics"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/usageanalytics"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/pkg/xjson"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// RequestService handles request and request execution operations.
type RequestService struct {
	*AbstractService

	SystemService        *SystemService
	UsageLogService      *UsageLogService
	DataStorageService   *DataStorageService
	LiveStreamRegistry   *LiveStreamRegistry
	PayloadService       *RequestPayloadService
	previousChannelCache xcache.Cache[int]
	sessionOwnerCache    xcache.Cache[SessionOwner]
	metricClaims         *requestMetricClaims
}

// recordedMetricRequestsTTL bounds how long a claimed request ID is kept in
// the chat metrics deduplication.
const recordedMetricRequestsTTL = time.Hour

// NewRequestService creates a new RequestService.
func NewRequestService(
	ent *ent.Client,
	cacheConfig xcache.Config,
	systemService *SystemService,
	usageLogService *UsageLogService,
	dataStorageService *DataStorageService,
	liveStreamRegistry *LiveStreamRegistry,
	payloadService *RequestPayloadService,
) *RequestService {
	return &RequestService{
		AbstractService: &AbstractService{
			db: ent,
		},
		SystemService:        systemService,
		UsageLogService:      usageLogService,
		DataStorageService:   dataStorageService,
		LiveStreamRegistry:   liveStreamRegistry,
		PayloadService:       payloadService,
		previousChannelCache: xcache.NewFromConfig[int](cacheConfig),
		sessionOwnerCache:    newSessionOwnerCache(cacheConfig),
		metricClaims:         newRequestMetricClaims(),
	}
}

// shouldUseExternalStorage checks if data should be saved to external storage.
// Returns true if the data storage is not primary (database).
func (s *RequestService) shouldUseExternalStorage(_ context.Context, ds *ent.DataStorage) bool {
	if ds == nil {
		return false
	}

	return !ds.Primary
}

// jsonNulEscape is the JSON escape of U+0000. PostgreSQL jsonb rejects it
// (SQLSTATE 22P05), which would otherwise lose the whole payload.
var jsonNulEscape = []byte(`\u0000`)

// stripJSONNul removes U+0000 escapes from a JSON document so it can be stored
// in a jsonb column. An escaped backslash followed by "u0000" is plain text and
// is kept. Documents without the escape are returned as is.
func stripJSONNul(body objects.JSONRawMessage) objects.JSONRawMessage {
	if !bytes.Contains(body, jsonNulEscape) {
		return body
	}

	out := make(objects.JSONRawMessage, 0, len(body))

	for i := 0; i < len(body); {
		if body[i] == '\\' && i+1 < len(body) {
			if bytes.HasPrefix(body[i:], jsonNulEscape) {
				i += len(jsonNulEscape)
				continue
			}

			// Copy the escape pair as a unit so `\\u0000` is not mistaken for a NUL.
			out = append(out, body[i], body[i+1])
			i += 2

			continue
		}

		out = append(out, body[i])
		i++
	}

	return out
}

// External payload markers live in the DB when the real JSON is in object storage.
// They must not match the GC strip placeholders (`{}` / `[]`).
var (
	ExternalResponseBodyMarker   = objects.JSONRawMessage(`{"_ext":1}`)
	ExternalResponseChunksMarker = []objects.JSONRawMessage{objects.JSONRawMessage(`{"_ext":1}`)}
)

func isExternalResponseBodyMarker(body objects.JSONRawMessage) bool {
	var compact bytes.Buffer
	if err := json.Compact(&compact, body); err != nil {
		return false
	}

	return bytes.Equal(compact.Bytes(), ExternalResponseBodyMarker)
}

func isExternalResponseChunksMarker(chunks []objects.JSONRawMessage) bool {
	return len(chunks) == 1 && isExternalResponseBodyMarker(chunks[0])
}

func sanitizeLoadedResponseBody(body objects.JSONRawMessage) objects.JSONRawMessage {
	if len(body) == 0 || isExternalResponseBodyMarker(body) {
		return xjson.EmptyJSONRawMessage
	}

	return body
}

func sanitizeLoadedResponseChunks(chunks []objects.JSONRawMessage) []objects.JSONRawMessage {
	if len(chunks) == 0 || isExternalResponseChunksMarker(chunks) {
		return []objects.JSONRawMessage{}
	}

	return chunks
}

// GenerateRequestBodyKey generates the storage key for request body.
func GenerateRequestBodyKey(projectID, requestID int) string {
	return fmt.Sprintf("/%d/requests/%d/request_body.json", projectID, requestID)
}

// GenerateResponseBodyKey generates the storage key for response body.
func GenerateResponseBodyKey(projectID, requestID int) string {
	return fmt.Sprintf("/%d/requests/%d/response_body.json", projectID, requestID)
}

// GenerateAudioKey generates the storage key for a generated audio file (TTS).
func GenerateAudioKey(projectID, requestID int, filename string) string {
	name := strings.TrimSpace(filename)
	if name == "" {
		name = "audio.mp3"
	}

	name = filepath.Base(name)

	return fmt.Sprintf("/%d/requests/%d/audio/%s", projectID, requestID, name)
}

// GenerateResponseChunksKey generates the storage key for response chunks.
func GenerateResponseChunksKey(projectID, requestID int) string {
	return fmt.Sprintf("/%d/requests/%d/response_chunks.json", projectID, requestID)
}

// GenerateRequestDirKey generates the storage key for request.
func GenerateRequestDirKey(projectID, requestID int) string {
	return fmt.Sprintf("/%d/requests/%d", projectID, requestID)
}

func GenerateRequestExecutionsDirKey(projectID, requestID int) string {
	return fmt.Sprintf("/%d/requests/%d/executions", projectID, requestID)
}

// GenerateExecutionRequestBodyKey generates the storage key for execution request body.
func GenerateExecutionRequestBodyKey(projectID, requestID, executionID int) string {
	return fmt.Sprintf("/%d/requests/%d/executions/%d/request_body.json", projectID, requestID, executionID)
}

// GenerateExecutionResponseBodyKey generates the storage key for execution response body.
func GenerateExecutionResponseBodyKey(projectID, requestID, executionID int) string {
	return fmt.Sprintf("/%d/requests/%d/executions/%d/response_body.json", projectID, requestID, executionID)
}

// GenerateExecutionResponseChunksKey generates the storage key for execution response chunks.
func GenerateExecutionResponseChunksKey(projectID, requestID, executionID int) string {
	return fmt.Sprintf("/%d/requests/%d/executions/%d/response_chunks.json", projectID, requestID, executionID)
}

// GenerateExecutionRequestDirKey generates the storage key for execution request.
func GenerateExecutionRequestDirKey(projectID, requestID, executionID int) string {
	return fmt.Sprintf("/%d/requests/%d/executions/%d", projectID, requestID, executionID)
}

// CreateRequest creates a new request record.
func (s *RequestService) CreateRequest(
	ctx context.Context,
	llmRequest *llm.Request,
	httpRequest *httpclient.Request,
	format llm.APIFormat,
) (*ent.Request, error) {
	// Get project ID from context.
	// If project ID is not found, use zero.
	// It will be not prsent in the admin pages,
	// e.g: test channel.
	projectID, _ := contexts.GetProjectID(ctx)

	// Decide whether to store the original request body
	storeRequestBody := false
	if policy, err := s.SystemService.StoragePolicy(ctx); err == nil {
		storeRequestBody = policy.StoreRequestBody
	} else {
		log.Warn(ctx, "Failed to get storage policy, request body will not be retained", log.Cause(err))
	}

	var (
		requestBodyBytes    objects.JSONRawMessage = []byte("{}")
		requestHeadersBytes objects.JSONRawMessage = []byte("{}")
	)

	if storeRequestBody {
		if len(httpRequest.JSONBody) > 0 {
			requestBodyBytes = httpRequest.JSONBody
		} else {
			b, err := xjson.Marshal(httpRequest.Body)
			if err != nil {
				log.Error(ctx, "Failed to serialize request body", log.Cause(err))
				return nil, err
			}

			requestBodyBytes = b
		}

		if httpRequest != nil && len(httpRequest.Headers) > 0 {
			requestHeadersBytes, _ = xjson.Marshal(httpclient.MaskSensitiveHeaders(httpRequest.Headers))
		}
	}

	isStream := false
	if llmRequest.Stream != nil {
		isStream = *llmRequest.Stream
	}

	// Get default data storage
	dataStorage, err := s.DataStorageService.GetDefaultDataStorage(ctx)
	if err != nil {
		log.Warn(ctx, "Failed to get default data storage, request will be created without data storage", log.Cause(err))
	}

	client := s.entFromContext(ctx)
	mut := client.Request.Create().
		SetProjectID(projectID).
		SetModelID(llmRequest.Model).
		SetFormat(string(format)).
		SetSource(contexts.GetSourceOrDefault(ctx, request.SourceAPI)).
		SetStatus(request.StatusProcessing).
		SetStream(isStream).
		SetRequestHeaders(requestHeadersBytes)

	if httpRequest != nil {
		mut = mut.SetClientIP(httpRequest.ClientIP)
		mut = mut.SetUserAgent(httpRequest.UserAgent)
	}

	if llmRequest.ReasoningEffort != "" {
		mut = mut.SetReasoningEffort(llmRequest.ReasoningEffort)
	}

	if storeRequestBody {
		requestBodyBytes = s.PayloadService.saveRequestDebug(ctx, requestBodyBytes)
	} else {
		requestBodyBytes = payloadMarker(requestPayloadReference{State: "disabled"})
	}
	mut.SetRequestBody(requestBodyBytes)

	if dataStorage != nil {
		mut = mut.SetDataStorageID(dataStorage.ID)
	}

	if apiKey, ok := contexts.GetAPIKey(ctx); ok && apiKey != nil {
		mut = mut.SetAPIKeyID(apiKey.ID)
	}

	if trace, ok := contexts.GetTrace(ctx); ok && trace != nil {
		mut = mut.SetTraceID(trace.ID)
	}

	if contexts.GetSourceOrDefault(ctx, request.SourceAPI) == request.SourcePlayground {
		if user, ok := contexts.GetUser(ctx); ok && user != nil {
			mut = mut.SetUserID(user.ID)
		}
	}

	// Create request
	req, err := mut.Save(ctx)
	if err != nil {
		return nil, err
	}

	// Let the downstream response-header middleware associate the eventual
	// HTTP response with this persisted request row.
	contexts.NotifyRequestRecord(ctx, req.ID)

	return req, nil
}

// CreateRequestExecution creates a new request execution record.
func (s *RequestService) CreateRequestExecution(
	ctx context.Context,
	channel *Channel,
	modelID string,
	request *ent.Request,
	channelRequest httpclient.Request,
	format llm.APIFormat,
	passThroughApplied bool,
	cacheSample *usageanalytics.CacheSample,
	selection *objects.RequestExecutionSelection,
	modelCard *objects.ModelCard,
	serviceTier ...*objects.ServiceTierEvidence,
) (*ent.RequestExecution, error) {
	// Decide whether to store the channel request body
	storeRequestBody := false
	if policy, err := s.SystemService.StoragePolicy(ctx); err == nil {
		storeRequestBody = policy.StoreRequestBody
	} else {
		log.Warn(ctx, "Failed to get storage policy, request body will not be retained", log.Cause(err))
	}

	var (
		requestBodyBytes    objects.JSONRawMessage = []byte("{}")
		requestHeadersBytes objects.JSONRawMessage = []byte("{}")
	)

	if storeRequestBody {
		if len(channelRequest.JSONBody) > 0 {
			requestBodyBytes = channelRequest.JSONBody
		} else {
			b, err := xjson.Marshal(channelRequest.Body)
			if err != nil {
				log.Error(ctx, "Failed to marshal request body", log.Cause(err))
				return nil, err
			}

			requestBodyBytes = b
		}

		if len(channelRequest.Headers) > 0 {
			requestHeadersBytes, _ = xjson.Marshal(httpclient.MaskSensitiveHeaders(channelRequest.Headers))
		}
	}

	client := s.entFromContext(ctx)

	requestBodyForDB := payloadMarker(requestPayloadReference{State: "disabled"})
	if storeRequestBody {
		requestBodyForDB = s.PayloadService.saveRequestDebug(ctx, requestBodyBytes)
	}

	costSnapshot := captureRequestCostSnapshot(ctx, client, channel, modelID, modelCard)
	if len(serviceTier) > 0 && serviceTier[0] != nil {
		evidence := *serviceTier[0]
		if channel.Settings != nil && channel.Settings.ServiceTierBilling != nil {
			evidence.Settings = *channel.Settings.ServiceTierBilling
		}
		costSnapshot.ServiceTier = &evidence
	}
	mut := client.RequestExecution.Create().
		SetFormat(string(format)).
		SetRequestID(request.ID).
		SetProjectID(request.ProjectID).
		SetChannelID(channel.ID).
		SetModelID(modelID).
		SetRequestBody(requestBodyForDB).
		SetStatus(requestexecution.StatusProcessing).
		SetStream(request.Stream).
		SetRequestHeaders(requestHeadersBytes).
		SetCostSnapshot(costSnapshot).
		SetPassThroughApplied(passThroughApplied)
	if selection != nil {
		mut.SetSelection(selection)
	}

	if cacheSample != nil {
		mut.SetCacheCompatibilityKey(cacheSample.CompatibilityKey).
			SetTheoreticalCacheTokens(cacheSample.TheoreticalTokens).
			SetCacheTTLBucket(cacheSample.TTLBucket)
	}

	if reasoningEffort := extractOutboundReasoningEffort(channelRequest, format); reasoningEffort != nil {
		mut = mut.SetReasoningEffort(*reasoningEffort)
	}

	// Record which channel credential served this execution. Both facts are read
	// from the key actually sent upstream, which is known here while the
	// credentials are still at hand.
	if apiKey, ok := contexts.GetChannelAPIKey(ctx); ok {
		runes := []rune(apiKey)
		if len(runes) > 4 {
			mut = mut.SetChannelAPIKeySuffix(string(runes[len(runes)-4:]))
		}

		// The 1-based position is derived here rather than resolved from the
		// stored suffix later: a lookup would drift as soon as keys are
		// reordered or removed, and it cannot tell two keys with the same last-4
		// characters apart. Single-key and OAuth channels have nothing to
		// disambiguate, so they stay null.
		if allKeys := channel.Credentials.GetAllAPIKeys(); len(allKeys) > 1 {
			if idx := slices.Index(allKeys, apiKey); idx >= 0 {
				mut = mut.SetChannelAPIKeyIndex(idx + 1)
			}
		}
	}

	if channelRequest.URL != "" {
		mut = mut.SetRequestURL(diagnosticRequestURL(channelRequest.URL))
	}

	// Use the same data storage as the request
	if request.DataStorageID != 0 {
		mut = mut.SetDataStorageID(request.DataStorageID)
	}

	execution, err := mut.Save(ctx)
	if err != nil {
		return nil, err
	}

	return execution, nil
}

// extractOutboundReasoningEffort returns the reasoning effort from the final
// request body that will be sent to the upstream provider.
func extractOutboundReasoningEffort(channelRequest httpclient.Request, format llm.APIFormat) *string {
	var path string

	switch format {
	case llm.APIFormatOpenAIChatCompletion:
		path = "reasoning_effort"
	case llm.APIFormatOpenAIResponse, llm.APIFormatOpenAIResponseWebSocket, llm.APIFormatOpenAIResponseCompact:
		path = "reasoning.effort"
	case llm.APIFormatAnthropicMessage:
		path = "output_config.effort"
	default:
		return nil
	}

	result := gjson.GetBytes(channelRequest.Body, path)
	if result.Type != gjson.String || result.String() == "" {
		return nil
	}

	effort := result.String()

	return &effort
}

// LatencyMetrics holds latency metrics for a request.
type LatencyMetrics struct {
	LatencyMs           *int64
	FirstTokenLatencyMs *int64
	FirstByteLatencyMs  *int64
	ReasoningDurationMs *int64
}

func (s *RequestService) UpdateRequestResponseHeaders(ctx context.Context, requestID int, headers http.Header) error {
	data, err := s.responseHeadersForStorage(ctx, headers)
	if err != nil || data == nil {
		return err
	}
	_, err = s.entFromContext(ctx).Request.UpdateOneID(requestID).SetResponseHeaders(data).Save(ctx)
	return err
}

func (s *RequestService) UpdateRequestExecutionResponseHeaders(ctx context.Context, executionID int, headers http.Header) error {
	data, err := s.responseHeadersForStorage(ctx, headers)
	if err != nil || data == nil {
		return err
	}
	_, err = s.entFromContext(ctx).RequestExecution.UpdateOneID(executionID).SetResponseHeaders(data).Save(ctx)
	return err
}

func (s *RequestService) responseHeadersForStorage(ctx context.Context, headers http.Header) (objects.JSONRawMessage, error) {
	if len(headers) == 0 {
		return nil, nil
	}
	policy, err := s.SystemService.StoragePolicy(authz.WithSystemBypass(ctx, "response-header-storage-policy"))
	if err == nil && !policy.StoreResponseBody {
		return nil, nil
	}
	return xjson.Marshal(httpclient.MaskSensitiveHeaders(headers))
}

// UpdateRequestFinalized persists a terminal response and its final request status.
func (s *RequestService) UpdateRequestFinalized(
	ctx context.Context,
	requestID int,
	status request.Status,
	externalId string,
	responseBody any,
	metrics *LatencyMetrics,
) error {
	client := s.entFromContext(ctx)
	req := s.requestMetricSnapshot(ctx, client, requestID)

	// A downstream delivery failure may be recorded while the upstream stream
	// is still draining. Late success must not replace that terminal outcome.
	upd := client.Request.Update().Where(request.IDEQ(requestID), request.FailureIsNil()).
		SetStatus(status).
		SetExternalID(externalId)

	// Set latency metrics if provided
	if metrics != nil {
		if metrics.LatencyMs != nil {
			upd = upd.SetMetricsLatencyMs(*metrics.LatencyMs)
		}

		if metrics.FirstTokenLatencyMs != nil {
			upd = upd.SetMetricsFirstTokenLatencyMs(*metrics.FirstTokenLatencyMs)
		}

		if metrics.ReasoningDurationMs != nil {
			upd = upd.SetMetricsReasoningDurationMs(*metrics.ReasoningDurationMs)
		}
	}

	upd.SetResponseBody(s.debugResponsePayload(ctx, responseBody))

	updated, err := upd.Save(ctx)
	if err != nil {
		log.Error(ctx, "Failed to update request status to completed", log.Cause(err))
		return err
	}

	if updated > 0 { s.recordChatRequestMetrics(ctx, req, status, metrics) }

	return nil
}

// recordChatRequestMetrics reports the transition of a request into a
// terminal state to the metrics registry. It is a no-op when metrics are
// disabled, the target status is not terminal, or the request was already
// terminal before the update: status refreshes that re-save an already
// terminal outcome (e.g. repeated video task polls) and marks that follow an
// earlier terminal outcome must not be counted again. req must reflect the
// pre-update status. Recording is best-effort: channel name resolution and
// metric recording must never affect request finalization.
func (s *RequestService) recordChatRequestMetrics(ctx context.Context, req *ent.Request, status request.Status, latency *LatencyMetrics) {
	if req == nil || !metrics.Enabled() || !isTerminalRequestStatus(status) || isTerminalRequestStatus(req.Status) {
		return
	}

	// Concurrent terminal setters can both observe the pre-update status;
	// the claim ensures only one of them records.
	if !s.metricClaims.claim(req.ID) {
		return
	}

	channelID := ""
	channelName := ""

	if req.ChannelID != 0 {
		channelID = strconv.Itoa(req.ChannelID)

		// Best-effort lookup: a deleted channel must not break finalization.
		if channel, err := req.QueryChannel().Only(ctx); err == nil {
			channelName = channel.Name
		}
	}

	duration := 0.0
	if latency != nil && latency.LatencyMs != nil {
		duration = float64(*latency.LatencyMs) / 1000
	} else if !req.CreatedAt.IsZero() {
		duration = time.Since(req.CreatedAt).Seconds()
	}

	metrics.Metrics.RecordChatRequest(ctx, channelID, channelName, duration, status == request.StatusCompleted)
}

// requestMetricClaims atomically deduplicates chat metric recording per
// request: exactly one of several concurrent or sequential terminal setters
// wins the claim for a request ID. Claims expire so the map stays bounded
// without a background goroutine.
type requestMetricClaims struct {
	mu        sync.Mutex
	claimed   map[int]time.Time
	lastPrune time.Time
}

func newRequestMetricClaims() *requestMetricClaims {
	return &requestMetricClaims{
		claimed: map[int]time.Time{},
	}
}

// claim reports whether the caller won the exclusive right to record the
// request, recording the claim when it does.
func (c *requestMetricClaims) claim(requestID int) bool {
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Prune expired claims at most once per TTL so the common path stays
	// O(1) while the map is bounded to roughly one TTL of requests.
	if now.Sub(c.lastPrune) > recordedMetricRequestsTTL {
		for id, at := range c.claimed {
			if now.Sub(at) > recordedMetricRequestsTTL {
				delete(c.claimed, id)
			}
		}

		c.lastPrune = now
	}

	if _, ok := c.claimed[requestID]; ok {
		return false
	}

	c.claimed[requestID] = now

	return true
}

// isTerminalRequestStatus reports whether the status is a terminal request
// outcome that should be counted in chat metrics.
func isTerminalRequestStatus(status request.Status) bool {
	switch status {
	case request.StatusCompleted, request.StatusFailed, request.StatusCanceled:
		return true
	default:
		return false
	}
}

// UpdateRequestCompletedWithAudio marks a request completed and persists a binary audio
// payload (TTS) to external storage when configured.
//
// The audio bytes are never stored in the database column: responseBody carries a compact
// metadata placeholder, and the raw audio is saved to the request's external DataStorage
// (when one is configured and non-primary), tracked via the content_storage_* fields,
// mirroring how video artifacts are stored.
func (s *RequestService) UpdateRequestCompletedWithAudio(
	ctx context.Context,
	requestID int,
	externalId string,
	responseBody any,
	audio []byte,
	filename string,
	metrics *LatencyMetrics,
) error {
	client := s.entFromContext(ctx)

	req, err := client.Request.Get(ctx, requestID)
	if err != nil {
		log.Error(ctx, "Failed to get request", log.Cause(err))
		return err
	}

	var dataStorage *ent.DataStorage
	if req.DataStorageID != 0 {
		dataStorage, err = s.DataStorageService.GetDataStorageByID(ctx, req.DataStorageID)
		if err != nil {
			log.Warn(ctx, "Failed to get data storage", log.Cause(err))
		}
	}

	upd := client.Request.UpdateOneID(requestID).
		SetStatus(request.StatusCompleted).
		SetExternalID(externalId)

	if metrics != nil {
		if metrics.LatencyMs != nil {
			upd = upd.SetMetricsLatencyMs(*metrics.LatencyMs)
		}

		if metrics.FirstTokenLatencyMs != nil {
			upd = upd.SetMetricsFirstTokenLatencyMs(*metrics.FirstTokenLatencyMs)
		}

		if metrics.ReasoningDurationMs != nil {
			upd = upd.SetMetricsReasoningDurationMs(*metrics.ReasoningDurationMs)
		}
	}

	upd.SetResponseBody(s.debugResponsePayload(ctx, responseBody))

	// Persist the binary audio to external storage when one is configured.
	if len(audio) > 0 && s.shouldUseExternalStorage(ctx, dataStorage) {
		key := GenerateAudioKey(req.ProjectID, requestID, filename)
		if err := s.DataStorageService.SaveData(ctx, dataStorage, key, audio); err != nil {
			log.Error(ctx, "Failed to save audio to external storage", log.Cause(err))
		} else {
			upd = upd.
				SetContentSaved(true).
				SetContentStorageID(dataStorage.ID).
				SetContentStorageKey(key).
				SetContentSavedAt(time.Now().UTC())
		}
	}

	_, err = upd.Save(ctx)
	if err != nil {
		log.Error(ctx, "Failed to update audio request status to completed", log.Cause(err))
		return err
	}

	s.recordChatRequestMetrics(ctx, req, request.StatusCompleted, metrics)

	return nil
}

// UpdateRequestStatusExternalIDAndResponseBody updates request status/external_id and optionally persists response body.
// It is intended for non-pipeline async task flows where task status is polled later.
func (s *RequestService) UpdateRequestStatusExternalIDAndResponseBody(
	ctx context.Context,
	requestID int,
	status request.Status,
	externalId string,
	responseBody any,
	metrics *LatencyMetrics,
) error {
	client := s.entFromContext(ctx)
	req := s.requestMetricSnapshot(ctx, client, requestID)

	upd := client.Request.UpdateOneID(requestID).
		Select(request.FieldID).
		SetStatus(status).
		SetExternalID(externalId)

	// Set latency metrics if provided
	if metrics != nil {
		if metrics.LatencyMs != nil {
			upd = upd.SetMetricsLatencyMs(*metrics.LatencyMs)
		}

		if metrics.FirstTokenLatencyMs != nil {
			upd = upd.SetMetricsFirstTokenLatencyMs(*metrics.FirstTokenLatencyMs)
		}

		if metrics.ReasoningDurationMs != nil {
			upd = upd.SetMetricsReasoningDurationMs(*metrics.ReasoningDurationMs)
		}
	}

	upd.SetResponseBody(s.debugResponsePayload(ctx, responseBody))

	_, err := upd.Save(ctx)
	if err != nil {
		log.Error(ctx, "Failed to update request status", log.Cause(err))
		return err
	}

	s.recordChatRequestMetrics(ctx, req, status, metrics)

	return nil
}

// UpdateRequestExecutionFinalized persists a terminal response and its final execution status.
func (s *RequestService) UpdateRequestExecutionFinalized(
	ctx context.Context,
	executionID int,
	status requestexecution.Status,
	errorMessage string,
	externalId string,
	responseBody any,
	metrics *LatencyMetrics,
	upstreamModelID string,
) error {
	client := s.entFromContext(ctx)

	upd := client.RequestExecution.UpdateOneID(executionID).
		SetStatus(status).
		SetExternalID(externalId)
	if metrics != nil {
		upd.SetNillableMetricsFirstByteLatencyMs(metrics.FirstByteLatencyMs).SetMetricsFirstByteRecorded(true)
	}

	if upstreamModelID != "" {
		upd = upd.SetUpstreamModelID(upstreamModelID)
	}
	if errorMessage != "" {
		upd = upd.SetErrorMessage(errorMessage)
	}

	// Set latency metrics if provided
	if metrics != nil {
		if metrics.LatencyMs != nil {
			upd = upd.SetMetricsLatencyMs(*metrics.LatencyMs)
		}

		if metrics.FirstTokenLatencyMs != nil {
			upd = upd.SetMetricsFirstTokenLatencyMs(*metrics.FirstTokenLatencyMs)
		}

		if metrics.ReasoningDurationMs != nil {
			upd = upd.SetMetricsReasoningDurationMs(*metrics.ReasoningDurationMs)
		}
	}

	upd.SetResponseBody(s.debugResponsePayload(ctx, responseBody))

	_, err := upd.Save(ctx)
	if err != nil {
		log.Error(ctx, "Failed to update finalized request execution", log.Cause(err), log.Any("status", status))
		return err
	}

	return nil
}

// UpdateRequestExecutionCanceled updates request execution status to canceled with error message.
func (s *RequestService) UpdateRequestExecutionCanceled(
	ctx context.Context,
	executionID int,
	errorMsg string,
) error {
	return s.UpdateRequestExecutionStatus(ctx, executionID, requestexecution.StatusCanceled, errorMsg, nil)
}

// ExecutionErrorInfo holds error details for a failed request execution.
type ExecutionErrorInfo struct {
	StatusCode   *int
	Failure      *objects.RequestExecutionFailure
	ExternalID   string
	ResponseBody []byte
}

// UpdateRequestExecutionFailed updates request execution status to failed with error message and optional error details.
func (s *RequestService) UpdateRequestExecutionFailed(
	ctx context.Context,
	executionID int,
	errorMsg string,
	errorInfo *ExecutionErrorInfo,
) error {
	return s.UpdateRequestExecutionStatus(ctx, executionID, requestexecution.StatusFailed, errorMsg, errorInfo)
}

// UpdateRequestExecutionStatus updates request execution status to the provided value (e.g., canceled or failed), with optional error message.
func (s *RequestService) UpdateRequestExecutionStatus(
	ctx context.Context,
	executionID int,
	status requestexecution.Status,
	errorMsg string,
	errorInfo *ExecutionErrorInfo,
) error {
	return s.UpdateRequestExecutionStatusWithMetrics(ctx, executionID, status, errorMsg, errorInfo, nil, "")
}

// UpdateRequestExecutionStatusWithMetrics is UpdateRequestExecutionStatus plus the latency
// metrics and upstream models collected before the execution ended, so a failed
// execution keeps its metadata even when its response cannot be aggregated.
func (s *RequestService) UpdateRequestExecutionStatusWithMetrics(
	ctx context.Context,
	executionID int,
	status requestexecution.Status,
	errorMsg string,
	errorInfo *ExecutionErrorInfo,
	metrics *LatencyMetrics,
	upstreamModelID string,
) error {
	client := s.entFromContext(ctx)

	upd := client.RequestExecution.UpdateOneID(executionID).
		SetStatus(status)
	if metrics != nil {
		upd.SetNillableMetricsFirstByteLatencyMs(metrics.FirstByteLatencyMs).SetMetricsFirstByteRecorded(true)
	}
	// A later status-only update must not clear metadata captured at finalization.
	if upstreamModelID != "" {
		upd = upd.SetUpstreamModelID(upstreamModelID)
	}
	if errorMsg != "" {
		upd = upd.SetErrorMessage(errorMsg)
	}

	if errorInfo != nil && errorInfo.Failure != nil {
		failure := *errorInfo.Failure
		if failure.Body != "" {
			policy, err := s.SystemService.StoragePolicy(ctx)
			if err != nil {
				log.Warn(ctx, "Failed to read error body storage policy", log.Cause(err))
			}
			if err != nil || !policy.StoreResponseBody {
				failure.Body, failure.BodyTruncated = "", false
			}
		}
		// The raw-error middleware runs before the transformed-error finalizer.
		// Keep the original provider evidence when finalization writes metrics/status again.
		existing, err := client.RequestExecution.Query().Where(requestexecution.IDEQ(executionID)).Select(requestexecution.FieldFailure).Only(ctx)
		if err != nil {
			return err
		}
		if existing.Failure == nil {
			upd.SetFailure(&failure)
		}
	}
	if errorInfo != nil && errorInfo.StatusCode != nil && (errorInfo.Failure == nil || errorInfo.Failure.Source == "upstream_http") {
		upd = upd.SetResponseStatusCode(*errorInfo.StatusCode)
	}
	if errorInfo != nil && errorInfo.ResponseBody != nil {
		upd.SetResponseBody(s.debugResponsePayload(ctx, errorInfo.ResponseBody)).SetExternalID(errorInfo.ExternalID)
	}

	if metrics != nil {
		if metrics.LatencyMs != nil {
			upd = upd.SetMetricsLatencyMs(*metrics.LatencyMs)
		}

		if metrics.FirstTokenLatencyMs != nil {
			upd = upd.SetMetricsFirstTokenLatencyMs(*metrics.FirstTokenLatencyMs)
		}

		if metrics.ReasoningDurationMs != nil {
			upd = upd.SetMetricsReasoningDurationMs(*metrics.ReasoningDurationMs)
		}
	}

	_, err := upd.Save(ctx)
	if err != nil {
		log.Error(ctx, "Failed to update request execution status", log.Cause(err), log.Any("status", status))
		return err
	}

	return nil
}

// UpdateRequestExecutionStatusFromError updates request execution status based on error type and sets error message.
func (s *RequestService) UpdateRequestExecutionStatusFromError(ctx context.Context, executionID int, rawErr error) error {
	status := requestexecution.StatusFailed
	if errors.Is(rawErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		status = requestexecution.StatusCanceled
	}

	return s.UpdateRequestExecutionStatus(ctx, executionID, status, rawErr.Error(), nil)
}

type jsonStreamEvent struct {
	LastEventID string          `json:"last_event_id,omitempty"`
	Type        string          `json:"event"`
	Data        json.RawMessage `json:"data"`
}

type binaryStreamChunkSummary struct {
	Object      string `json:"object"`
	ContentType string `json:"content_type"`
	Bytes       int    `json:"bytes"`
}

func isBinaryStreamChunk(chunk *httpclient.StreamEvent) bool {
	if chunk == nil {
		return false
	}

	eventType := strings.ToLower(strings.TrimSpace(chunk.Type))

	return strings.HasPrefix(eventType, "audio/") || eventType == "application/octet-stream"
}

func shouldSkipStoredStreamChunk(chunk *httpclient.StreamEvent) bool {
	return chunk == nil ||
		(!isBinaryStreamChunk(chunk) && bytes.Equal(chunk.Data, llm.DoneStreamEvent.Data)) ||
		chunk.Type == httpclient.BinaryStreamDoneEventType
}

func marshalStreamEventForStorage(chunk *httpclient.StreamEvent) (objects.JSONRawMessage, error) {
	data := json.RawMessage(chunk.Data)
	if isBinaryStreamChunk(chunk) {
		// Prefer chunk.Size, which is set when the persistence layer summarized the
		// raw audio chunk to avoid buffering audio bytes in memory.
		byteCount := len(chunk.Data)
		if byteCount == 0 {
			byteCount = chunk.Size
		}

		var err error
		data, err = json.Marshal(binaryStreamChunkSummary{
			Object:      "binary.stream_chunk",
			ContentType: strings.TrimSpace(chunk.Type),
			Bytes:       byteCount,
		})
		if err != nil {
			return nil, err
		}
	}

	return xjson.Marshal(jsonStreamEvent{
		LastEventID: chunk.LastEventID,
		Type:        chunk.Type,
		Data:        data,
	})
}

// SaveRequestExecutionChunks saves all response chunks to request execution at once.
// Only stores chunks if the system StoreChunks setting is enabled.
func (s *RequestService) SaveRequestExecutionChunks(
	ctx context.Context,
	executionID int,
	chunks []*httpclient.StreamEvent,
) error {
	stored := s.debugStreamPayload(ctx, chunks)
	if len(stored) == 0 {
		return nil
	}
	_, err := s.entFromContext(ctx).RequestExecution.UpdateOneID(executionID).SetResponseChunks(stored).Save(ctx)
	return err
}

// SaveRequestChunks saves all response chunks to request at once.
// Only stores chunks if the system StoreChunks setting is enabled.
func (s *RequestService) SaveRequestChunks(
	ctx context.Context,
	requestID int,
	chunks []*httpclient.StreamEvent,
) error {
	stored := s.debugStreamPayload(ctx, chunks)
	if len(stored) == 0 {
		return nil
	}
	_, err := s.entFromContext(ctx).Request.UpdateOneID(requestID).SetResponseChunks(stored).Save(ctx)
	return err
}

// MarkRequestCanceled updates request status to canceled.
func (s *RequestService) MarkRequestCanceled(ctx context.Context, requestID int) error {
	return s.UpdateRequestStatus(ctx, requestID, request.StatusCanceled)
}

// MarkRequestFailed updates request status to failed.
func (s *RequestService) MarkRequestFailed(ctx context.Context, requestID int) error {
	return s.UpdateRequestStatus(ctx, requestID, request.StatusFailed)
}

// UpdateRequestStatus updates request status to the provided value (e.g., canceled or failed).
func (s *RequestService) UpdateRequestStatus(ctx context.Context, requestID int, status request.Status) error {
	client := s.entFromContext(ctx)

	// Load the pre-update request for metric recording when the update
	// targets a terminal state; the load is skipped entirely when metrics
	// are disabled.
	var metricReq *ent.Request
	if metrics.Enabled() && isTerminalRequestStatus(status) {
		if req, err := client.Request.Get(ctx, requestID); err == nil {
			metricReq = req
		}
	}

	_, err := client.Request.UpdateOneID(requestID).
		SetStatus(status).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to update request status: %w", err)
	}

	if metricReq != nil {
		s.recordChatRequestMetrics(ctx, metricReq, status, nil)
	}

	return nil
}

// UpdateRequestStatusFromError updates request status based on error type: canceled if context canceled, otherwise failed.
func (s *RequestService) UpdateRequestStatusFromError(ctx context.Context, requestID int, rawErr error, failure ...*objects.RequestFailure) error {
	status := request.StatusFailed
	if errors.Is(rawErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		status = request.StatusCanceled
	}
	if len(failure) > 0 && failure[0] != nil {
		return s.entFromContext(ctx).Request.UpdateOneID(requestID).SetStatus(status).SetFailure(failure[0]).Exec(ctx)
	}
	// Stream cleanup may observe cancellation after an explicit delivery failure.
	return s.entFromContext(ctx).Request.Update().Where(request.IDEQ(requestID), request.FailureIsNil()).SetStatus(status).Exec(ctx)
}

// cancelStaleRecords updates records older than maxAge to canceled status.
func (s *RequestService) cancelStaleRecords(
	ctx context.Context,
	maxAge time.Duration,
	entityName string,
	updateFn func(ctx context.Context, cutoff time.Time) (int, error),
) error {
	cutoff := time.Now().UTC().Add(-maxAge)
	return authz.RunWithSystemBypassVoid(ctx, "cleanup-"+entityName, func(ctx context.Context) error {
		count, err := updateFn(ctx, cutoff)
		if err != nil {
			return fmt.Errorf("failed to cancel stale %s: %w", entityName, err)
		}
		if count > 0 {
			log.Info(ctx, "canceled stale processing records",
				log.String("entity", entityName),
				log.Int("count", count),
				log.Duration("maxAge", maxAge))
		}
		return nil
	})
}

// maxProcessingDuration defines how long a record can be in "processing" state.
// Records exceeding this are considered stuck and will be canceled on startup.
const maxProcessingDuration = 1 * time.Hour

func (s *RequestService) ClearStaleProcessingOnStartup(ctx context.Context) error {
	var errs []error

	if err := s.cancelStaleRecords(ctx, maxProcessingDuration, "requests", func(ctx context.Context, cutoff time.Time) (int, error) {
		return s.entFromContext(ctx).Request.Update().
			Where(
				request.StatusEQ(request.StatusProcessing),
				request.CreatedAtLT(cutoff),
				withoutBudgetHold(budgetcharge.FieldRequestID),
			).
			SetStatus(request.StatusCanceled).
			Save(ctx)
	}); err != nil {
		errs = append(errs, err)
	}

	if err := s.cancelStaleRecords(ctx, maxProcessingDuration, "executions", func(ctx context.Context, cutoff time.Time) (int, error) {
		return s.entFromContext(ctx).RequestExecution.Update().
			Where(
				requestexecution.StatusEQ(requestexecution.StatusProcessing),
				requestexecution.CreatedAtLT(cutoff),
				withoutBudgetHold(budgetcharge.FieldExecutionID),
			).
			SetStatus(requestexecution.StatusCanceled).
			Save(ctx)
	}); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("startup cleanup failed: %w", errors.Join(errs...))
	}
	return nil
}

// UpdateRequestChannelID updates request with channel ID after channel selection.
func (s *RequestService) UpdateRequestChannelID(ctx context.Context, requestID int, channelID int) error {
	client := s.entFromContext(ctx)

	req, err := client.Request.UpdateOneID(requestID).
		SetChannelID(channelID).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to update request channel ID: %w", err)
	}

	s.cachePreviousChannelForRequest(ctx, req)

	return nil
}

// LoadRequestBody returns the stored request body, loading from external storage when necessary.
func (s *RequestService) LoadRequestBody(ctx context.Context, req *ent.Request) (objects.JSONRawMessage, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	if body, ok := s.PayloadService.loadDebug(ctx, req.RequestBody); ok {
		return body, nil
	}

	dataStorage, err := s.getDataStorage(ctx, req.DataStorageID)
	if err != nil {
		log.Warn(ctx, "Failed to get data storage for request body", log.Cause(err), log.Int("request_id", req.ID))
		return xjson.EmptyJSONRawMessage, nil
	}

	if !s.shouldUseExternalStorage(ctx, dataStorage) {
		if req.RequestBody == nil {
			return xjson.EmptyJSONRawMessage, nil
		}

		return req.RequestBody, nil
	}

	key := GenerateRequestBodyKey(req.ProjectID, req.ID)

	data, err := s.DataStorageService.LoadData(ctx, dataStorage, key)
	if err != nil {
		return xjson.EmptyJSONRawMessage, nil
	}

	if json.Valid(data) {
		return objects.JSONRawMessage(data), nil
	}

	return xjson.EmptyJSONRawMessage, nil
}

// LoadResponseBody returns the request response body, loading from external storage when necessary.
func (s *RequestService) LoadResponseBody(ctx context.Context, req *ent.Request) (objects.JSONRawMessage, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	if body, ok := s.PayloadService.loadDebug(ctx, req.ResponseBody); ok {
		return body, nil
	}

	// Only load response body if request is completed
	if req.Status != request.StatusCompleted {
		return xjson.EmptyJSONRawMessage, nil
	}

	dataStorage, err := s.getDataStorage(ctx, req.DataStorageID)
	if err != nil {
		log.Warn(ctx, "Failed to get data storage for request response body", log.Cause(err), log.Int("request_id", req.ID))
		return xjson.EmptyJSONRawMessage, nil
	}

	if !s.shouldUseExternalStorage(ctx, dataStorage) {
		return sanitizeLoadedResponseBody(req.ResponseBody), nil
	}

	key := GenerateResponseBodyKey(req.ProjectID, req.ID)

	data, err := s.DataStorageService.LoadData(ctx, dataStorage, key)
	if err != nil {
		return xjson.EmptyJSONRawMessage, nil
	}

	if json.Valid(data) {
		return objects.JSONRawMessage(data), nil
	}

	return xjson.EmptyJSONRawMessage, nil
}

// LoadCompletedResponsesSession loads the original request and final response
// for a Responses API continuation. The lookup is explicitly scoped to the
// authenticated API key and project because provider response IDs are supplied
// by the client.
func (s *RequestService) LoadCompletedResponsesSession(
	ctx context.Context,
	responseID string,
) (requestBody, responseBody []byte, found bool, err error) {
	responseID = strings.TrimSpace(responseID)
	apiKey, ok := contexts.GetAPIKey(ctx)
	if responseID == "" || !ok || apiKey == nil {
		return nil, nil, false, nil
	}
	projectID, hasProject := contexts.GetProjectID(ctx)
	if !hasProject {
		return nil, nil, false, nil
	}

	if input, output, found := s.loadResponsesContext(ctx, responseID); found {
		return input, output, true, nil
	}

	query := s.entFromContext(ctx).Request.Query().Where(
		request.ExternalIDEQ(responseID),
		request.APIKeyIDEQ(apiKey.ID),
		request.FormatIn(
			string(llm.APIFormatOpenAIResponse),
			string(llm.APIFormatOpenAIResponseWebSocket),
		),
		request.StatusEQ(request.StatusCompleted),
	)
	query = query.Where(request.ProjectIDEQ(projectID))

	req, err := query.Order(ent.Desc(request.FieldCreatedAt)).First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil, false, nil
		}
		return nil, nil, false, fmt.Errorf("failed to find completed Responses request: %w", err)
	}

	// New requests use the independent continuation cache. A missing entry
	// must not resurrect an expired debug copy; old rows remain readable.
	if _, managed := parsePayloadMarker(req.RequestBody); managed {
		return nil, nil, false, nil
	}

	storedRequest, err := s.LoadRequestBody(ctx, req)
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to load Responses request body: %w", err)
	}
	storedResponse, err := s.LoadResponseBody(ctx, req)
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to load Responses response body: %w", err)
	}
	if bytes.Equal(storedRequest, xjson.EmptyJSONRawMessage) ||
		bytes.Equal(storedResponse, xjson.EmptyJSONRawMessage) ||
		!json.Valid(storedRequest) ||
		!json.Valid(storedResponse) {
		return nil, nil, false, nil
	}
	if !certifiedResponsesSnapshot(storedRequest, storedResponse, responseID) {
		return nil, nil, false, nil
	}

	return append([]byte(nil), storedRequest...), append([]byte(nil), storedResponse...), true, nil
}

func isFinishedStreamStatus(status request.Status) bool {
	switch status {
	case request.StatusCompleted, request.StatusFailed, request.StatusCanceled:
		return true
	default:
		return false
	}
}

func isFinishedExecutionStatus(status requestexecution.Status) bool {
	switch status {
	case requestexecution.StatusCompleted, requestexecution.StatusFailed, requestexecution.StatusCanceled:
		return true
	default:
		return false
	}
}

// LoadResponseChunks returns the request response chunks, loading from external storage when necessary.
func (s *RequestService) LoadResponseChunks(ctx context.Context, req *ent.Request) ([]objects.JSONRawMessage, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	if chunks, ok := s.loadDebugStreamPayload(ctx, req.ResponseChunks); ok {
		return chunks, nil
	}

	// Live preview for active streaming requests
	if req.Stream && req.Status == request.StatusProcessing {
		chunks := s.LiveStreamRegistry.GetRequestChunks(req.ID)
		return chunks, nil
	}
	// Load persisted chunks for finished streams, including failed/canceled ones
	// that still buffered partial upstream output for debugging.
	if !req.Stream || !isFinishedStreamStatus(req.Status) {
		return []objects.JSONRawMessage{}, nil
	}

	dataStorage, err := s.getDataStorage(ctx, req.DataStorageID)
	if err != nil {
		// No external storage configured (common in tests / DB-only installs).
		// Fall back to whatever was persisted on the request row.
		if chunks := sanitizeLoadedResponseChunks(req.ResponseChunks); len(chunks) > 0 {
			return chunks, nil
		}
		log.Warn(ctx, "Failed to get data storage for request response chunks", log.Cause(err), log.Int("request_id", req.ID))
		return []objects.JSONRawMessage{}, nil
	}

	if !s.shouldUseExternalStorage(ctx, dataStorage) {
		return sanitizeLoadedResponseChunks(req.ResponseChunks), nil
	}

	key := GenerateResponseChunksKey(req.ProjectID, req.ID)

	data, err := s.DataStorageService.LoadData(ctx, dataStorage, key)
	if err != nil {
		log.Warn(ctx, "Failed to load request response chunks", log.Cause(err), log.Int("request_id", req.ID))

		return []objects.JSONRawMessage{}, nil
	}

	if len(data) == 0 {
		return []objects.JSONRawMessage{}, nil
	}

	var chunks []objects.JSONRawMessage
	if err := json.Unmarshal(data, &chunks); err != nil {
		log.Warn(ctx, "Failed to unmarshal request response chunks", log.Cause(err), log.Int("request_id", req.ID))
		return []objects.JSONRawMessage{}, nil
	}

	return chunks, nil
}

// LoadRequestExecutionRequestBody returns the execution request body, loading from external storage when necessary.
func (s *RequestService) LoadRequestExecutionRequestBody(ctx context.Context, exec *ent.RequestExecution) (objects.JSONRawMessage, error) {
	if exec == nil {
		return nil, fmt.Errorf("request execution is nil")
	}
	if body, ok := s.PayloadService.loadDebug(ctx, exec.RequestBody); ok {
		return body, nil
	}

	dataStorage, err := s.getDataStorage(ctx, exec.DataStorageID)
	if err != nil {
		log.Warn(ctx, "Failed to get data storage for execution request body", log.Cause(err), log.Int("execution_id", exec.ID))
		return xjson.EmptyJSONRawMessage, nil
	}

	if !s.shouldUseExternalStorage(ctx, dataStorage) {
		if exec.RequestBody == nil {
			return xjson.EmptyJSONRawMessage, nil
		}

		return exec.RequestBody, nil
	}

	key := GenerateExecutionRequestBodyKey(exec.ProjectID, exec.RequestID, exec.ID)

	data, err := s.DataStorageService.LoadData(ctx, dataStorage, key)
	if err != nil {
		return xjson.EmptyJSONRawMessage, nil
	}

	if json.Valid(data) {
		return objects.JSONRawMessage(data), nil
	}

	return xjson.EmptyJSONRawMessage, nil
}

// LoadRequestExecutionResponseBody returns the execution response body, loading from external storage when necessary.
func (s *RequestService) LoadRequestExecutionResponseBody(ctx context.Context, exec *ent.RequestExecution) (objects.JSONRawMessage, error) {
	if exec == nil {
		return nil, fmt.Errorf("request execution is nil")
	}
	if body, ok := s.PayloadService.loadDebug(ctx, exec.ResponseBody); ok {
		return body, nil
	}

	// Only load response body if execution is completed
	if exec.Status != requestexecution.StatusCompleted {
		return xjson.EmptyJSONRawMessage, nil
	}

	dataStorage, err := s.getDataStorage(ctx, exec.DataStorageID)
	if err != nil {
		log.Warn(ctx, "Failed to get data storage for execution response body", log.Cause(err), log.Int("execution_id", exec.ID))
		return xjson.EmptyJSONRawMessage, nil
	}

	if !s.shouldUseExternalStorage(ctx, dataStorage) {
		return sanitizeLoadedResponseBody(exec.ResponseBody), nil
	}

	key := GenerateExecutionResponseBodyKey(exec.ProjectID, exec.RequestID, exec.ID)

	data, err := s.DataStorageService.LoadData(ctx, dataStorage, key)
	if err != nil {
		return xjson.EmptyJSONRawMessage, nil
	}

	if json.Valid(data) {
		return objects.JSONRawMessage(data), nil
	}

	return xjson.EmptyJSONRawMessage, nil
}

// LoadRequestExecutionResponseChunks returns the execution response chunks, loading from external storage when necessary.
func (s *RequestService) LoadRequestExecutionResponseChunks(ctx context.Context, exec *ent.RequestExecution) ([]objects.JSONRawMessage, error) {
	if exec == nil {
		return nil, fmt.Errorf("request execution is nil")
	}
	if chunks, ok := s.loadDebugStreamPayload(ctx, exec.ResponseChunks); ok {
		return chunks, nil
	}

	// Live preview for active streaming executions
	if exec.Stream && exec.Status == requestexecution.StatusProcessing {
		chunks := s.LiveStreamRegistry.GetExecutionChunks(exec.ID)
		return chunks, nil
	}
	// Load persisted chunks for finished streams, including failed/canceled ones
	// that still buffered partial upstream output for debugging.
	if !exec.Stream || !isFinishedExecutionStatus(exec.Status) {
		return []objects.JSONRawMessage{}, nil
	}

	dataStorage, err := s.getDataStorage(ctx, exec.DataStorageID)
	if err != nil {
		// No external storage configured (common in tests / DB-only installs).
		// Fall back to whatever was persisted on the execution row.
		if chunks := sanitizeLoadedResponseChunks(exec.ResponseChunks); len(chunks) > 0 {
			return chunks, nil
		}
		log.Warn(ctx, "Failed to get data storage for execution response chunks", log.Cause(err), log.Int("execution_id", exec.ID))
		return []objects.JSONRawMessage{}, nil
	}

	if !s.shouldUseExternalStorage(ctx, dataStorage) {
		return sanitizeLoadedResponseChunks(exec.ResponseChunks), nil
	}

	key := GenerateExecutionResponseChunksKey(exec.ProjectID, exec.RequestID, exec.ID)

	data, err := s.DataStorageService.LoadData(ctx, dataStorage, key)
	if err != nil {
		log.Warn(ctx, "Failed to load request execution response chunks", log.Cause(err), log.Int("execution_id", exec.ID))

		return []objects.JSONRawMessage{}, nil
	}

	if json.Valid(data) {
		var chunks []objects.JSONRawMessage
		if err := json.Unmarshal(data, &chunks); err != nil {
			log.Warn(ctx, "Failed to unmarshal request execution response chunks", log.Cause(err), log.Int("execution_id", exec.ID))
			return []objects.JSONRawMessage{}, nil
		}

		return chunks, nil
	}

	return []objects.JSONRawMessage{}, nil
}

func (s *RequestService) GetTraceFirstRequest(ctx context.Context, traceID int, fields ...string) (*ent.Request, error) {
	client := s.entFromContext(ctx)
	if client == nil {
		return nil, fmt.Errorf("ent client not found in context")
	}

	request, err := client.Request.Query().
		Where(request.TraceIDEQ(traceID), request.StatusEQ(request.StatusCompleted)).
		Order(ent.Asc(request.FieldCreatedAt)).
		Select(fields...).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("failed to get first request for trace: %w", err)
	}

	return request, nil
}

// GetTraceFirstUserQuery loads the user input for list previews without response payloads.
func (s *RequestService) GetTraceFirstUserQuery(ctx context.Context, traceID int) (*string, error) {
	req, err := s.GetTraceFirstRequest(ctx, traceID,
		request.FieldID, request.FieldProjectID, request.FieldDataStorageID, request.FieldFormat, request.FieldRequestBody,
	)
	if err != nil || req == nil {
		return nil, err
	}
	req.RequestBody, err = s.LoadRequestBody(ctx, req)
	if err != nil {
		return nil, err
	}
	segment, err := requestToSegment(ctx, req)
	if err != nil {
		return nil, err
	}
	return segment.FirstUserQuery(), nil
}

func (s *RequestService) GetTraceFirstSegment(ctx context.Context, traceID int) (*Segment, error) {
	request, err := s.GetTraceFirstRequest(ctx, traceID)
	if err != nil {
		return nil, err
	}

	if request == nil {
		return nil, nil
	}

	body, err := s.LoadRequestBody(ctx, request)
	if err != nil {
		return nil, err
	}

	request.RequestBody = body

	body, err = s.LoadResponseBody(ctx, request)
	if err != nil {
		return nil, err
	}

	request.ResponseBody = body

	return requestToSegment(ctx, request)
}

// GetPreviousChannelID retrieves the most recently selected channel ID from a trace.
// The cache is the source of truth; when it expires, trace affinity is reset.
// Returns 0 if no selected channel is cached.
func (s *RequestService) GetPreviousChannelID(ctx context.Context, traceID int) (int, error) {
	cacheKey := buildPreviousTraceChannelCacheKey(traceID)
	if channelID, err := s.previousChannelCache.Get(ctx, cacheKey); err == nil {
		return channelID, nil
	}

	return 0, nil
}

// GetPreviousChannelIDByThread retrieves the most recently selected channel
// from all traces associated with a thread. The cache is the source of truth;
// when it expires, thread affinity is reset. Returns 0 if none is cached.
func (s *RequestService) GetPreviousChannelIDByThread(ctx context.Context, threadID int) (int, error) {
	cacheKey := buildPreviousThreadChannelCacheKey(threadID)
	if channelID, err := s.previousChannelCache.Get(ctx, cacheKey); err == nil {
		return channelID, nil
	}

	return 0, nil
}

func (s *RequestService) cachePreviousChannelForRequest(ctx context.Context, req *ent.Request) {
	if req == nil || req.ChannelID == 0 || req.TraceID == 0 {
		return
	}

	s.setPreviousTraceChannelID(ctx, req.TraceID, req.ChannelID)

	threadID := 0
	if currentTrace, ok := contexts.GetTrace(ctx); ok && currentTrace.ID == req.TraceID {
		threadID = currentTrace.ThreadID
	}

	if threadID == 0 {
		currentTrace, err := s.entFromContext(ctx).Trace.Get(ctx, req.TraceID)
		if err != nil {
			log.Warn(ctx, "failed to get trace for previous channel cache", log.Cause(err), log.Int("trace_id", req.TraceID))
			return
		}
		threadID = currentTrace.ThreadID
	}

	if threadID != 0 {
		s.setPreviousThreadChannelID(ctx, threadID, req.ChannelID)
	}
}

func (s *RequestService) setPreviousTraceChannelID(ctx context.Context, traceID, channelID int) {
	s.setPreviousChannelCache(ctx, buildPreviousTraceChannelCacheKey(traceID), channelID, 30*time.Minute)
}

func (s *RequestService) setPreviousThreadChannelID(ctx context.Context, threadID, channelID int) {
	s.setPreviousChannelCache(ctx, buildPreviousThreadChannelCacheKey(threadID), channelID, 30*time.Minute)
}

func (s *RequestService) setPreviousChannelCache(ctx context.Context, cacheKey string, channelID int, expiration time.Duration) {
	_ = s.previousChannelCache.Set(ctx, cacheKey, channelID, store.WithExpiration(expiration))
}

func buildPreviousTraceChannelCacheKey(traceID int) string {
	return fmt.Sprintf("axonhub:routing:previous-channel:v1:trace:%d", traceID)
}

func buildPreviousThreadChannelCacheKey(threadID int) string {
	return fmt.Sprintf("axonhub:routing:previous-channel:v1:thread:%d", threadID)
}

// Metrics are best-effort and must observe the status before the terminal write.
func (s *RequestService) requestMetricSnapshot(ctx context.Context, client *ent.Client, id int) *ent.Request {
 if !metrics.Enabled() { return nil }
 req, err := client.Request.Query().Where(request.IDEQ(id)).Select(request.FieldID, request.FieldStatus, request.FieldChannelID, request.FieldCreatedAt).Only(ctx)
 if err != nil { return nil }
 return req
}
