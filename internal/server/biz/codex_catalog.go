package biz

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/transformer/openai/codex"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
)

var errCodexMetadataUnavailable = errors.New("complete Codex metadata is not configured")

const (
	codexCatalogMaxBytes = 1 << 20
	// Codex allows five seconds including transport and decoding. Leave time
	// for model filtering, serialization and the client-to-gateway connection.
	codexCatalogRefreshTimeout = 3 * time.Second
)

type codexCatalogCache struct {
	models  map[string]map[string]any
	err     error
	expires time.Time
}

// CodexCatalogService keeps provider metadata separate from public model admission.
// Raw metadata is preserved: new client-specific fields must not be lost in a Go DTO.
type CodexCatalogService struct {
	models   *ModelService
	channels *ChannelService
	mu       sync.Mutex
	cache    map[string]codexCatalogCache
	flight   singleflight.Group
}

func NewCodexCatalogService(models *ModelService, channels *ChannelService) *CodexCatalogService {
	return &CodexCatalogService{models: models, channels: channels, cache: make(map[string]codexCatalogCache)}
}

func (s *CodexCatalogService) Models(ctx context.Context, version string) ([]byte, int, error) {
	visible, err := s.models.ListEnabledModels(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("cannot read models permitted by this API key")
	}
	channels := make(map[int]*Channel)
	allChannels := s.channels.GetEnabledChannels()
	for _, ch := range allChannels {
		channels[ch.ID] = ch
	}
	// Model definitions may be shared by routes for the same actual model.
	// Source discovery does not grant access to those routes: m.Connections stays
	// scoped to the caller and is used separately for capability intersection.
	var connections []*ModelChannelConnection
	sourceConnections := make(map[string][]*ModelChannelConnection)
	systemSettings := s.models.modelSettingsOrDefault(ctx)
	for _, m := range visible {
		if m.ConfiguredModel.Type == model.TypeChat && (m.ConfiguredModel.Settings == nil || len(m.ConfiguredModel.Settings.CodexCatalog) == 0 || string(m.ConfiguredModel.Settings.CodexCatalog) == "null") {
			matched := MatchConnectionsForModel(EffectiveModelAssociationsForRequest(systemSettings, m.ConfiguredModel, m.TargetID), allChannels, m.TargetID)
			sourceConnections[m.ID] = matched
			connections = append(connections, matched...)
		}
	}
	sources := s.fetchCatalogSources(ctx, connections, channels, version)
	result := make([]map[string]any, 0, len(visible))
	omitted := 0
	for _, m := range visible {
		if m.ConfiguredModel.Type != model.TypeChat {
			continue
		}
		if settings := m.ConfiguredModel.Settings; settings != nil && len(settings.CodexCatalog) > 0 && string(settings.CodexCatalog) != "null" {
			definition, definitionErr := decodeCodexDefinition(settings.CodexCatalog)
			var merged map[string]any
			if definitionErr == nil {
				merged, definitionErr = definition.catalogModel(m)
			}
			if definitionErr != nil {
				omitted++
				log.Warn(ctx, "Codex model omitted", log.String("model", m.ID), log.String("reason", definitionErr.Error()))
				continue
			}
			result = append(result, merged)
			continue
		}
		merged, err := automaticCodexModel(ctx, m, sourceConnections[m.ID], sources, channels, version)
		if err != nil {
			if errors.Is(err, errCodexMetadataUnavailable) {
				log.Debug(ctx, "Codex model has no configured metadata", log.String("model", m.ID))
			} else {
				log.Warn(ctx, "Codex model omitted", log.String("model", m.ID), log.String("reason", err.Error()))
			}
			omitted++
			continue
		}
		result = append(result, merged)
	}
	if len(result) == 0 && omitted > 0 {
		return nil, omitted, fmt.Errorf("no complete Codex metadata for permitted models; associated channels must provide metadata for the actual model, or configure a manual model definition")
	}
	body, err := json.Marshal(map[string]any{"models": result})
	if len(body) > codexCatalogMaxBytes {
		return nil, omitted, fmt.Errorf("filtered Codex catalog exceeds 1 MiB; narrow the API key model list")
	}
	return body, omitted, err
}

func (s *CodexCatalogService) channelCatalog(ctx context.Context, ch *Channel, version string) (map[string]map[string]any, error) {
	var ep objects.ChannelEndpoint
	for _, candidate := range ch.ResolveEndpoints() {
		if candidate.APIFormat == llm.APIFormatOpenAIResponse.String() {
			ep = candidate
			break
		}
	}
	if len(ep.CatalogJSON) > 0 && string(ep.CatalogJSON) != "null" {
		return parseCodexCatalog(ep.CatalogJSON)
	}
	if ep.APIFormat == "" {
		return nil, nil
	}
	u, err := url.Parse(codexCatalogURL(ch, ep))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("invalid catalog URL")
	}
	q := u.Query()
	q.Set("client_version", version)
	u.RawQuery = q.Encode()
	headers := make(http.Header)
	if provider, ok := ch.Outbounds[llm.APIFormatOpenAIResponse.String()].(*codex.OutboundTransformer); ok {
		creds, err := provider.TokenProvider().Get(ctx)
		if err != nil {
			return nil, fmt.Errorf("catalog authentication unavailable")
		}
		headers.Set("Authorization", "Bearer "+creds.AccessToken)
		if account := codex.ExtractChatGPTAccountIDFromJWT(creds.AccessToken); account != "" {
			headers.Set("ChatGPT-Account-ID", account)
		}
	} else {
		if ch.apiKeyOverride == "" && len(ch.cachedEnabledAPIKeys) == 0 {
			return nil, fmt.Errorf("channel does not provide API key authentication for the catalog; configure catalog JSON")
		}
		headers.Set("Authorization", "Bearer "+getAPIKeyProvider(ch).Get(ctx))
	}
	headers.Set("Accept", "application/json")
	identity, _ := json.Marshal(headers)
	key := fmt.Sprintf("%d:%s:%x", ch.ID, u.String(), sha256.Sum256(identity))
	s.mu.Lock()
	cached, found := s.cache[key]
	s.mu.Unlock()
	if found && time.Now().Before(cached.expires) {
		return cached.models, cached.err
	}
	value, err, _ := s.flight.Do(key, func() (any, error) {
		fetchCtx, cancel := context.WithTimeout(ctx, codexCatalogRefreshTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("invalid catalog request")
		}
		req.Header = headers
		client := *ch.HTTPClient.GetNativeClient()
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("catalog connection failed")
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("catalog returned HTTP %d", resp.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, codexCatalogMaxBytes+1))
		if err != nil {
			return nil, fmt.Errorf("catalog read failed")
		}
		models, parseErr := parseRemoteCodexCatalog(body)
		if errors.Is(parseErr, errCodexPlainModelList) {
			parseErr = nil // A valid ordinary model list declares no Codex metadata.
		}
		if parseErr != nil && !errors.Is(parseErr, errCodexInvalidEntries) {
			return nil, parseErr
		}
		ttl := catalogCacheTTL(resp.Header)
		if ttl > 0 {
			s.mu.Lock()
			for k, item := range s.cache {
				if time.Now().After(item.expires) {
					delete(s.cache, k)
				}
			}
			if len(s.cache) >= 128 {
				for k := range s.cache {
					delete(s.cache, k)
					break
				}
			}
			// An ordinary OpenAI list is a valid response without Codex metadata.
			// Cache that fact under the same identity/version and freshness rules.
			// Partial catalogs retain invalid-entry diagnostics with their valid data;
			// authentication, transport and malformed-response errors stay uncached.
			s.cache[key] = codexCatalogCache{models: models, err: parseErr, expires: time.Now().Add(ttl)}
			s.mu.Unlock()
		}
		return models, parseErr
	})
	if err != nil && !errors.Is(err, errCodexInvalidEntries) {
		return nil, err
	}
	models, ok := value.(map[string]map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected Codex catalog result type %T", value)
	}
	return models, err
}

func codexCatalogURL(ch *Channel, ep objects.ChannelEndpoint) string {
	if ep.CatalogURL != "" {
		return ep.CatalogURL
	}
	base := ep.BaseURL
	if base == "" {
		base = ch.BaseURL
	}
	exactBase := strings.HasSuffix(base, "#")
	base = httpModelsBaseURL(strings.TrimRight(base, "#/"))
	// OpenAI's public model list is not a Codex catalog. Only the explicit
	// official endpoint uses OpenAI's documented catalog; proxy keys stay local.
	if (ch.Type == channel.TypeOpenai || ch.Type == channel.TypeOpenaiResponses) &&
		(base == "https://api.openai.com" || base == "https://api.openai.com/v1") {
		return "https://chatgpt.com/backend-api/codex/models"
	}
	if ch.Type == channel.TypeCodex {
		if base == "" || base == "https://chatgpt.com" {
			base = "https://chatgpt.com/backend-api/codex"
		}
		return base + "/models"
	}
	if !strings.HasSuffix(base, "/v1") && !exactBase {
		base += "/v1"
	}
	return base + "/models"
}

func catalogCacheTTL(h http.Header) time.Duration {
	control := strings.ToLower(h.Get("Cache-Control"))
	if strings.Contains(control, "no-store") || strings.Contains(control, "no-cache") {
		return 0
	}
	ttl := 5 * time.Minute
	for _, part := range strings.Split(control, ",") {
		if seconds, ok := strings.CutPrefix(strings.TrimSpace(part), "max-age="); ok {
			if n, err := strconv.Atoi(strings.Trim(seconds, `"`)); err == nil {
				ttl = min(ttl, time.Duration(max(n, 0))*time.Second)
			}
		}
	}
	if age, err := strconv.Atoi(h.Get("Age")); err == nil {
		ttl -= time.Duration(age) * time.Second
	}
	return max(ttl, 0)
}

// Bound the whole refresh, rather than paying a timeout for every candidate.
func (s *CodexCatalogService) fetchCatalogSources(ctx context.Context, connections []*ModelChannelConnection, channels map[int]*Channel, version string) map[int]map[string]map[string]any {
	ctx, cancel := context.WithTimeout(ctx, codexCatalogRefreshTimeout)
	defer cancel()
	result := make(map[int]map[string]map[string]any)
	seen := make(map[int]bool)
	var sources []*Channel
	for _, connection := range connections {
		id := connection.Channel.ID
		if seen[id] || channels[id] == nil {
			continue
		}
		seen[id] = true
		sources = append(sources, channels[id])
	}
	// Ordinary /v1/models probes must not occupy every slot ahead of explicit
	// catalogs. This changes fetch order, not model/profile selection priority.
	slices.SortStableFunc(sources, func(a, b *Channel) int {
		return codexCatalogSourcePriority(a) - codexCatalogSourcePriority(b)
	})
	var mu sync.Mutex
	var group errgroup.Group
	group.SetLimit(4)
	for _, ch := range sources {
		group.Go(func() error {
			defer func() {
				if recovered := recover(); recovered != nil {
					log.Error(ctx, "catalog refresh panicked", log.Any("panic", recovered))
				}
			}()
			models, err := s.channelCatalog(ctx, ch, version)
			if errors.Is(err, errCodexInvalidEntries) {
				log.Warn(ctx, "Codex catalog contains invalid model definitions", log.Int("channel_id", ch.ID), log.String("reason", err.Error()))
			} else if err != nil {
				log.Warn(ctx, "Codex catalog source unavailable", log.Int("channel_id", ch.ID), log.String("reason", err.Error()))
			}
			mu.Lock()
			result[ch.ID] = models
			mu.Unlock()
			return nil
		})
	}
	_ = group.Wait()
	return result
}

func codexCatalogSourcePriority(ch *Channel) int {
	for _, ep := range ch.ResolveEndpoints() {
		if ep.APIFormat != llm.APIFormatOpenAIResponse.String() {
			continue
		}
		if len(ep.CatalogJSON) > 0 && string(ep.CatalogJSON) != "null" {
			return 0
		}
		if ep.CatalogURL != "" || ch.Type == channel.TypeCodex ||
			codexCatalogURL(ch, ep) == "https://chatgpt.com/backend-api/codex/models" {
			return 1
		}
		break
	}
	return 2
}
