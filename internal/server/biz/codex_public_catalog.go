package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/system"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/server/scheduler"
)

const publicCatalogURL = "https://raw.githubusercontent.com/openai/codex/main/codex-rs/models-manager/models.json"
const publicArchiveKey = "codex_public_catalog_v1"
const publicScheduleKey = "codex_public_catalog_schedule"
const publicCheckKey = "codex_public_catalog_last_check"

var ErrPublicCatalogDisabled = errors.New("public Codex catalog sync is disabled by the server configuration")
var ErrPublicCatalogInterval = errors.New("invalid catalog check interval")

type PublicCatalogSchedule struct {
	Automatic bool `json:"automatic"`
	Minutes   int  `json:"minutes"`
}

type PublicCatalogEntry struct {
	Slug          string   `json:"slug"`
	Context       any      `json:"context"`
	Maximum       any      `json:"maximum"`
	DefaultEffort any      `json:"defaultEffort"`
	Efforts       []string `json:"efforts"`
}

type PublicCatalogStatus struct {
	CanImportLocal bool   `json:"canImportLocal"`
	Source         string `json:"source"`
	URL            string `json:"url,omitempty"`
	PublicCatalogSchedule
	Blocked   bool                 `json:"blocked"`
	CheckedAt *time.Time           `json:"checkedAt"`
	Changed   int                  `json:"changed"`
	LastError string               `json:"lastError,omitempty"`
	Entries   []PublicCatalogEntry `json:"entries"`
}

type publicCatalogArchive struct {
	Source        string `json:"source,omitempty"`
	URL           string `json:"url,omitempty"`
	ClientVersion string `json:"clientVersion,omitempty"`
	Body          []byte `json:"body"`
	ETag          string `json:"etag"`
	SHA256        string `json:"sha256"`
	models        map[string]map[string]any
	entries       []PublicCatalogEntry
}

type codexPublicSync struct {
	update sync.Mutex
	mu     sync.RWMutex
	publicCatalogState
}

type publicCatalogState struct {
	archiveError string
	archive      *publicCatalogArchive
	check        publicCatalogCheck
	stamp        string // exact check row value for conditional writes, including pre-generation records
}

type publicCatalogCheck struct {
	Generation uint64    `json:"generation,omitempty"`
	CheckedAt  time.Time `json:"checkedAt"`
	Changed    int       `json:"changed"`
	LastError  string    `json:"lastError,omitempty"`
}

func (a *publicCatalogArchive) decode() error {
	digest := sha256.Sum256(a.Body)
	if hex.EncodeToString(digest[:]) != a.SHA256 {
		return errors.New("invalid public catalog digest")
	}
	models, err := parseCodexCatalog(a.Body)
	if err != nil {
		return err
	}
	if len(models) == 0 {
		return errors.New("empty public catalog")
	}
	var entries []PublicCatalogEntry
	for slug, model := range models {
		entry := PublicCatalogEntry{Slug: slug, Context: model["context_window"], Maximum: model["max_context_window"], DefaultEffort: model["default_reasoning_level"], Efforts: []string{}}
		for _, choice := range model["supported_reasoning_levels"].([]any) {
			value := choice.(map[string]any)
			entry.Efforts = append(entry.Efforts, value["effort"].(string))
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(left, right int) bool {
		a, b := models[entries[left].Slug]["priority"].(float64), models[entries[right].Slug]["priority"].(float64)
		return a < b || (a == b && entries[left].Slug < entries[right].Slug)
	})
	a.models, a.entries = models, entries
	return nil
}

func (s *CodexSnapshotService) publicBlocked() bool {
	return s.config.Disabled || s.config.Offline || s.config.Version != ""
}

func (s *CodexSnapshotService) publicSchedule(ctx context.Context) (PublicCatalogSchedule, error) {
	return authz.RunAsHost(ctx, s.db, "read-codex-public-schedule", func(host context.Context) (PublicCatalogSchedule, error) {
		host = contexts.WithPrimaryRead(host)
		row, err := s.db.System.Query().Where(system.KeyEQ(publicScheduleKey)).Only(host)
		schedule := PublicCatalogSchedule{Automatic: true, Minutes: 15}
		if ent.IsNotFound(err) {
			return schedule, nil
		}
		if err != nil {
			return schedule, err
		}
		err = json.Unmarshal([]byte(row.Value), &schedule)
		if err == nil && !slices.Contains([]int{5, 15, 30, 60}, schedule.Minutes) {
			err = ErrPublicCatalogInterval
		}
		return schedule, err
	})
}

func (s *CodexSnapshotService) SetPublicSchedule(ctx context.Context, schedule PublicCatalogSchedule) error {
	if s.publicBlocked() {
		return ErrPublicCatalogDisabled
	}
	if !slices.Contains([]int{5, 15, 30, 60}, schedule.Minutes) {
		return ErrPublicCatalogInterval
	}
	body, err := json.Marshal(schedule)
	if err != nil {
		return err
	}
	_, err = authz.RunAsHost(ctx, s.db, "set-codex-public-schedule", func(host context.Context) (struct{}, error) {
		return struct{}{}, s.db.System.Create().SetKey(publicScheduleKey).SetValue(string(body)).OnConflictColumns(system.FieldKey).UpdateNewValues().Exec(host)
	})
	return err
}

func (s *CodexSnapshotService) PublicStatus(ctx context.Context) (PublicCatalogStatus, error) {
	schedule, err := s.publicSchedule(ctx)
	result := PublicCatalogStatus{CanImportLocal: !s.config.Disabled && s.config.Version == "", Source: "official", PublicCatalogSchedule: schedule, Blocked: s.publicBlocked(), Entries: []PublicCatalogEntry{}}
	if err != nil || !result.CanImportLocal {
		return result, err
	}
	state, err := s.readPublicState(ctx)
	if err != nil {
		return result, err
	}
	if !state.check.CheckedAt.IsZero() {
		checked := state.check.CheckedAt
		result.CheckedAt = &checked
	}
	result.LastError, result.Changed = state.check.LastError, state.check.Changed
	if a := state.archive; a != nil {
		if state.archiveError != "" {
			result.LastError = state.archiveError
		} else {
			result.Entries = a.entries
		}
		result.Source, result.URL = a.source(), a.URL
	}
	return result, nil
}

func (s *CodexSnapshotService) startPublicCatalog(ctx context.Context, sched *scheduler.Scheduler) error {
	if s.config.Disabled || s.config.Version != "" {
		return nil
	}
	_, err := s.readPublicState(ctx)
	if err != nil {
		log.Warn(ctx, "Codex public catalog restore failed", log.Cause(err))
	}
	if s.config.Offline {
		return nil
	}
	return sched.Register(ctx, scheduler.TaskSpec{Name: "codex-public-catalog", Description: "检查 Codex 开源模型目录", FixRate: time.Minute}, func(ctx context.Context) {
		if err := s.checkPublicIfDue(ctx); err != nil {
			log.Warn(ctx, "Codex public catalog check failed", log.Cause(err))
		}
	})
}

func (s *CodexSnapshotService) checkPublicIfDue(ctx context.Context) error {
	schedule, err := s.publicSchedule(ctx)
	if err != nil || !schedule.Automatic {
		return err
	}
	state, err := s.readPublicState(ctx)
	if err != nil {
		return err
	}
	if state.archive.canCheckRemote() && time.Since(state.check.CheckedAt) >= time.Duration(schedule.Minutes)*time.Minute {
		return s.CheckPublicCatalog(ctx)
	}
	return nil
}

func (s *CodexSnapshotService) CheckPublicCatalog(ctx context.Context) error {
	if s.publicBlocked() {
		return ErrPublicCatalogDisabled
	}
	_, err, _ := s.flight.Do(publicArchiveKey, func() (any, error) {
		return authz.RunAsHost(ctx, s.db, "refresh-codex-public-catalog", s.checkAndSavePublic)
	})
	return err
}

func (s *CodexSnapshotService) checkAndSavePublic(ctx context.Context) (struct{}, error) {
	s.public.update.Lock()
	defer s.public.update.Unlock()
	state, err := s.readPublicState(ctx)
	if err != nil {
		return struct{}{}, err
	}
	previous := state.archive
	source := previous.source()
	if !previous.canCheckRemote() {
		return struct{}{}, nil
	}
	address := publicCatalogURL
	if source == "url" {
		address = previous.URL
	}
	next, fetchErr := s.fetchPublic(ctx, previous, source, address)
	check := publicCatalogCheck{CheckedAt: time.Now().UTC()}
	if fetchErr != nil {
		check.LastError = fetchErr.Error()
	}
	if next != nil {
		check.Changed = len(catalogChanges(previous, next))
	}
	// Subscriptions report differences until an administrator applies them.
	if source == "url" {
		next = nil
	}
	return struct{}{}, errors.Join(fetchErr, s.savePublic(ctx, state, next, check))
}

func (s *CodexSnapshotService) fetchPublic(ctx context.Context, previous *publicCatalogArchive, source, address string) (*publicCatalogArchive, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	if previous != nil && previous.ETag != "" {
		req.Header.Set("If-None-Match", previous.ETag)
	}
	client := *s.httpClient
	client.Timeout = 10 * time.Second
	if source == "url" {
		client.Transport = s.catalogTransport
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified && previous != nil {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("public catalog HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, codexCatalogMaxBytes+1))
	if err != nil {
		return nil, err
	}
	var next *publicCatalogArchive
	if source == "official" {
		digest := sha256.Sum256(body)
		next = &publicCatalogArchive{Body: body, SHA256: hex.EncodeToString(digest[:])}
		err = next.decode()
	} else {
		next, err = importedArchive(body)
		if err == nil {
			next.Source, next.URL = source, address
		}
	}
	if err != nil {
		return nil, err
	}
	next.ETag = resp.Header.Get("ETag")
	if previous != nil && next.SHA256 == previous.SHA256 && previous.ETag == next.ETag {
		return nil, nil
	}
	return next, nil
}
