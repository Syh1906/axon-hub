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
	"reflect"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/looplj/axonhub/internal/authz"
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
	PublicCatalogSchedule
	Blocked   bool                 `json:"blocked"`
	CheckedAt *time.Time           `json:"checkedAt"`
	Changed   int                  `json:"changed"`
	LastError string               `json:"lastError,omitempty"`
	Entries   []PublicCatalogEntry `json:"entries"`
}

type publicCatalogArchive struct {
	Body    []byte `json:"body"`
	ETag    string `json:"etag"`
	SHA256  string `json:"sha256"`
	models  map[string]map[string]any
	entries []PublicCatalogEntry
}

type codexPublicSync struct {
	mu      sync.RWMutex
	archive *publicCatalogArchive
	check   publicCatalogCheck
}

type publicCatalogCheck struct {
	CheckedAt time.Time `json:"checkedAt"`
	Changed   int       `json:"changed"`
	LastError string    `json:"lastError,omitempty"`
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
	result := PublicCatalogStatus{PublicCatalogSchedule: schedule, Blocked: s.publicBlocked(), Entries: []PublicCatalogEntry{}}
	s.public.mu.RLock()
	defer s.public.mu.RUnlock()
	if !s.public.check.CheckedAt.IsZero() {
		checked := s.public.check.CheckedAt
		result.CheckedAt = &checked
	}
	result.LastError, result.Changed = s.public.check.LastError, s.public.check.Changed
	if a := s.public.archive; a != nil {
		result.Entries = a.entries
	}
	return result, err
}

func (s *CodexSnapshotService) startPublicCatalog(ctx context.Context, sched *scheduler.Scheduler) error {
	if s.config.Disabled || s.config.Version != "" {
		return nil
	}
	_, err := authz.RunAsHost(ctx, s.db, "restore-codex-public-catalog", func(host context.Context) (struct{}, error) {
		rows, err := s.db.System.Query().Where(system.KeyIn(publicArchiveKey, publicCheckKey)).All(host)
		if err != nil {
			return struct{}{}, err
		}
		var archive *publicCatalogArchive
		var check publicCatalogCheck
		for _, row := range rows {
			if row.Key == publicCheckKey {
				if err := json.Unmarshal([]byte(row.Value), &check); err != nil {
					return struct{}{}, err
				}
			} else {
				var saved publicCatalogArchive
				if err := json.Unmarshal([]byte(row.Value), &saved); err != nil {
					return struct{}{}, err
				}
				if err := saved.decode(); err != nil {
					return struct{}{}, err
				}
				archive = &saved
			}
		}
		s.public.mu.Lock()
		s.public.archive, s.public.check = archive, check
		s.public.mu.Unlock()
		return struct{}{}, nil
	})
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
	s.public.mu.RLock()
	due := time.Since(s.public.check.CheckedAt) >= time.Duration(schedule.Minutes)*time.Minute
	s.public.mu.RUnlock()
	if due {
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
	s.public.mu.RLock()
	previous := s.public.archive
	s.public.mu.RUnlock()
	next, fetchErr := s.fetchPublic(ctx, previous)
	check := publicCatalogCheck{CheckedAt: time.Now().UTC()}
	if fetchErr != nil {
		check.LastError = fetchErr.Error()
	}
	var writes []*ent.SystemCreate
	if next != nil {
		for slug, entry := range next.models {
			if previous == nil || !reflect.DeepEqual(entry, previous.models[slug]) {
				check.Changed++
			}
		}
		if previous != nil {
			for slug := range previous.models {
				if next.models[slug] == nil {
					check.Changed++
				}
			}
		}
		raw, err := json.Marshal(next)
		if err != nil {
			return struct{}{}, err
		}
		writes = append(writes, s.db.System.Create().SetKey(publicArchiveKey).SetValue(string(raw)))
	}
	raw, err := json.Marshal(check)
	if err != nil {
		return struct{}{}, err
	}
	writes = append(writes, s.db.System.Create().SetKey(publicCheckKey).SetValue(string(raw)))
	if err := s.db.System.CreateBulk(writes...).OnConflictColumns(system.FieldKey).UpdateNewValues().Exec(ctx); err != nil {
		return struct{}{}, errors.Join(fetchErr, err)
	}
	s.public.mu.Lock()
	if next != nil {
		s.public.archive = next
	}
	s.public.check = check
	s.public.mu.Unlock()
	return struct{}{}, fetchErr
}

func (s *CodexSnapshotService) fetchPublic(ctx context.Context, previous *publicCatalogArchive) (*publicCatalogArchive, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, publicCatalogURL, nil)
	if err != nil {
		return nil, err
	}
	if previous != nil && previous.ETag != "" {
		req.Header.Set("If-None-Match", previous.ETag)
	}
	client := *s.httpClient
	client.Timeout = 10 * time.Second
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
	digest := sha256.Sum256(body)
	next := &publicCatalogArchive{Body: body, ETag: resp.Header.Get("ETag"), SHA256: hex.EncodeToString(digest[:])}
	if previous != nil && next.SHA256 == previous.SHA256 && previous.ETag == next.ETag {
		return nil, nil
	}
	if err := next.decode(); err != nil {
		return nil, err
	}
	return next, nil
}
