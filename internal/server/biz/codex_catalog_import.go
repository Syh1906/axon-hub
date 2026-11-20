package biz

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"time"

	"github.com/looplj/axonhub/internal/authz"
)

var ErrCatalogImport = errors.New("invalid catalog import")
var ErrCatalogPreviewChanged = errors.New("catalog changed since preview; preview again")

func (a *publicCatalogArchive) source() string {
	if a == nil || a.Source == "" {
		return "official"
	}
	return a.Source
}

func (a *publicCatalogArchive) canCheckRemote() bool {
	source := a.source()
	return source == "official" || source == "url"
}

const importedCatalogKind = "imported_catalog"

func isPublicCatalogKind(kind string) bool {
	return kind == "official_public" || kind == importedCatalogKind
}

type CatalogImport struct {
	Source    string          `json:"source"`
	URL       string          `json:"url,omitempty"`
	Subscribe bool            `json:"subscribe,omitempty"`
	Catalog   json.RawMessage `json:"catalog,omitempty"`
	Preview   string          `json:"preview,omitempty"`
}

type catalogVersionChange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type CatalogChange struct {
	MinimumVersion *catalogVersionChange `json:"minimumVersion,omitempty"`
	Slug           string                `json:"slug"`
	Action         string                `json:"action"`
	Fields         []string              `json:"fields,omitempty"`
}
type CatalogPreview struct {
	Revision string          `json:"revision"`
	Changes  []CatalogChange `json:"changes"`
}

func importedArchive(body []byte) (*publicCatalogArchive, error) {
	var payload struct {
		Models        json.RawMessage `json:"models"`
		ClientVersion string          `json:"client_version,omitempty"`
	}
	if len(body) > codexCatalogMaxBytes {
		return nil, fmt.Errorf("%w: catalog exceeds 1 MiB", ErrCatalogImport)
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON", ErrCatalogImport)
	}
	if payload.ClientVersion != "" && !validCodexSnapshotVersion(payload.ClientVersion) {
		return nil, fmt.Errorf("%w: invalid client_version", ErrCatalogImport)
	}
	// Keep only the catalog envelope; device and account cache identifiers are not needed.
	clean, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	a := &publicCatalogArchive{Source: "manual", Body: clean, SHA256: fmt.Sprintf("%x", sha256.Sum256(clean)), ClientVersion: payload.ClientVersion}
	if err := a.decode(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCatalogImport, err)
	}
	return a, nil
}

func catalogChanges(previous, next *publicCatalogArchive) []CatalogChange {
	var before map[string]map[string]any
	if previous != nil {
		before = previous.models
	}
	changes := []CatalogChange{}
	for slug, m := range next.models {
		old := before[slug]
		if old == nil {
			changes = append(changes, CatalogChange{Slug: slug, Action: "add"})
			continue
		}
		var fields []string
		for key, v := range m {
			if prior, ok := old[key]; !ok || !reflect.DeepEqual(prior, v) {
				fields = append(fields, key)
			}
		}
		for key := range old {
			if _, ok := m[key]; !ok {
				fields = append(fields, key)
			}
		}
		var minimumVersion *catalogVersionChange
		if from, to := previous.minimumClientVersion(old), next.minimumClientVersion(m); from != to {
			minimumVersion = &catalogVersionChange{From: from, To: to}
			if !slices.Contains(fields, "minimal_client_version") {
				fields = append(fields, "minimal_client_version")
			}
		}
		if len(fields) > 0 {
			slices.Sort(fields)
			changes = append(changes, CatalogChange{Slug: slug, Action: "update", Fields: fields, MinimumVersion: minimumVersion})
		}
	}
	for slug := range before {
		if next.models[slug] == nil {
			changes = append(changes, CatalogChange{Slug: slug, Action: "remove"})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Slug < changes[j].Slug })
	return changes
}

func (s *CodexSnapshotService) importCandidate(ctx context.Context, input CatalogImport) (*publicCatalogArchive, error) {
	switch input.Source {
	case "manual":
		return importedArchive(input.Catalog)
	case "official":
		return s.fetchPublic(ctx, nil, "official", publicCatalogURL)
	case "url":
		u, err := url.Parse(input.URL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return nil, fmt.Errorf("%w: expected a public HTTPS JSON URL", ErrCatalogImport)
		}
		a, err := s.fetchPublic(ctx, nil, "url", u.String())
		if err == nil && !input.Subscribe {
			a.Source = "manual"
			a.URL = ""
		}
		return a, err
	default:
		return nil, fmt.Errorf("%w: unknown source", ErrCatalogImport)
	}
}

// Preview and apply use the same parsing path. Re-fetching a URL at apply time
// is safe only if both the source content and the active baseline still match.
func (s *CodexSnapshotService) ImportCatalog(ctx context.Context, input CatalogImport, apply bool) (CatalogPreview, error) {
	if s.config.Disabled || s.config.Version != "" || (s.config.Offline && input.Source != "manual") {
		return CatalogPreview{}, ErrPublicCatalogDisabled
	}
	next, err := s.importCandidate(ctx, input)
	if err != nil {
		return CatalogPreview{}, err
	}
	s.public.update.Lock()
	defer s.public.update.Unlock()
	state, err := s.readPublicState(ctx)
	if err != nil {
		return CatalogPreview{}, err
	}
	previous := state.archive
	revision := func(a *publicCatalogArchive) string {
		if a == nil {
			return ""
		}
		return a.source() + "\n" + a.URL + "\n" + a.SHA256
	}
	preview := CatalogPreview{Changes: catalogChanges(previous, next), Revision: fmt.Sprintf("%x", sha256.Sum256([]byte(revision(previous)+"\n"+revision(next))))}
	if !apply {
		return preview, nil
	}
	if input.Preview != preview.Revision {
		return CatalogPreview{}, ErrCatalogPreviewChanged
	}
	_, err = authz.RunAsHost(ctx, s.db, "apply-codex-catalog", func(host context.Context) (struct{}, error) {
		check := publicCatalogCheck{CheckedAt: time.Now().UTC()}
		if next.source() != "url" {
			check.Changed = len(preview.Changes)
		}
		return struct{}{}, s.savePublic(host, state, next, check)
	})
	return preview, err
}
