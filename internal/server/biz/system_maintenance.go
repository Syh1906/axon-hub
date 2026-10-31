package biz

import (
	"fmt"
	"math"
)

// MaintenanceSettings limits background work independently of request handling.
// DiskPath is interpreted on the application host; remote database volumes must
// be monitored by the deployment's host-side tooling.
type MaintenanceSettings struct {
	BatchSize              int     `json:"batch_size"`
	BatchIntervalMs        int     `json:"batch_interval_ms"`
	BatchTimeoutSeconds    int     `json:"batch_timeout_seconds"`
	MaxRunSeconds          int     `json:"max_run_seconds"`
	BusyConnectionsPercent int     `json:"busy_connections_percent"`
	DiskPath               string  `json:"disk_path"`
	DiskWarningPercent     int     `json:"disk_warning_percent"`
	DiskCriticalPercent    int     `json:"disk_critical_percent"`
	ReserveDiskGiB         float64 `json:"reserve_disk_gib"`
}

func DefaultMaintenanceSettings() *MaintenanceSettings {
	return &MaintenanceSettings{
		BatchSize: 100, BatchIntervalMs: 250, BatchTimeoutSeconds: 3,
		MaxRunSeconds: 300, BusyConnectionsPercent: 60,
		DiskWarningPercent: 70, DiskCriticalPercent: 85,
	}
}

func (m *MaintenanceSettings) Validate() error {
	if m.BatchSize < 1 || m.BatchSize > 500 || m.BatchIntervalMs < 100 || m.BatchIntervalMs > 60000 ||
		m.BatchTimeoutSeconds < 1 || m.BatchTimeoutSeconds > 10 || m.MaxRunSeconds < 10 || m.MaxRunSeconds > 3600 ||
		m.BusyConnectionsPercent < 10 || m.BusyConnectionsPercent > 90 {
		return fmt.Errorf("invalid maintenance limits: batch 1–500, interval 100–60000 ms, timeout 1–10 s, run 10–3600 s, busy threshold 10–90%%")
	}
	if m.DiskWarningPercent < 1 || m.DiskCriticalPercent <= m.DiskWarningPercent || m.DiskCriticalPercent > 99 || m.ReserveDiskGiB < 0 || math.IsInf(m.ReserveDiskGiB, 0) || math.IsNaN(m.ReserveDiskGiB) {
		return fmt.Errorf("invalid disk thresholds: warning must be below critical (<100%%); reserve must be non-negative")
	}
	return nil
}
