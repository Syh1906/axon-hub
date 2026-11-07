package biz

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/system"
	"github.com/looplj/axonhub/internal/log"
)

//go:embed codexdata/*.json
var codexSnapshotFiles embed.FS

const codexSnapshotKeyPrefix = "codex_official_snapshot_v1:"

type CodexCatalogConfig struct {
	Disabled bool   `conf:"disabled" yaml:"disabled" json:"disabled"`
	Offline  bool   `conf:"offline" yaml:"offline" json:"offline"`
	Version  string `conf:"version" yaml:"version" json:"version"`
}

type codexSnapshot struct {
	Version string                    `json:"version"`
	Source  string                    `json:"source"`
	SHA256  string                    `json:"sha256"`
	Body    []byte                    `json:"body"`
	Models  map[string]map[string]any `json:"-"`
}

// CodexSnapshotService owns credential-free official metadata, independently of
// inference channels. Release snapshots are immutable and archived by version.
type CodexSnapshotService struct {
	config      CodexCatalogConfig
	db          *ent.Client
	httpClient  *http.Client
	mu          sync.Mutex
	snapshots   map[string]*codexSnapshot
	nextAttempt map[string]time.Time
	flight      singleflight.Group
}

func NewCodexSnapshotService(config CodexCatalogConfig, db *ent.Client) (*CodexSnapshotService, error) {
	if config.Version != "" && !validCodexSnapshotVersion(config.Version) {
		return nil, fmt.Errorf("invalid codex_catalog.version")
	}
	s := &CodexSnapshotService{config: config, db: db, snapshots: make(map[string]*codexSnapshot), nextAttempt: make(map[string]time.Time),
		httpClient: &http.Client{Timeout: 1500 * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	var manifest []struct{ Version, Source, SHA256 string }
	body, err := codexSnapshotFiles.ReadFile("codexdata/manifest.json")
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, err
	}
	for _, item := range manifest {
		body, err := codexSnapshotFiles.ReadFile("codexdata/" + item.Version + ".json")
		if err != nil {
			return nil, err
		}
		snapshot, err := parseCodexSnapshot(item.Version, item.Source, item.SHA256, body)
		if err != nil {
			return nil, err
		}
		s.snapshots[item.Version] = snapshot
	}
	return s, nil
}

func validCodexSnapshotVersion(version string) bool {
	if len(version) > 32 || !definitionVersion.MatchString(version) {
		return false
	}
	for _, part := range strings.Split(version, ".") {
		if _, err := strconv.ParseUint(part, 10, 32); err != nil {
			return false
		}
	}
	return true
}

func compareCodexVersions(a, b string) int {
	ap, bp := strings.Split(a, "."), strings.Split(b, ".")
	for i := range ap {
		x, _ := strconv.ParseUint(ap[i], 10, 32)
		y, _ := strconv.ParseUint(bp[i], 10, 32)
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

func officialCodexSnapshotURL(version string) string {
	return "https://raw.githubusercontent.com/openai/codex/rust-v" + version + "/codex-rs/models-manager/models.json"
}

func parseCodexSnapshot(version, source, hash string, body []byte) (*codexSnapshot, error) {
	if !validCodexSnapshotVersion(version) || source != officialCodexSnapshotURL(version) {
		return nil, fmt.Errorf("invalid official snapshot provenance")
	}
	actual := fmt.Sprintf("%x", sha256.Sum256(body))
	if actual != hash {
		return nil, fmt.Errorf("official snapshot checksum mismatch")
	}
	models, err := parseCodexCatalog(body)
	if err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("empty official snapshot")
	}
	return &codexSnapshot{Version: version, Source: source, SHA256: hash, Body: body, Models: models}, nil
}

// Load uses an exact release when available, otherwise the newest verified
// snapshot no newer than the client. A configured/per-model pin never drifts.
func (s *CodexSnapshotService) Load(ctx context.Context, clientVersion, pin string) *codexSnapshot {
	if s.config.Disabled || !validCodexSnapshotVersion(clientVersion) {
		return nil
	}
	if pin == "" {
		pin = s.config.Version
	}
	version := clientVersion
	if pin != "" {
		if !validCodexSnapshotVersion(pin) || compareCodexVersions(pin, clientVersion) > 0 {
			return nil
		}
		version = pin
	}
	s.mu.Lock()
	exact := s.snapshots[version]
	next := s.nextAttempt[version]
	s.mu.Unlock()
	if exact != nil {
		return exact
	}
	if time.Now().After(next) {
		_, _, _ = s.flight.Do(version, func() (any, error) {
			s.mu.Lock()
			ready, retryAt := s.snapshots[version], s.nextAttempt[version]
			s.mu.Unlock()
			if ready != nil || time.Now().Before(retryAt) {
				return nil, nil
			}
			bypass := authz.WithSystemBypass(ctx, "codex-official-snapshot")
			snapshot, err := s.readArchive(bypass, version)
			if err != nil {
				log.Warn(ctx, "Codex snapshot archive unavailable", log.Cause(err))
			}
			if snapshot == nil && !s.config.Offline {
				snapshot, err = s.fetch(ctx, version)
				if err != nil {
					log.Warn(ctx, "Codex official snapshot refresh failed", log.String("version", version), log.Cause(err))
				}
				if snapshot != nil {
					encoded, _ := json.Marshal(snapshot)
					err = s.db.System.Create().SetKey(codexSnapshotKeyPrefix + version).SetValue(string(encoded)).OnConflictColumns(system.FieldKey).DoNothing().Exec(bypass)
					if err != nil {
						log.Warn(ctx, "Codex snapshot archive write failed", log.Cause(err))
					}
				}
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if snapshot != nil {
				s.snapshots[version] = snapshot
			}
			if len(s.nextAttempt) >= 128 {
				clear(s.nextAttempt)
			}
			s.nextAttempt[version] = time.Now().Add(time.Hour)
			return nil, nil
		})
	}
	s.mu.Lock()
	exact = s.snapshots[version]
	s.mu.Unlock()
	if exact != nil || pin != "" {
		return exact
	}
	// Archive keys are small; load only the selected body, not every old catalog.
	bypass := authz.WithSystemBypass(ctx, "codex-official-snapshot-fallback")
	keys, err := s.db.System.Query().Where(system.KeyHasPrefix(codexSnapshotKeyPrefix)).Select(system.FieldKey).Strings(bypass)
	if err != nil {
		log.Warn(ctx, "Codex snapshot history unavailable", log.Cause(err))
	}
	versions := make([]string, 0, len(keys)+2)
	s.mu.Lock()
	for v := range s.snapshots {
		versions = append(versions, v)
	}
	s.mu.Unlock()
	for _, key := range keys {
		v := strings.TrimPrefix(key, codexSnapshotKeyPrefix)
		if validCodexSnapshotVersion(v) {
			versions = append(versions, v)
		}
	}
	sort.Slice(versions, func(i, j int) bool { return compareCodexVersions(versions[i], versions[j]) > 0 })
	for _, v := range versions {
		if compareCodexVersions(v, clientVersion) > 0 {
			continue
		}
		s.mu.Lock()
		snapshot := s.snapshots[v]
		s.mu.Unlock()
		if snapshot != nil {
			return snapshot
		}
		snapshot, err = s.readArchive(bypass, v)
		if err != nil {
			log.Warn(ctx, "Codex historical snapshot invalid", log.String("version", v), log.Cause(err))
			continue
		}
		if snapshot != nil {
			s.mu.Lock()
			s.snapshots[v] = snapshot
			s.mu.Unlock()
			return snapshot
		}
	}
	return nil
}

func (s *CodexSnapshotService) readArchive(ctx context.Context, version string) (*codexSnapshot, error) {
	row, err := s.db.System.Query().Where(system.KeyEQ(codexSnapshotKeyPrefix + version)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var snapshot codexSnapshot
	if err := json.Unmarshal([]byte(row.Value), &snapshot); err != nil {
		return nil, err
	}
	if snapshot.Version != version {
		return nil, fmt.Errorf("snapshot version mismatch")
	}
	return parseCodexSnapshot(snapshot.Version, snapshot.Source, snapshot.SHA256, snapshot.Body)
}

func (s *CodexSnapshotService) fetch(ctx context.Context, version string) (*codexSnapshot, error) {
	url := officialCodexSnapshotURL(version)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// No channel credentials or caller headers are forwarded to the public host.
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("official snapshot returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, codexCatalogMaxBytes+1))
	if err != nil {
		return nil, err
	}
	return parseCodexSnapshot(version, url, fmt.Sprintf("%x", sha256.Sum256(body)), body)
}
