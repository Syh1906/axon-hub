package datatransfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/looplj/axonhub/internal/build"
	"github.com/looplj/axonhub/internal/log"
)

func (s *Service) preflight(ctx context.Context) (int, error) {
	major, err := s.checkTools(ctx)
	if err != nil {
		return 0, err
	}
	if err := s.initializeFiles(); err != nil {
		return 0, err
	}
	return major, nil
}

func (s *Service) newJob(ctx context.Context, id, kind, fingerprint, phase string) (*Job, bool, error) {
	if _, err := s.path(id, ".dump"); err != nil {
		return nil, false, ErrPackage
	}
	old, raw, err := s.read(ctx)
	if err != nil {
		return nil, false, err
	}
	if old != nil {
		if old.ID == id {
			if old.Fingerprint != fingerprint || old.Kind != kind {
				return nil, false, ErrConflict
			}
			if old.active() {
				old.Phase, old.Error, old.Owner = "failed", "interrupted", uuid.NewString()
				if err := s.write(ctx, raw, old); err != nil {
					return nil, false, err
				}
			}
			return old, false, nil
		}
		if old.active() {
			old.Phase, old.Error, old.Owner = "failed", "interrupted", uuid.NewString()
			if err := s.write(ctx, raw, old); err != nil {
				return nil, false, err
			}
			_, raw, err = s.read(ctx)
			if err != nil {
				return nil, false, err
			}
		}
		if old.Phase == "ready" || old.Phase == "awaiting_upload" {
			return nil, false, ErrBusy
		}
		if old.Kind == "import" && old.TargetDatabase != "" {
			if err := s.cleanupRestoreRole(ctx, old); err != nil {
				return nil, false, err
			}
		}
		if old.Instance == s.instance {
			if err := s.removeFiles(old); err != nil {
				return nil, false, err
			}
		}
	}
	now := time.Now().UTC()
	job := &Job{ID: id, Kind: kind, Fingerprint: fingerprint, Phase: phase, CreatedAt: now, UpdatedAt: now, Instance: s.instance, Owner: uuid.NewString()}
	if err := s.write(ctx, raw, job); err != nil {
		return nil, false, err
	}
	if err := s.writeFile(job); err != nil {
		return nil, false, err
	}
	return job, true, nil
}

func (s *Service) StartExport(ctx context.Context, id string) (*Job, error) {
	major, err := s.preflight(ctx)
	if err != nil {
		return nil, err
	}
	lock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	job, created, err := s.newJob(ctx, id, "export", "full", "exporting")
	if err != nil {
		lock.Close(context.Background())
		return nil, err
	}
	if !created || job.Instance != s.instance {
		lock.Close(context.Background())
		return s.publicJob(job), nil
	}
	// A fresh owner can only be created while holding the session lock. Duplicate
	// requests cannot launch a second process; the lock is kept by the worker.
	response := s.publicJob(job)
	if err := s.launch(lock, job, func(workCtx context.Context, progress func(int64)) error {
		conn, err := s.connect(workCtx, "")
		if err != nil {
			return err
		}
		var size int64
		err = conn.QueryRow(workCtx, "SELECT pg_database_size(current_database())").Scan(&size)
		conn.Close(context.Background())
		if err != nil {
			return ErrProcess
		}
		if err := s.checkSpace(workCtx, s.config.WorkDir, size+size/5); err != nil {
			return err
		}
		path, err := s.path(job.ID, ".dump")
		if err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		defer file.Close()
		hash := sha256.New()
		writer := &progressWriter{ctx: workCtx, target: io.MultiWriter(file, hash), max: int64(s.config.MaxFileGiB) * gib, progress: progress}
		if _, err := s.dump(workCtx, writer); err != nil {
			return err
		}
		if err := file.Sync(); err != nil {
			return err
		}
		job.Manifest = &Manifest{Format: packageFormat, AppVersion: build.Version, PostgresMajor: major, DatabaseBytes: size, DumpBytes: writer.written, SHA256: hex.EncodeToString(hash.Sum(nil)), ExportedAt: time.Now().UTC()}
		return validateManifest(job.Manifest, build.Version, int64(s.config.MaxFileGiB)*gib, major)
	}); err != nil {
		lock.Close(context.Background())
		return nil, err
	}
	return response, nil
}

func (s *Service) InitializeUpload(ctx context.Context, id string, size int64, manifestHash string) (*Job, error) {
	if size < 1024 || size > int64(s.config.MaxFileGiB)*gib+128*1024 || !digestPattern.MatchString(manifestHash) {
		return nil, ErrPackage
	}
	if _, err := s.preflight(ctx); err != nil {
		return nil, err
	}
	if err := s.emptyDestination(ctx); err != nil {
		return nil, err
	}
	if err := s.importPermission(ctx); err != nil {
		return nil, err
	}
	if err := s.checkSpace(ctx, s.config.WorkDir, size); err != nil {
		return nil, err
	}
	lock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer lock.Close(context.Background())
	job, _, err := s.newJob(ctx, id, "import", fmt.Sprintf("%d:%s", size, manifestHash), "awaiting_upload")
	if err != nil {
		return nil, err
	}
	if job.TotalBytes == 0 {
		job.TotalBytes = size
		if err := s.persist(ctx, job); err != nil {
			return nil, err
		}
	}
	return s.publicJob(job), nil
}

func (s *Service) Upload(ctx context.Context, id string, input io.Reader) (*Job, error) {
	if err := s.beginWork(); err != nil {
		return nil, err
	}
	defer s.wg.Done()
	ctx, serviceCancel := context.WithCancel(ctx)
	defer serviceCancel()
	stopServiceCancel := context.AfterFunc(s.ctx, serviceCancel)
	defer stopServiceCancel()
	major, err := s.preflight(ctx)
	if err != nil {
		return nil, err
	}
	lock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer lock.Close(context.Background())
	job, _, err := s.read(ctx)
	if err != nil {
		return nil, err
	}
	if job == nil || job.ID != id || job.Instance != s.instance || job.Kind != "import" {
		return nil, ErrMissing
	}
	if job.Phase != "awaiting_upload" {
		return s.publicJob(job), nil
	}
	path, err := s.path(id, ".dump")
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	job.Phase = "uploading"
	if err := s.persist(ctx, job); err != nil {
		return nil, err
	}
	workCtx, cancel := context.WithTimeout(ctx, time.Duration(s.config.TimeoutSeconds)*time.Second)
	defer cancel()
	if body, ok := input.(io.ReadCloser); ok {
		stopClose := closeOnCancellation(workCtx, body)
		defer stopClose()
	}
	var bytes atomic.Int64
	stop := s.watch(workCtx, cancel, lock, job, &bytes)
	_, expectedHash, _ := strings.Cut(job.Fingerprint, ":")
	limited := &io.LimitedReader{R: input, N: job.TotalBytes + 1}
	manifest, runErr := readPackage(workCtx, limited, file, build.Version, int64(s.config.MaxFileGiB)*gib, major, bytes.Store, expectedHash)
	stop()
	if runErr == nil && limited.N != 1 {
		runErr = ErrPackage
	}
	if runErr == nil {
		runErr = workCtx.Err()
	}
	job.Bytes = bytes.Load()
	if runErr == nil {
		job.Manifest = manifest
		job.Phase = "ready"
	} else {
		job.Phase = "failed"
		job.Error = ErrorCode(runErr)
	}
	finishCtx, finishCancel := context.WithTimeout(systemContext(), 5*time.Second)
	defer finishCancel()
	if err := s.persist(finishCtx, job); err != nil {
		return nil, err
	}
	if runErr != nil {
		return s.publicJob(job), runErr
	}
	return s.publicJob(job), nil
}

func (s *Service) StartImport(ctx context.Context, id, target string) (*Job, error) {
	if _, err := s.preflight(ctx); err != nil {
		return nil, err
	}
	if !databaseName.MatchString(target) {
		return nil, ErrTarget
	}
	if err := s.emptyDestination(ctx); err != nil {
		return nil, err
	}
	if err := s.importPermission(ctx); err != nil {
		return nil, err
	}
	lock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	job, _, err := s.read(ctx)
	if err != nil {
		lock.Close(context.Background())
		return nil, err
	}
	if job == nil || job.ID != id || job.Kind != "import" || job.Instance != s.instance || job.Manifest == nil {
		lock.Close(context.Background())
		return nil, ErrMissing
	}
	if job.TargetDatabase != "" && job.TargetDatabase != target {
		lock.Close(context.Background())
		return nil, ErrConflict
	}
	if job.Phase != "ready" {
		lock.Close(context.Background())
		return s.publicJob(job), nil
	}
	budget := max(job.Manifest.DatabaseBytes*2, int64(s.config.RestoreReserveGiB)*gib)
	if err := s.checkSpace(ctx, s.config.DatabaseDiskPath, budget); err != nil {
		lock.Close(context.Background())
		return nil, err
	}
	job.TargetDatabase, job.Phase = target, "importing"
	if err := s.persist(ctx, job); err != nil {
		lock.Close(context.Background())
		return nil, err
	}
	response := s.publicJob(job)
	if err := s.launch(lock, job, func(workCtx context.Context, progress func(int64)) error {
		if err := s.emptyDestination(workCtx); err != nil {
			return err
		}
		file, err := s.openDump(job.ID)
		if err != nil {
			return err
		}
		defer file.Close()
		if info, err := file.Stat(); err != nil || info.Size() != job.Manifest.DumpBytes {
			return ErrPackage
		}
		// Recheck immutable bytes before letting the native restore client use them.
		hash := sha256.New()
		if _, err := io.Copy(hash, file); err != nil {
			return err
		}
		if hex.EncodeToString(hash.Sum(nil)) != job.Manifest.SHA256 {
			return ErrPackage
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		return s.restore(workCtx, job, file)
	}); err != nil {
		lock.Close(context.Background())
		return nil, err
	}
	return response, nil
}

func (s *Service) launch(lock *pgx.Conn, job *Job, operation func(context.Context, func(int64)) error) error {
	if err := s.beginWork(); err != nil {
		return err
	}
	go func() {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Error(s.ctx, "Migration worker panicked", log.String("job_id", job.ID))
			}
		}()
		defer lock.Close(context.Background())
		ctx, cancel := context.WithTimeout(s.ctx, time.Duration(s.config.TimeoutSeconds)*time.Second)
		defer cancel()
		var bytes atomic.Int64
		stop := s.watch(ctx, cancel, lock, job, &bytes)
		var runErr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Error(ctx, "Migration task panicked", log.String("job_id", job.ID))
					runErr = ErrProcess
				}
			}()
			runErr = operation(ctx, bytes.Store)
		}()
		stop()
		if runErr == nil {
			runErr = ctx.Err()
		}
		job.Bytes = bytes.Load()
		job.Phase, job.Error = "completed", ""
		if runErr != nil {
			job.Phase, job.Error = "failed", ErrorCode(runErr)
		}
		finishCtx, finishCancel := context.WithTimeout(systemContext(), 5*time.Second)
		defer finishCancel()
		if err := s.persist(finishCtx, job); err != nil {
			log.Warn(finishCtx, "Migration task status not saved", log.String("job_id", job.ID))
		}
	}()
	return nil
}

// One watcher owns the dedicated lock connection. Loss of that connection
// cancels the native process before another instance can own its work.
func (s *Service) watch(ctx context.Context, cancel context.CancelFunc, lock *pgx.Conn, job *Job, bytes *atomic.Int64) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	snapshot := *job
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				cancel()
				log.Error(ctx, "Migration lease monitor panicked", log.String("job_id", snapshot.ID))
			}
		}()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			pingCtx, pingCancel := context.WithTimeout(ctx, 2*time.Second)
			err := lock.Ping(pingCtx)
			pingCancel()
			if err != nil {
				cancel()
				return
			}
			snapshot.Bytes = bytes.Load()
			saveCtx, saveCancel := context.WithTimeout(ctx, 3*time.Second)
			err = s.persist(saveCtx, &snapshot)
			if err == nil {
				err = s.checkSpace(saveCtx, s.config.WorkDir, 0)
			}
			if err == nil && snapshot.Phase == "importing" {
				err = s.checkSpace(saveCtx, s.config.DatabaseDiskPath, 0)
			}
			saveCancel()
			if err != nil {
				cancel()
				return
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(stop); <-done }) }
}

func (s *Service) Discard(ctx context.Context, id string) error {
	lock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer lock.Close(context.Background())
	job, _, err := s.read(ctx)
	if err != nil {
		return err
	}
	if job == nil || job.ID != id || job.active() {
		return ErrBusy
	}
	if job.Kind == "import" && job.TargetDatabase != "" {
		if err := s.cleanupRestoreRole(ctx, job); err != nil {
			return err
		}
	}
	if err := s.removeFiles(job); err != nil {
		return err
	}
	job.Phase = "expired"
	return s.persist(ctx, job)
}
