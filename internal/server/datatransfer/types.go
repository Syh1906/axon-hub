package datatransfer

import (
	"errors"
	"time"
)

const (
	jobKey              = "database_transfer_job_v1"
	packageFormat       = "axonhub-postgres-v1"
	advisoryKey   int64 = 0x41584f4e4d494752
	gib           int64 = 1024 * 1024 * 1024
)

type Config struct {
	WorkDir           string `conf:"work_dir" yaml:"work_dir" json:"work_dir"`
	MaxFileGiB        int    `conf:"max_file_gib" yaml:"max_file_gib" json:"max_file_gib"`
	TimeoutSeconds    int    `conf:"timeout_seconds" yaml:"timeout_seconds" json:"timeout_seconds"`
	RetentionHours    int    `conf:"retention_hours" yaml:"retention_hours" json:"retention_hours"`
	DatabaseDiskPath  string `conf:"database_disk_path" yaml:"database_disk_path" json:"database_disk_path"`
	RestoreReserveGiB int    `conf:"restore_reserve_gib" yaml:"restore_reserve_gib" json:"restore_reserve_gib"`
}

type Manifest struct {
	Format        string    `json:"format"`
	AppVersion    string    `json:"appVersion"`
	PostgresMajor int       `json:"postgresMajor"`
	DatabaseBytes int64     `json:"databaseBytes"`
	DumpBytes     int64     `json:"dumpBytes"`
	SHA256        string    `json:"sha256"`
	ExportedAt    time.Time `json:"exportedAt"`
}

type Job struct {
	ID                string    `json:"id"`
	Kind              string    `json:"kind"`
	Phase             string    `json:"phase"`
	Bytes             int64     `json:"bytes"`
	TotalBytes        int64     `json:"totalBytes"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
	Manifest          *Manifest `json:"manifest,omitempty"`
	TargetDatabase    string    `json:"targetDatabase,omitempty"`
	Error             string    `json:"error,omitempty"`
	DownloadAvailable bool      `json:"downloadAvailable"`
	Owner             string    `json:"owner,omitempty"` // Private executor token; stripped at HTTP boundary.
	Instance          string    `json:"instance,omitempty"`
	Fingerprint       string    `json:"fingerprint,omitempty"`
}

func (j *Job) active() bool {
	return j != nil && (j.Phase == "exporting" || j.Phase == "uploading" || j.Phase == "checking" || j.Phase == "importing")
}

type Status struct {
	Supported     bool   `json:"supported"`
	Reason        string `json:"reason"`
	Dialect       string `json:"dialect"`
	PostgresMajor int    `json:"postgresMajor"`
	ImportAllowed bool   `json:"importAllowed"`
	ImportReason  string `json:"importReason"`
	MaxFileBytes  int64  `json:"maxFileBytes"`
	Job           *Job   `json:"job"`
}

var (
	ErrBusy        = errors.New("busy")
	ErrUnsupported = errors.New("unsupported")
	ErrTools       = errors.New("tools_unavailable")
	ErrNotEmpty    = errors.New("destination_not_empty")
	ErrSpace       = errors.New("insufficient_space")
	ErrDiskPath    = errors.New("database_disk_unavailable")
	ErrPackage     = errors.New("invalid_package")
	ErrVersion     = errors.New("incompatible_version")
	ErrMissing     = errors.New("artifact_unavailable")
	ErrConflict    = errors.New("submission_conflict")
	ErrRole        = errors.New("migration_permission_required")
	ErrTarget      = errors.New("target_database_exists")
	ErrLostOwner   = errors.New("ownership_lost")
	ErrProcess     = errors.New("database_tool_failed")
)
