package datatransfer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/system"
)

func (s *Service) read(ctx context.Context) (*Job, string, error) {
	// Transactions force primary routing even when the installation has replicas.
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	row, err := tx.System.Query().Where(system.KeyEQ(jobKey)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var job Job
	if err := json.Unmarshal([]byte(row.Value), &job); err != nil {
		return nil, "", err
	}
	return &job, row.Value, nil
}

func (s *Service) write(ctx context.Context, previous string, job *Job) error {
	encoded, err := json.Marshal(job)
	if err != nil {
		return err
	}
	if previous == "" {
		_, err = s.client.System.Create().SetKey(jobKey).SetValue(string(encoded)).Save(ctx)
		return err
	}
	n, err := s.client.System.Update().Where(system.KeyEQ(jobKey), system.ValueEQ(previous)).SetValue(string(encoded)).Save(ctx)
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrLostOwner
	}
	return nil
}

func (s *Service) persist(ctx context.Context, job *Job) error {
	current, raw, err := s.read(ctx)
	if err != nil {
		return err
	}
	if current == nil || current.ID != job.ID || current.Owner != job.Owner {
		return ErrLostOwner
	}
	job.UpdatedAt = time.Now().UTC()
	if err := s.write(ctx, raw, job); err != nil {
		return err
	}
	if job.Phase == "expired" {
		return nil
	}
	return s.writeFile(job)
}

func (s *Service) ensureDirectory() error {
	if s.config.WorkDir == "" || s.config.MaxFileGiB < 1 || s.config.MaxFileGiB > 1024 || s.config.RetentionHours < 1 || s.config.TimeoutSeconds < 30 {
		return ErrUnsupported
	}
	if err := os.MkdirAll(s.config.WorkDir, 0700); err != nil {
		return err
	}
	// A caller-selected root may be a mount, but never a symlink into another root.
	info, err := os.Lstat(s.config.WorkDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrMissing
	}
	return nil
}

func (s *Service) path(id, suffix string) (string, error) {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id || (suffix != ".dump" && suffix != ".json") {
		return "", ErrMissing
	}
	path := filepath.Join(s.config.WorkDir, id+suffix)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return "", ErrMissing
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return path, nil
}

func (s *Service) writeFile(job *Job) error {
	path, err := s.path(job.ID, ".json")
	if err != nil {
		return err
	}
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.config.WorkDir, ".metadata-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp.Name(), path)
}

func (s *Service) openDump(id string) (*os.File, error) {
	path, err := s.path(id, ".dump")
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrMissing
	}
	return f, nil
}

func (s *Service) removeFiles(job *Job) error {
	if job.Instance != s.instance {
		return ErrMissing
	}
	for _, suffix := range []string{".dump", ".json"} {
		path, err := s.path(job.ID, suffix)
		if err != nil {
			return err
		}
		if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Service) publicJob(job *Job) *Job {
	if job == nil {
		return nil
	}
	copy := *job
	copy.Owner, copy.Instance, copy.Fingerprint = "", "", ""
	copy.DownloadAvailable = false
	if job.Kind == "export" && job.Phase == "completed" && job.Instance == s.instance && job.Manifest != nil && time.Since(job.UpdatedAt) <= time.Duration(s.config.RetentionHours)*time.Hour {
		if f, err := s.openDump(job.ID); err == nil {
			info, e := f.Stat()
			f.Close()
			copy.DownloadAvailable = e == nil && info.Size() == job.Manifest.DumpBytes
		}
	}
	return &copy
}
