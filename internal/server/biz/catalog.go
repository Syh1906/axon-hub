package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/tidwall/gjson"
	"golang.org/x/sync/singleflight"

	_ "embed"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm/httpclient"
)

const (
	catalogSourceUpstream = "upstream"
	catalogSourceFallback = "fallback"
	catalogFetchTimeout   = 20 * time.Second

	// catalogHoldTTL matches the UI freshness interval; expired data remains
	// available for transient upstream failures without background polling.
	catalogHoldTTL = 5 * time.Minute
)

// Bundled snapshots. scripts/sync/sync-model-developers.js copies providers.json here.
//
//go:embed catalogdata/providers.json
var catalogFallbackJSON []byte

//go:embed catalogdata/models.json
var catalogExtraModelsJSON []byte

// CatalogSnapshot is the catalog payload served to the admin UI.
type CatalogSnapshot struct {
	Data      objects.JSONRawMessage `json:"data"`
	FetchedAt *time.Time             `json:"fetched_at,omitempty"`
	Source    string                 `json:"source"`
	Filtered  bool                   `json:"filtered"`
}

// catalogPayload is a finished JSON payload. Only encoded bytes are retained:
// the upstream catalog decoded into maps costs ~4x its JSON size and is all
// pointers the GC has to scan.
type catalogPayload struct {
	data      []byte
	fetchedAt time.Time
	source    string
}

// CatalogService holds encoded data on demand. A failed refresh retains the last
// successful source for this process; both UI variants share one upstream fetch.
type CatalogService struct {
	system      *SystemService
	httpClient  *httpclient.HttpClient
	mu          sync.Mutex
	unfiltered  *catalogPayload
	filtered    *catalogPayload
	lastSuccess *catalogPayload
	freshUntil  time.Time
	generation  uint64
	holdTTL     time.Duration
	sf          singleflight.Group
}

func NewCatalogService(system *SystemService, httpClient *httpclient.HttpClient) *CatalogService {
	return &CatalogService{system: system, httpClient: httpClient, holdTTL: catalogHoldTTL}
}

func (s *CatalogService) Snapshot(ctx context.Context, filtered bool) (CatalogSnapshot, error) {
	payload, err := s.ensure(ctx, filtered)
	if err != nil {
		return CatalogSnapshot{}, err
	}
	return snapshotFromPayload(payload, filtered), nil
}

// Invalidate is used when the source settings change. Do not use an old URL's
// successful response as the fallback for a newly configured source.
func (s *CatalogService) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	s.unfiltered, s.filtered, s.lastSuccess = nil, nil, nil
	s.freshUntil = time.Time{}
}

// Refresh expires freshness, preserving the successful payload if the source is
// temporarily unavailable. Concurrent visitors share this explicit refresh.
func (s *CatalogService) Refresh(ctx context.Context) (CatalogSnapshot, error) {
	s.mu.Lock()
	s.generation++
	s.freshUntil = time.Time{}
	s.mu.Unlock()
	return s.Snapshot(ctx, true)
}

func (s *CatalogService) ensure(ctx context.Context, filtered bool) (*catalogPayload, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		generation := s.generation
		payload := s.unfiltered
		if filtered {
			payload = s.filtered
		}
		fresh := time.Now().Before(s.freshUntil)
		s.mu.Unlock()
		if fresh && payload != nil {
			return payload, nil
		}

		result := s.sf.DoChan("catalog", func() (any, error) {
			s.mu.Lock()
			generation := s.generation
			source, previous := s.unfiltered, s.lastSuccess
			fresh := time.Now().Before(s.freshUntil)
			s.mu.Unlock()
			if !fresh || source == nil {
				fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), catalogFetchTimeout)
				settings := s.system.CatalogSettingsOrDefault(fetchCtx)
				body, err := s.fetchUpstream(fetchCtx, settings.EffectiveUpstreamURL())
				cancel()
				var prepared []byte
				if err == nil {
					prepared, err = buildFilteredCatalog(body)
				}
				if err == nil {
					source = &catalogPayload{data: body, fetchedAt: time.Now().UTC(), source: catalogSourceUpstream}
				} else {
					log.Warn(ctx, "catalog fetch failed, preserving available catalog", log.Cause(err))
					source = previous
					if source == nil {
						source = &catalogPayload{data: fallbackCatalogJSON(), source: catalogSourceFallback}
					}
				}
				s.mu.Lock()
				if generation != s.generation {
					s.mu.Unlock()
					return nil, nil
				}
				s.unfiltered, s.filtered = source, nil
				if prepared != nil {
					s.filtered = &catalogPayload{data: prepared, fetchedAt: source.fetchedAt, source: source.source}
				}
				if source.source == catalogSourceUpstream {
					s.lastSuccess = source
				}
				s.freshUntil = time.Now().Add(s.holdTTL)
				s.mu.Unlock()
			}
			if filtered {
				s.mu.Lock()
				ready := s.filtered != nil
				s.mu.Unlock()
				if !ready {
					body, err := buildFilteredCatalog(source.data)
					if err != nil {
						return nil, err
					}
					result := &catalogPayload{data: body, fetchedAt: source.fetchedAt, source: source.source}
					s.mu.Lock()
					if generation == s.generation {
						s.filtered = result
					}
					s.mu.Unlock()
				}
			}
			return nil, nil
		})
		select {
		case fetched := <-result:
			if fetched.Err != nil {
				return nil, fetched.Err
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		s.mu.Lock()
		current := s.generation
		s.mu.Unlock()
		if current != generation {
			continue
		}
	}
}

// fetchUpstream returns the upstream catalog body after checking it is a JSON
// document with a providers object. The body is served verbatim to the admin
// UI, so it must be valid JSON.
func (s *CatalogService) fetchUpstream(ctx context.Context, upstreamURL string) ([]byte, error) {
	if s.httpClient == nil {
		return nil, fmt.Errorf("catalog http client is not configured")
	}

	resp, err := s.httpClient.Do(ctx, &httpclient.Request{
		Method: http.MethodGet,
		URL:    upstreamURL,
		Headers: http.Header{
			"Accept": []string{"application/json"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("fetch catalog: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch catalog: unexpected status %d", resp.StatusCode)
	}

	if err := validateCatalogJSON(resp.Body); err != nil {
		return nil, err
	}

	return resp.Body, nil
}

func validateCatalogJSON(body []byte) error {
	if !json.Valid(body) {
		return fmt.Errorf("parse catalog: invalid JSON")
	}

	if !gjson.GetBytes(body, "providers").IsObject() {
		return fmt.Errorf("catalog is missing providers")
	}

	return nil
}

// buildFilteredCatalog decodes a full catalog, applies the developer filters
// and extra models, and returns the encoded result. The decoded catalog is
// discarded on return.
func buildFilteredCatalog(body []byte) ([]byte, error) {
	var data catalogFile
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("parse catalog: %w", err)
	}

	if data.Providers == nil {
		return nil, fmt.Errorf("catalog is missing providers")
	}

	prepared := filterCatalogProviders(data, DefaultDeveloperIDs)
	if err := mergeExtraModels(&prepared, catalogExtraModelsJSON); err != nil {
		return nil, fmt.Errorf("merge extra catalog models: %w", err)
	}

	sortCatalogModels(&prepared)

	return json.Marshal(prepared)
}

func fallbackCatalogJSON() []byte {
	if validateCatalogJSON(catalogFallbackJSON) != nil {
		return []byte(`{"providers":{}}`)
	}

	return catalogFallbackJSON
}

func snapshotFromPayload(payload *catalogPayload, filtered bool) CatalogSnapshot {
	var fetchedAt *time.Time
	if !payload.fetchedAt.IsZero() {
		at := payload.fetchedAt
		fetchedAt = &at
	}
	source := payload.source
	if source == "" {
		source = catalogSourceFallback
	}

	return CatalogSnapshot{
		Data:      objects.JSONRawMessage(payload.data),
		FetchedAt: fetchedAt,
		Source:    source,
		Filtered:  filtered,
	}
}
