package objects

import (
	"crypto/sha256"
	"fmt"
	"time"
)

// UsageRestoreRef identifies the same record across backups, retention and local ID remapping.
func UsageRestoreRef(saved *string, kind string, project, id int, at time.Time) string {
	if saved != nil {
		return *saved
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d/%s/%d/%s", project, kind, id, at.UTC().Format(time.RFC3339Nano)))))
}
