package biz

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/system"
)

func (s *CodexSnapshotService) cachedPublicState() publicCatalogState {
	s.public.mu.RLock()
	defer s.public.mu.RUnlock()
	return s.public.publicCatalogState
}

// The small check row changes in the same transaction as the archive. Most reads
// can reuse the decoded models without transferring the whole catalog.
func (s *CodexSnapshotService) readPublicState(ctx context.Context) (publicCatalogState, error) {
	return authz.RunAsHost(ctx, s.db, "read-codex-public-catalog", func(host context.Context) (publicCatalogState, error) {
		host = contexts.WithPrimaryRead(host)
		row, err := s.db.System.Query().Where(system.KeyEQ(publicCheckKey)).Select(system.FieldValue).Only(host)
		var stamp string
		switch {
		case err == nil:
			stamp = row.Value
		case !ent.IsNotFound(err):
			return publicCatalogState{}, err
		}
		if current := s.cachedPublicState(); current.stamp == stamp {
			return current, nil
		}

		s.public.mu.Lock()
		defer s.public.mu.Unlock()
		if s.public.stamp == stamp {
			return s.public.publicCatalogState, nil
		}
		// Read both rows in one statement: another replica may have committed since
		// the initial stamp lookup.
		rows, err := s.db.System.Query().Where(system.KeyIn(publicArchiveKey, publicCheckKey)).All(host)
		if err != nil {
			return publicCatalogState{}, err
		}
		var current publicCatalogState
		for _, row := range rows {
			switch row.Key {
			case publicCheckKey:
				if err := json.Unmarshal([]byte(row.Value), &current.check); err != nil {
					return publicCatalogState{}, err
				}
				current.stamp = row.Value
			case publicArchiveKey:
				var saved publicCatalogArchive
				err := json.Unmarshal([]byte(row.Value), &saved)
				if err == nil {
					err = saved.decode()
				} else {
					saved = publicCatalogArchive{Source: "unavailable"}
				}
				if err != nil {
					// Keep the source and the compare-and-swap stamp available for repair.
					// Never reuse unverified model data or a conditional GET for corrupt bytes.
					current.archiveError = err.Error()
					saved = publicCatalogArchive{Source: saved.Source, URL: saved.URL, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(row.Value)))}
				}
				current.archive = &saved
			}
		}
		s.public.publicCatalogState = current
		return current, nil
	})
}

func (s *CodexSnapshotService) savePublic(ctx context.Context, previous publicCatalogState, next *publicCatalogArchive, check publicCatalogCheck) error {
	check.Generation = previous.check.Generation + 1
	raw, err := json.Marshal(check)
	if err != nil {
		return err
	}
	stamp := string(raw)
	err = s.RunInTransaction(ctx, func(txCtx context.Context) error {
		rows := s.entFromContext(txCtx).System
		// Reserve the observed generation before writing either the archive or a
		// check-only result. A competing initial insert also invalidates our view.
		if previous.stamp == "" {
			err := rows.Create().SetKey(publicCheckKey).SetValue(stamp).Exec(txCtx)
			if ent.IsConstraintError(err) {
				return ErrCatalogPreviewChanged
			}
			if err != nil {
				return err
			}
		} else {
			count, err := rows.Update().Where(system.KeyEQ(publicCheckKey), system.ValueEQ(previous.stamp)).SetValue(stamp).Save(txCtx)
			if err != nil {
				return err
			}
			if count == 0 {
				return ErrCatalogPreviewChanged
			}
		}
		if next == nil {
			return nil
		}
		body, err := json.Marshal(next)
		if err != nil {
			return err
		}
		return rows.Create().SetKey(publicArchiveKey).SetValue(string(body)).OnConflictColumns(system.FieldKey).UpdateNewValues().Exec(txCtx)
	})
	if err != nil {
		return err
	}

	current := publicCatalogState{archive: next, check: check, stamp: stamp}
	if next == nil {
		current.archive = previous.archive
		current.archiveError = previous.archiveError
	}
	s.public.mu.Lock()
	// A concurrent reader may already have loaded a later committed generation.
	if s.public.check.Generation <= check.Generation {
		s.public.publicCatalogState = current
	}
	s.public.mu.Unlock()
	return nil
}
