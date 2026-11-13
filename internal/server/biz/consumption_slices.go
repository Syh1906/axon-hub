package biz

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	"github.com/google/uuid"

	stdsql "database/sql"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/consumptionsnapshot"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/schema/schematype"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
	"github.com/looplj/axonhub/internal/ent/user"
)

func (s *ConsumptionService) verifyConsumptionDays(ctx context.Context, projectID int, input ConsumptionFilter, known []*ConsumptionSliceRevision, location *time.Location, currency string) (bool, error) {
	opts := &stdsql.TxOptions{ReadOnly: true, Isolation: stdsql.LevelRepeatableRead}

	if s.client.Driver().Dialect() == dialect.SQLite {
		opts = &stdsql.TxOptions{}
	}

	tx, err := s.client.BeginTx(ctx, opts)
	if err != nil {
		return false, err
	}

	defer func() { _ = tx.Rollback() }()

	for offset := 0; offset < len(known); offset += 100 {

		end := min(offset+100, len(known))

		expected := map[string]string{}

		keys := make([]string, 0, end-offset)

		for _, day := range known[offset:end] {

			start := day.Start.In(location)

			key, _, err := consumptionDayKey(projectID, input, start, start.AddDate(0, 0, 1), currency)
			if err != nil {
				return false, err
			}

			keys = append(keys, key)

			expected[key] = day.Revision

		}

		jobs, err := tx.ConsumptionSnapshot.Query().Where(consumptionsnapshot.CacheKeyIn(keys...)).Select(consumptionsnapshot.FieldCacheKey, consumptionsnapshot.FieldSourceVersion, consumptionsnapshot.FieldSnapshotVersion, consumptionsnapshot.FieldFingerprint, consumptionsnapshot.FieldGeneratedAt, consumptionsnapshot.FieldCheckedAt, consumptionsnapshot.FieldRangeEnd, consumptionsnapshot.FieldPending).All(ctx)
		if err != nil {
			return false, err
		}

		if len(jobs) != len(expected) {
			return false, nil
		}

		now := time.Now().UTC()

		for _, job := range jobs {

			active := job.RangeEnd.After(now) && now.Sub(job.CheckedAt) < time.Minute

			if !active && (job.Pending || job.SourceVersion != job.SnapshotVersion) {
				return false, nil
			}

			version := consumptionDayRevision(job)

			if version != expected[job.CacheKey] {
				return false, nil
			}

		}

	}

	return true, nil
}

const consumptionSnapshotVersion = 2

const consumptionSlicePageDays = 31

type consumptionDayQuery struct {
	Version int `json:"Version"`

	Project int `json:"Project"`

	Timezone string `json:"Timezone"`

	Currency string `json:"Currency"`

	Start time.Time `json:"Start"`

	End time.Time `json:"End"`

	Filter ConsumptionFilter `json:"Filter"`
}

func (s *ConsumptionService) Slices(ctx context.Context, input ConsumptionFilter, after *time.Time, view string) (*ConsumptionSlicePage, error) {
	return s.ReadSlices(ctx, input, after, nil, nil, false, view)
}

// ReadSlices 只登记/读取日期任务；历史计算由有界 worker 执行，不占据 HTTP 查询期限。

func (s *ConsumptionService) ReadSlices(ctx context.Context, input ConsumptionFilter, after *time.Time, known []*ConsumptionSliceRevision, asOf *time.Time, verify bool, view string) (*ConsumptionSlicePage, error) {
	var projectID int

	var err error

	if view == "usage" {
		projectID, err = usageStatisticsProjectID(ctx, input)
	} else {
		projectID, err = leaderboardProjectID(ctx, input)
	}

	if err != nil {
		return nil, err
	}

	if !verify && len(known) > consumptionSlicePageDays {
		return nil, fmt.Errorf("too many known consumption days")
	}

	return authz.RunAsHost(ctx, s.client, "consumption-slices", func(ctx context.Context) (*ConsumptionSlicePage, error) {
		ctx = contexts.WithPrimaryRead(ctx)

		if _, err := consumptionEnum(string(input.Dimension), "USER", "API_KEY", "API_KEY_GROUP", "CHANNEL", "MODEL"); err != nil {
			return nil, err
		}

		if _, err := consumptionEnum(string(input.Metric), "COST", "CACHE"); err != nil {
			return nil, err
		}

		location := s.system.TimeLocation(ctx)

		now := time.Now().UTC()

		resolved := now

		if asOf != nil {

			if asOf.After(now.Add(time.Second)) {
				return nil, fmt.Errorf("invalid consumption snapshot time")
			}

			resolved = asOf.UTC()

		}

		start, end, err := consumptionRange(input, resolved, location)
		if err != nil {
			return nil, err
		}

		settings, err := s.system.GeneralSettings(ctx)
		if err != nil {
			return nil, err
		}

		page := &ConsumptionSlicePage{Slices: []*ConsumptionResult{}, Days: []*ConsumptionSliceRevision{}, RangeStart: start, RangeEnd: end, Timezone: location.String(), CurrencyCode: settings.CurrencyCode, ResolvedAt: resolved}

		if verify {

			valid, err := s.verifyConsumptionDays(ctx, projectID, input, known, location, settings.CurrencyCode)
			if err != nil {
				return nil, err
			}

			page.Pending = !valid

			return page, nil

		}

		from := start

		if from == nil {

			from, err = s.firstConsumptionDate(ctx, projectID, input.Dimension)
			if err != nil {
				return nil, err
			}

		}

		if from == nil {
			return page, nil
		}

		date := from.In(location)

		cursor := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, location)

		if after != nil {

			at := after.In(location)

			boundary := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, location)

			if !at.Equal(boundary) || at.Before(cursor) || !at.Before(end) {
				return nil, fmt.Errorf("invalid consumption date cursor")
			}

			cursor = at

		}

		revisions := make(map[int64]string, len(known))

		for _, day := range known {
			revisions[day.Start.Unix()] = day.Revision
		}

		dates := make([]time.Time, 0, consumptionSlicePageDays)

		for date := cursor; len(dates) < consumptionSlicePageDays && date.Before(end) && !date.After(resolved); date = date.AddDate(0, 0, 1) {
			dates = append(dates, date)
		}

		jobs, err := s.requestConsumptionDays(ctx, projectID, input, dates, settings.CurrencyCode, revisions)
		if err != nil {
			return nil, err
		}

		for len(page.Days) < consumptionSlicePageDays && cursor.Before(end) && !cursor.After(resolved) {

			next := cursor.AddDate(0, 0, 1)

			job := jobs[len(page.Days)]

			state := &ConsumptionSliceRevision{Start: cursor.UTC()}

			ready := consumptionDayReady(job, now)

			if ready {

				state.Revision = consumptionDayRevision(job)

				if revisions[state.Start.Unix()] != state.Revision {

					var result ConsumptionResult

					if err := json.Unmarshal(job.Payload, &result); err != nil {
						return nil, fmt.Errorf("decode consumption snapshot: %w", err)
					}

					result.ResolvedAt = resolved

					result.Revision = state.Revision

					result.RangeStart = &state.Start

					hydrated, err := s.withChannelIdentity(ctx, input, &result)
					if err != nil {
						return nil, err
					}

					hydrated, err = s.filterConsumptionSlice(ctx, input, hydrated)
					if err != nil {
						return nil, err
					}

					page.Slices = append(page.Slices, hydrated)

				}

			} else {

				if job.Failed && job.LeaseUntil.After(now) {
					return nil, fmt.Errorf("consumption summary calculation failed; retry after %s", job.LeaseUntil.Format(time.RFC3339))
				}

				page.Pending = true

			}

			page.Days = append(page.Days, state)

			cursor = next

		}

		if cursor.Before(end) && !cursor.After(resolved) {

			next := cursor.UTC()

			page.Next = &next

		}

		return page, nil
	})
}

func (s *ConsumptionService) firstConsumptionDate(ctx context.Context, projectID int, dimension ConsumptionDimension) (*time.Time, error) {
	var first *time.Time

	add := func(at time.Time) {
		if first == nil || at.Before(*first) {
			first = &at
		}
	}

	rq := s.client.Request.Query().Where(request.StatusIn(request.StatusCompleted, request.StatusFailed, request.StatusCanceled)).Select(request.FieldCreatedAt).Order(ent.Asc(request.FieldCreatedAt))

	if projectID > 0 {
		rq.Where(request.ProjectIDEQ(projectID))
	}

	r, err := rq.First(ctx)

	if err != nil && !ent.IsNotFound(err) {
		return nil, err
	}

	if r != nil {
		add(r.CreatedAt)
	}

	aq := s.client.UsageArchive.Query().Where(usagearchive.KindIn(usagearchive.KindRequest, usagearchive.KindExecution)).Select(usagearchive.FieldOccurredAt).Order(ent.Asc(usagearchive.FieldOccurredAt))

	if projectID > 0 {
		aq.Where(usagearchive.ProjectIDEQ(projectID))
	}

	a, err := aq.First(ctx)

	if err != nil && !ent.IsNotFound(err) {
		return nil, err
	}

	if a != nil {
		add(a.OccurredAt)
	}

	if dimension == ConsumptionDimensionChannel {

		eq := s.client.RequestExecution.Query().Where(requestexecution.StatusIn(requestexecution.StatusCompleted, requestexecution.StatusFailed, requestexecution.StatusCanceled)).Select(requestexecution.FieldCreatedAt).Order(ent.Asc(requestexecution.FieldCreatedAt))

		if projectID > 0 {
			eq.Where(requestexecution.ProjectIDEQ(projectID))
		}

		e, err := eq.First(ctx)

		if err != nil && !ent.IsNotFound(err) {
			return nil, err
		}

		if e != nil {
			add(e.CreatedAt)
		}

	}

	return first, nil
}

func consumptionDayReady(job *ent.ConsumptionSnapshot, now time.Time) bool {
	return job.GeneratedAt.After(time.Unix(0, 0)) && ((!job.Pending && job.SourceVersion == job.SnapshotVersion) || (job.RangeEnd.After(now) && now.Sub(job.CheckedAt) < time.Minute))
}

func consumptionDayRevision(job *ent.ConsumptionSnapshot) string {
	return job.Fingerprint + ":" + job.GeneratedAt.UTC().Format(time.RFC3339Nano)
}

// 一页只读一次轻量状态；心跳最多每两秒批量更新，只读取缺失版本的载荷。

func (s *ConsumptionService) requestConsumptionDays(ctx context.Context, projectID int, input ConsumptionFilter, dates []time.Time, currency string, revisions map[int64]string) ([]*ent.ConsumptionSnapshot, error) {
	if len(dates) == 0 {
		return nil, nil
	}

	now := time.Now().UTC()

	// 登记任务使用普通写事务，允许并发首次建立同一日期；最终版本复核

	// 另用只读一致快照。载荷与对应版本在同一条 SELECT 中读取。

	opts := &stdsql.TxOptions{Isolation: stdsql.LevelReadCommitted}

	if s.client.Driver().Dialect() == dialect.SQLite {
		opts = &stdsql.TxOptions{}
	}

	tx, err := s.client.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}

	defer func() { _ = tx.Rollback() }()

	client := tx.Client()

	keys, queries := make([]string, len(dates)), make([][]byte, len(dates))

	for i, start := range dates {

		keys[i], queries[i], err = consumptionDayKey(projectID, input, start, start.AddDate(0, 0, 1), currency)
		if err != nil {
			return nil, err
		}

	}

	fields := []string{
		consumptionsnapshot.FieldID, consumptionsnapshot.FieldCacheKey, consumptionsnapshot.FieldRangeStart, consumptionsnapshot.FieldRangeEnd,

		consumptionsnapshot.FieldGeneratedAt, consumptionsnapshot.FieldCheckedAt, consumptionsnapshot.FieldSourceVersion, consumptionsnapshot.FieldSnapshotVersion, consumptionsnapshot.FieldFingerprint,

		consumptionsnapshot.FieldPending, consumptionsnapshot.FieldFailed, consumptionsnapshot.FieldLeaseUntil, consumptionsnapshot.FieldRequestedAt,
	}

	read := func() ([]*ent.ConsumptionSnapshot, error) {
		return client.ConsumptionSnapshot.Query().Where(consumptionsnapshot.CacheKeyIn(keys...)).Select(fields...).All(ctx)
	}

	jobs, err := read()
	if err != nil {
		return nil, err
	}

	byKey := map[string]*ent.ConsumptionSnapshot{}

	for _, job := range jobs {
		byKey[job.CacheKey] = job
	}

	var create []*ent.ConsumptionSnapshotCreate

	for i, key := range keys {
		if byKey[key] == nil {
			create = append(create, client.ConsumptionSnapshot.Create().SetCacheKey(key).SetProjectID(projectID).SetRangeStart(dates[i].UTC()).SetRangeEnd(dates[i].AddDate(0, 0, 1).UTC()).
				SetGeneratedAt(time.Unix(0, 0).UTC()).SetRequestedAt(now).SetQuery(queries[i]).SetSourceVersion(uuid.NewString()))
		}
	}

	if len(create) > 0 {

		if err := client.ConsumptionSnapshot.CreateBulk(create...).OnConflictColumns(consumptionsnapshot.FieldCacheKey).Ignore().Exec(ctx); err != nil {
			return nil, err
		}

		jobs, err = read()
		if err != nil {
			return nil, err
		}

		for _, job := range jobs {
			byKey[job.CacheKey] = job
		}

	}

	var touch, dirty, payload []int

	ordered := make([]*ent.ConsumptionSnapshot, len(keys))

	for i, key := range keys {

		job := byKey[key]

		ordered[i] = job

		activeExpired := job.RangeEnd.After(now) && now.Sub(job.CheckedAt) >= time.Minute

		needs := job.SourceVersion != job.SnapshotVersion || activeExpired || now.Sub(job.CheckedAt) >= 5*time.Minute

		if needs && !job.Pending {

			dirty = append(dirty, job.ID)

			job.Pending = true

		}

		if now.Sub(job.RequestedAt) >= 2*time.Second {
			touch = append(touch, job.ID)
		}

		version := consumptionDayRevision(job)

		if consumptionDayReady(job, now) && revisions[job.RangeStart.Unix()] != version {
			payload = append(payload, job.ID)
		}

	}

	if len(dirty) > 0 {
		if err := client.ConsumptionSnapshot.Update().Where(consumptionsnapshot.IDIn(dirty...)).SetPending(true).Exec(ctx); err != nil {
			return nil, err
		}
	}

	if len(touch) > 0 {
		if err := client.ConsumptionSnapshot.Update().Where(consumptionsnapshot.IDIn(touch...)).SetRequestedAt(now).Exec(ctx); err != nil {
			return nil, err
		}
	}

	if len(payload) > 0 {

		data, err := client.ConsumptionSnapshot.Query().Where(consumptionsnapshot.IDIn(payload...)).Select(append(fields, consumptionsnapshot.FieldPayload)...).All(ctx)
		if err != nil {
			return nil, err
		}

		for _, job := range data {
			*byKey[job.CacheKey] = *job
		}

	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}

	return ordered, nil
}

func consumptionDayKey(projectID int, input ConsumptionFilter, start, end time.Time, currency string) (string, []byte, error) {
	input.Search = nil

	if input.Dimension == ConsumptionDimensionAPIKey || input.Dimension == ConsumptionDimensionAPIKeyGroup {
		input.GroupRef = nil
	}

	from, to := start.Format("2006-01-02"), end.AddDate(0, 0, -1).Format("2006-01-02")

	input.Period, input.StartDate, input.EndDate = ConsumptionPeriodCustom, &from, &to

	input.Uncached, input.DailySnapshot = true, true

	input.ProjectID = nil

	query := consumptionDayQuery{Version: consumptionSnapshotVersion, Project: projectID, Timezone: start.Location().String(), Currency: currency, Start: start.UTC(), End: end.UTC(), Filter: input}

	bytes, err := json.Marshal(query)
	if err != nil {
		return "", nil, err
	}

	key := fmt.Sprintf("%x", sha256.Sum256(bytes))

	return key, bytes, nil
}

func (s *ConsumptionService) filterConsumptionSlice(ctx context.Context, input ConsumptionFilter, result *ConsumptionResult) (*ConsumptionResult, error) {
	search := ""

	if input.Search != nil {
		search = strings.ToLower(strings.TrimSpace(*input.Search))
	}

	if search == "" && input.GroupRef == nil {
		return result, nil
	}

	filtered := *result

	filtered.Entries = make([]*ConsumptionEntry, 0, len(result.Entries))

	ids := make([]int, 0, len(result.Entries))

	for _, entry := range result.Entries {
		ids = append(ids, entry.ID.ID)
	}

	allowed := map[int]bool{}

	switch input.Dimension {

	case ConsumptionDimensionAPIKey:

		q := s.client.APIKey.Query().Where(apikey.IDIn(ids...)).Select(apikey.FieldID, apikey.FieldName, apikey.FieldGroupRef)

		if input.GroupRef != nil {
			q.Where(apikey.GroupRefEQ(*input.GroupRef))
		}

		keys, err := q.All(schematype.SkipSoftDelete(ctx))
		if err != nil {
			return nil, err
		}

		for _, key := range keys {
			if strings.Contains(strings.ToLower(key.Name), search) {
				allowed[key.ID] = true
			}
		}

	case ConsumptionDimensionUser:

		users, err := s.client.User.Query().Where(user.IDIn(ids...)).Select(user.FieldID, user.FieldFirstName, user.FieldLastName, user.FieldEmail).All(ctx)
		if err != nil {
			return nil, err
		}

		for _, owner := range users {
			for _, value := range []string{owner.FirstName, owner.LastName, owner.Email, strings.TrimSpace(owner.FirstName + " " + owner.LastName)} {
				if strings.Contains(strings.ToLower(value), search) {
					allowed[owner.ID] = true
				}
			}
		}

	default:

		for _, entry := range result.Entries {
			if (input.GroupRef == nil || entry.ID.ID == *input.GroupRef) && strings.Contains(strings.ToLower(entry.Name), search) {
				allowed[entry.ID.ID] = true
			}
		}

	}

	for _, entry := range result.Entries {
		if allowed[entry.ID.ID] {
			filtered.Entries = append(filtered.Entries, entry)
		}
	}

	if input.Dimension == ConsumptionDimensionModel {

		filtered.ModelEntries = nil

		for _, model := range result.ModelEntries {
			if strings.Contains(strings.ToLower(model.Model), search) {
				filtered.ModelEntries = append(filtered.ModelEntries, model)
			}
		}

	}

	return &filtered, nil
}
