package gc

import (
	"context"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"time"

	"github.com/looplj/axonhub/internal/pkg/xfile"
)

type StorageOverview struct {
	Dialect           string          `json:"dialect"`
	DatabaseBytes     *float64        `json:"database_bytes,omitempty"`
	Tables            []*StorageTable `json:"tables"`
	DiskTotalBytes    *float64        `json:"disk_total_bytes,omitempty"`
	DiskFreeBytes     *float64        `json:"disk_free_bytes,omitempty"`
	DiskSource        string          `json:"disk_source"`
	ReserveBytes      *float64        `json:"reserve_bytes,omitempty"`
	Level             string          `json:"level"`
	SampledAt         time.Time       `json:"sampled_at"`
	DatabaseAvailable bool            `json:"database_available"`
}

type StorageTable struct {
	Name          string  `json:"name"`
	Bytes         float64 `json:"bytes"`
	EstimatedRows float64 `json:"estimated_rows"`
}

func (w *Worker) StorageOverview(ctx context.Context) (*StorageOverview, error) {
	w.storageMu.Lock()
	defer w.storageMu.Unlock()
	if w.storageCache != nil && time.Since(w.storageCache.SampledAt) < 30*time.Second {
		return w.storageCache, nil
	}
	policy, err := w.SystemService.StoragePolicy(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result := &StorageOverview{Dialect: w.Ent.Driver().Dialect(), Tables: []*StorageTable{}, SampledAt: time.Now().UTC(), Level: "unknown", DiskSource: "application"}
	rows := &sql.Rows{}
	var query string
	switch result.Dialect {
	case dialect.Postgres:
		query = "SELECT relname AS name, pg_total_relation_size(relid)::double precision AS bytes, n_live_tup::double precision AS estimated_rows FROM pg_stat_user_tables WHERE schemaname=current_schema() ORDER BY pg_total_relation_size(relid) DESC LIMIT 15"
	case dialect.MySQL:
		query = "SELECT TABLE_NAME AS name, COALESCE(DATA_LENGTH+INDEX_LENGTH,0) AS bytes, COALESCE(TABLE_ROWS,0) AS estimated_rows FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() ORDER BY DATA_LENGTH+INDEX_LENGTH DESC LIMIT 15"
	}
	if query != "" && w.Ent.Driver().Query(ctx, query, []any{}, rows) == nil {
		err = sql.ScanSlice(rows, &result.Tables)
		rows.Close()
		result.DatabaseAvailable = err == nil
	}
	var totals []struct {
		Bytes float64 `json:"bytes"`
	}
	switch result.Dialect {
	case dialect.Postgres:
		query = "SELECT pg_database_size(current_database())::double precision AS bytes"
	case dialect.MySQL:
		query = "SELECT COALESCE(SUM(DATA_LENGTH+INDEX_LENGTH),0) AS bytes FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE()"
	case dialect.SQLite:
		query = "SELECT page_count * page_size AS bytes FROM pragma_page_count(), pragma_page_size()"
	}
	rows = &sql.Rows{}
	if query != "" && w.Ent.Driver().Query(ctx, query, []any{}, rows) == nil {
		err = sql.ScanSlice(rows, &totals)
		rows.Close()
		if err == nil && len(totals) > 0 {
			result.DatabaseBytes = &totals[0].Bytes
			result.DatabaseAvailable = true
		}
	}
	path := policy.Maintenance.DiskPath
	if path != "" {
		result.DiskSource = "configured"
	} else {
		path = "."
	}
	if total, free, err := xfile.DiskUsage(path); err == nil && total > 0 {
		result.DiskTotalBytes, result.DiskFreeBytes = &total, &free
		reserve := policy.Maintenance.ReserveDiskGiB * 1024 * 1024 * 1024
		if reserve == 0 {
			reserve = max(2*1024*1024*1024, total*0.15)
		}
		result.ReserveBytes = &reserve
		usedPercent := (total - free) * 100 / total
		result.Level = "ok"
		if usedPercent >= float64(policy.Maintenance.DiskWarningPercent) || free < reserve {
			result.Level = "warning"
		}
		if usedPercent >= float64(policy.Maintenance.DiskCriticalPercent) || free < reserve/2 {
			result.Level = "critical"
		}
	}
	w.storageCache = result
	return result, nil
}
