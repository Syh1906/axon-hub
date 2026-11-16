package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/statisticsrevision"
)

// Source writes and revision changes commit together. Refresh checks these
// small revision rows; it never downloads historical requests to hash them.
func (s *ConsumptionService) consumptionFingerprint(ctx context.Context, query consumptionDayQuery) (string, error) {
	rows, err := readStatisticsRevisions(ctx, s.client, query.Project, query.Start, query.End)
	if err != nil {
		return "", err
	}
	return statisticsFingerprint(rows, query.Start, query.End), nil
}

func readStatisticsRevisions(ctx context.Context, client *ent.Client, projectID int, start, end time.Time) ([]*ent.StatisticsRevision, error) {
	q := client.StatisticsRevision.Query().Where(statisticsrevision.Or(
		statisticsrevision.OccurredAtEQ(time.Unix(0, 0).UTC()),
		statisticsrevision.And(statisticsrevision.OccurredAtGTE(start.UTC().Truncate(time.Hour)), statisticsrevision.OccurredAtLT(end)),
	))
	if projectID > 0 {
		q.Where(statisticsrevision.ProjectIDIn(0, projectID))
	}
	return q.Select(statisticsrevision.FieldProjectID, statisticsrevision.FieldOccurredAt, statisticsrevision.FieldVersion).
		Order(ent.Asc(statisticsrevision.FieldProjectID), ent.Asc(statisticsrevision.FieldOccurredAt)).All(ctx)
}

func statisticsFingerprint(rows []*ent.StatisticsRevision, start, end time.Time) string {
	hash := sha256.New()
	metadata := sha256.New()
	encoder := json.NewEncoder(hash)
	metaEncoder := json.NewEncoder(metadata)
	_ = encoder.Encode(consumptionSnapshotVersion)
	_ = metaEncoder.Encode(consumptionSnapshotVersion)
	for _, row := range rows {
		if row.OccurredAt.Equal(time.Unix(0, 0)) {
			_ = metaEncoder.Encode([]any{row.ProjectID, row.Version})
		} else if !row.OccurredAt.Before(start.UTC().Truncate(time.Hour)) && row.OccurredAt.Before(end) {
			_ = encoder.Encode([]any{row.ProjectID, row.OccurredAt.Unix(), row.Version})
		}
	}
	return hex.EncodeToString(metadata.Sum(nil)[:16]) + ":" + hex.EncodeToString(hash.Sum(nil)[:15])
}

func sameStatisticsMetadata(a, b string) bool {
	return len(a) == 63 && len(b) == 63 && a[:32] == b[:32]
}
