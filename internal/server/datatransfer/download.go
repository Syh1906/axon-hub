package datatransfer

import (
	"context"
	"encoding/json"
	"io"
	"time"
)

func (s *Service) Timeout() time.Duration {
	return time.Duration(s.config.TimeoutSeconds) * time.Second
}
func (s *Service) MaxUploadBytes() int64 { return int64(s.config.MaxFileGiB)*gib + 128*1024 }

// StreamDownload keeps the session lock until the consumer finishes. The
// callback sets HTTP headers only after all artifact checks have succeeded.
func (s *Service) StreamDownload(ctx context.Context, id, digest string, begin func(int64) io.Writer) error {
	if err := s.beginWork(); err != nil {
		return err
	}
	defer s.wg.Done()
	ctx, cancel := context.WithTimeout(ctx, s.Timeout())
	defer cancel()
	stopServiceCancel := context.AfterFunc(s.ctx, cancel)
	defer stopServiceCancel()
	if err := s.initializeFiles(); err != nil {
		return err
	}
	lock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer lock.Close(context.Background())
	job, _, err := s.read(ctx)
	if err != nil {
		return err
	}
	if job == nil || job.ID != id || job.Instance != s.instance || job.Kind != "export" || job.Phase != "completed" || job.Manifest == nil || job.Manifest.SHA256 != digest || time.Since(job.UpdatedAt) > time.Duration(s.config.RetentionHours)*time.Hour {
		return ErrMissing
	}
	f, err := s.openDump(id)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() != job.Manifest.DumpBytes {
		return ErrMissing
	}
	manifest, err := json.Marshal(job.Manifest)
	if err != nil {
		return err
	}
	size := int64(2048) + (int64(len(manifest))+511)/512*512 + (job.Manifest.DumpBytes+511)/512*512
	writer := begin(size)
	if writer == nil {
		return nil
	} // HEAD validates without consuming a ticket.
	if closer, ok := writer.(io.Closer); ok {
		stopClose := closeOnCancellation(ctx, closer)
		defer stopClose()
	}
	return writePackage(&progressWriter{ctx: ctx, target: writer, max: size}, job.Manifest, f)
}
