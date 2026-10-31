package datatransfer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/schema/schematype"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/pkg/xfile"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/db"
)

type Params struct {
	fx.In
	Config    Config
	DBConfig  db.Config
	Client    *ent.Client
	System    *biz.SystemService
	Lifecycle fx.Lifecycle
}

type Service struct {
	config     Config
	dbConfig   db.Config
	client     *ent.Client
	system     *biz.SystemService
	instance   string
	initialize sync.Once
	initError  error
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	mu         sync.Mutex
	stopping   bool
}

var Module = fx.Module("database-transfer", fx.Provide(NewService))

func NewService(p Params) *Service {
	ctx, cancel := context.WithCancel(systemContext())
	s := &Service{config: p.Config, dbConfig: p.DBConfig, client: p.Client, system: p.System, ctx: ctx, cancel: cancel}
	p.Lifecycle.Append(fx.Hook{OnStart: func(context.Context) error {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Error(s.ctx, "Migration maintenance panicked", log.Any("panic", r))
				}
			}()
			timer := time.NewTicker(time.Minute)
			defer timer.Stop()
			for {
				s.recoverAndExpire()
				select {
				case <-s.ctx.Done():
					return
				case <-timer.C:
				}
			}
		}()
		return nil
	}, OnStop: func(ctx context.Context) error {
		s.mu.Lock()
		s.stopping = true
		s.cancel()
		s.mu.Unlock()
		done := make(chan struct{})
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Error(ctx, "Migration shutdown panicked", log.Any("panic", r))
				}
			}()
			s.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}})
	return s
}

func systemContext() context.Context {
	return authz.WithSystemBypass(context.Background(), "database-transfer")
}

func (s *Service) beginWork() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return context.Canceled
	}
	s.wg.Add(1)
	return nil
}

func (s *Service) initializeFiles() error {
	s.initialize.Do(func() {
		if s.initError = s.ensureDirectory(); s.initError != nil {
			return
		}
		path := filepath.Join(s.config.WorkDir, "instance-id")
		file, err := os.CreateTemp(s.config.WorkDir, ".identity-*")
		if err == nil {
			defer os.Remove(file.Name())
			_, err = file.WriteString(uuid.NewString())
			if err == nil {
				err = file.Sync()
			}
			closeErr := file.Close()
			if err == nil {
				err = closeErr
			}
			// Link publishes complete bytes without replacing another instance's
			// identity. No reader can observe a partially written identity file.
			if err == nil {
				err = os.Link(file.Name(), path)
			}
		}
		if err != nil && !errors.Is(err, os.ErrExist) {
			s.initError = err
			return
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 64 {
			s.initError = ErrMissing
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			s.initError = err
			return
		}
		parsed, err := uuid.Parse(string(data))
		if err != nil {
			s.initError = ErrMissing
			return
		}
		s.instance = parsed.String()
	})
	return s.initError
}

func (s *Service) lock(ctx context.Context) (*pgx.Conn, error) {
	conn, err := s.connect(ctx, "")
	if err != nil {
		return nil, err
	}
	var locked bool
	err = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", advisoryKey).Scan(&locked)
	if err != nil || !locked {
		conn.Close(context.Background())
		if err != nil {
			return nil, ErrProcess
		}
		return nil, ErrBusy
	}
	return conn, nil
}

func (s *Service) emptyDestination(ctx context.Context) error {
	ctx = schematype.SkipSoftDelete(ctx)
	// These checks must never use a lagging replica to authorize an import.
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	checks := []func(context.Context) (bool, error){tx.Channel.Query().Exist, tx.APIKey.Query().Exist, tx.Model.Query().Exist, tx.Request.Query().Exist, tx.UsageLog.Query().Exist, tx.UsageArchive.Query().Exist, tx.BudgetAccount.Query().Exist, tx.BudgetPosting.Query().Exist, tx.BudgetCharge.Query().Exist}
	for _, check := range checks {
		found, err := check(ctx)
		if err != nil {
			return err
		}
		if found {
			return ErrNotEmpty
		}
	}
	return nil
}

func (s *Service) checkSpace(ctx context.Context, path string, extra int64) error {
	if path == "" {
		return ErrDiskPath
	}
	total, free, err := xfile.DiskUsage(path)
	if err != nil {
		return ErrDiskPath
	}
	policy, err := s.system.StoragePolicy(ctx)
	if err != nil {
		return err
	}
	reserve := policy.Maintenance.ReserveDiskGiB * float64(gib)
	if reserve == 0 {
		reserve = max(float64(2*gib), total*.15)
	}
	if free < reserve+float64(extra) {
		return ErrSpace
	}
	return nil
}

func (s *Service) Status(ctx context.Context) (*Status, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	result := &Status{Dialect: s.dbConfig.Dialect, MaxFileBytes: int64(s.config.MaxFileGiB) * gib}
	major, err := s.checkTools(ctx)
	if err != nil {
		result.Reason = ErrorCode(err)
		return result, nil
	}
	if err := s.initializeFiles(); err != nil {
		result.Reason = "work_directory_unavailable"
		return result, nil
	}
	result.Supported, result.PostgresMajor = true, major
	if err := s.emptyDestination(ctx); err != nil {
		result.ImportReason = ErrorCode(err)
	} else if err := s.importPermission(ctx); err != nil {
		result.ImportReason = ErrorCode(err)
	} else if err := s.checkSpace(ctx, s.config.DatabaseDiskPath, 0); err != nil {
		result.ImportReason = ErrorCode(err)
	} else {
		result.ImportAllowed = true
	}
	job, _, err := s.read(ctx)
	if err != nil {
		return nil, err
	}
	result.Job = s.publicJob(job)
	return result, nil
}

func (s *Service) Job(ctx context.Context) (*Job, error) {
	job, _, err := s.read(ctx)
	if err != nil {
		return nil, err
	}
	_ = s.initializeFiles()
	return s.publicJob(job), nil
}

func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	for _, known := range []error{ErrBusy, ErrUnsupported, ErrTools, ErrNotEmpty, ErrSpace, ErrDiskPath, ErrPackage, ErrVersion, ErrMissing, ErrConflict, ErrRole, ErrTarget, ErrLostOwner, ErrProcess} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "interrupted"
	}
	return "operation_failed"
}

func (s *Service) recoverAndExpire() {
	if s.dbConfig.Dialect != "postgres" && s.dbConfig.Dialect != "postgresql" {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	if s.initializeFiles() != nil {
		return
	}
	lock, err := s.lock(ctx)
	if err != nil {
		return
	}
	defer lock.Close(context.Background())
	job, raw, err := s.read(ctx)
	if err != nil {
		return
	}
	if job.active() {
		// The database lock, not the instance name or wall clock, proves that the
		// prior executor no longer owns the job. Never replay an uncertain restore.
		job.Phase, job.Error, job.UpdatedAt = "failed", "interrupted", time.Now().UTC()
		job.Owner = uuid.NewString()
		if err := s.write(ctx, raw, job); err != nil {
			return
		}
		if job.Instance == s.instance {
			_ = s.writeFile(job)
		}
	}
	if job != nil && job.Kind == "import" && job.TargetDatabase != "" {
		if err := s.cleanupRestoreRole(ctx, job); err != nil {
			log.Warn(ctx, "Migration temporary role cleanup pending", log.String("job_id", job.ID))
			return
		}
	}
	files, err := os.ReadDir(s.config.WorkDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-time.Duration(s.config.RetentionHours) * time.Hour)
	for _, entry := range files {
		if ctx.Err() != nil {
			return
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := entry.Name()[:len(entry.Name())-5]
		path, err := s.path(id, ".json")
		if err != nil {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.Size() > 64*1024 {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var saved Job
		if json.Unmarshal(data, &saved) != nil || saved.ID != id || saved.Instance != s.instance || !saved.UpdatedAt.Before(cutoff) {
			continue
		}
		if job != nil && job.ID == saved.ID && job.active() {
			continue
		}
		if err := s.removeFiles(&saved); err != nil {
			log.Warn(ctx, "Migration artifact cleanup failed", log.String("job_id", id))
			continue
		}
		if job != nil && job.ID == saved.ID {
			job.Phase = "expired"
			job.DownloadAvailable = false
			_ = s.persist(ctx, job)
		}
	}
}
