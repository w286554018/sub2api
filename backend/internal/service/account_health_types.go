package service

import (
	"context"
	"time"
)

const (
	SettingKeyAccountHealthSettings = "account_health_settings"

	AccountHealthStateHealthy  = "healthy"
	AccountHealthStateDegraded = "degraded"
	AccountHealthStateIsolated = "isolated"
)

// AccountHealthSettings contains the read model thresholds and the disabled-by-default
// automation settings that Phase 4 will consume.
type AccountHealthSettings struct {
	Enabled          bool    `json:"enabled"`
	WindowMinutes    int     `json:"window_minutes"`
	MinSamples       int     `json:"min_samples"`
	IsolateErrorRate float64 `json:"isolate_error_rate"`
	RecoverErrorRate float64 `json:"recover_error_rate"`
	CooldownMinutes  int     `json:"cooldown_minutes"`
	IntervalSeconds  int     `json:"interval_seconds"`
}

func DefaultAccountHealthSettings() AccountHealthSettings {
	return AccountHealthSettings{
		Enabled:          false,
		WindowMinutes:    10,
		MinSamples:       10,
		IsolateErrorRate: 0.5,
		RecoverErrorRate: 0.2,
		CooldownMinutes:  30,
		IntervalSeconds:  60,
	}
}

type AccountHealthFilter struct {
	Page     int
	PageSize int
	Search   string
	Platform string
	State    string
}

// AccountHealthWindowStat is the repository read model. It intentionally contains
// no account mutation capability.
type AccountHealthWindowStat struct {
	AccountID               int64
	Name                    string
	Platform                string
	AccountStatus           string
	SuccessCount            int64
	ErrorCount              int64
	AvgLatencyMS            *float64
	TempUnschedulableUntil  *time.Time
	TempUnschedulableReason string
}

type AccountHealthSnapshot struct {
	AccountID                int64      `json:"account_id"`
	Name                     string     `json:"name"`
	Platform                 string     `json:"platform"`
	AccountStatus            string     `json:"account_status"`
	SuccessCount             int64      `json:"success_count"`
	ErrorCount               int64      `json:"error_count"`
	TotalCount               int64      `json:"total_count"`
	ErrorRate                float64    `json:"error_rate"`
	AvgLatencyMS             *int64     `json:"avg_latency_ms"`
	Score                    int        `json:"score"`
	State                    string     `json:"state"`
	HasEnoughSamples         bool       `json:"has_enough_samples"`
	TemporarilyUnschedulable bool       `json:"temporarily_unschedulable"`
	UnschedulableReason      string     `json:"unschedulable_reason,omitempty"`
	UnschedulableUntil       *time.Time `json:"unschedulable_until,omitempty"`
	EvaluatedAt              time.Time  `json:"evaluated_at"`
}

type AccountHealthOverview struct {
	Healthy       int64 `json:"healthy"`
	Degraded      int64 `json:"degraded"`
	Isolated      int64 `json:"isolated"`
	NoSamples     int64 `json:"no_samples"`
	TotalAccounts int64 `json:"total_accounts"`
}

type AccountHealthPage struct {
	Items         []AccountHealthSnapshot `json:"items"`
	Total         int64                   `json:"total"`
	Page          int                     `json:"page"`
	PageSize      int                     `json:"page_size"`
	EvaluatedAt   time.Time               `json:"evaluated_at"`
	WindowMinutes int                     `json:"window_minutes"`
	Overview      AccountHealthOverview   `json:"overview"`
}

type AccountHealthRepository interface {
	IsAdmin(ctx context.Context, userID int64) (bool, error)
	IsSuperAdmin(ctx context.Context, userID int64) (bool, error)
	ListWindowStats(ctx context.Context, since time.Time, filter AccountHealthFilter) ([]AccountHealthWindowStat, error)
}
