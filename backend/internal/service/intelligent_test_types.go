package service

import (
	"context"
	"time"
)

const (
	IntelligentTestStatusQueued               = "queued"
	IntelligentTestStatusRunning              = "running"
	IntelligentTestStatusCompleted            = "completed"
	IntelligentTestStatusSuccess              = "success"
	IntelligentTestStatusFailed               = "failed"
	IntelligentTestStatusCancelled            = "cancelled"
	IntelligentTestStatusRateLimited          = "rate_limited"
	IntelligentTestStatusAccountError         = "account_error"
	IntelligentTestStatusModelError           = "model_error"
	IntelligentTestStatusRequestError         = "request_error"
	IntelligentTestStatusNetworkError         = "network_error"
	IntelligentTestStatusSuspectedDegradation = "suspected_degradation"
)

type IntelligentTestConfig struct {
	Prompt         string `json:"prompt"`
	Model          string `json:"model"`
	Evaluator      string `json:"evaluator"`
	ExpectedAnswer string `json:"expected_answer"`
	AnswerType     string `json:"answer_type,omitempty"`
	AnswerFormat   string `json:"answer_format,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type IntelligentTestSetting struct {
	TestType    string                `json:"test_type"`
	Name        string                `json:"name"`
	Enabled     bool                  `json:"enabled"`
	UserVisible bool                  `json:"-"`
	Config      IntelligentTestConfig `json:"config"`
	UpdatedAt   time.Time             `json:"updated_at"`
}

type IntelligentTestRecord struct {
	ID             int64                  `json:"id"`
	AccountID      int64                  `json:"account_id"`
	TestType       string                 `json:"test_type"`
	Status         string                 `json:"status"`
	Score          *float64               `json:"score"`
	Result         string                 `json:"result"`
	ResultImage    string                 `json:"result_image"`
	Input          string                 `json:"-"`
	RawResponse    string                 `json:"-"`
	RawTruncated   bool                   `json:"raw_truncated"`
	ErrorMessage   string                 `json:"error_message"`
	DurationMS     int64                  `json:"duration_ms"`
	Model          string                 `json:"model"`
	ConfigSnapshot *IntelligentTestConfig `json:"config_snapshot,omitempty"`
	Evaluation     map[string]any         `json:"evaluation,omitempty"`
	RequestedBy    int64                  `json:"requested_by,omitempty"`
	LeaseToken     string                 `json:"-"`
	AvailableAt    *time.Time             `json:"available_at,omitempty"`
	QueueReason    string                 `json:"queue_reason,omitempty"`
	StartedAt      *time.Time             `json:"started_at"`
	FinishedAt     *time.Time             `json:"finished_at"`
	CreatedAt      time.Time              `json:"created_at"`
}

type IntelligentTestFilter struct {
	Page, PageSize                               int
	AccountID                                    int64
	Search, Platform, AccountType, AccountStatus string
	TestType, Status                             string
	From, To                                     *time.Time
}

type IntelligentTestAccountCard struct {
	AccountID     int64                     `json:"account_id"`
	Name          string                    `json:"name"`
	Notes         string                    `json:"notes"`
	Platform      string                    `json:"platform"`
	AccountType   string                    `json:"account_type"`
	AccountStatus string                    `json:"account_status"`
	Tests         []IntelligentTestCardTest `json:"tests"`
}

type IntelligentTestCardTest struct {
	TestType     string                 `json:"test_type"`
	Latest       *IntelligentTestRecord `json:"latest"`
	HistoryCount int64                  `json:"history_count"`
}

type IntelligentTestOverview struct {
	TotalAccounts        int64 `json:"total_accounts"`
	TestedToday          int64 `json:"tested_today"`
	SuccessAccounts      int64 `json:"success_accounts"`
	AbnormalAccounts     int64 `json:"abnormal_accounts"`
	SuspectedDegradation int64 `json:"suspected_degradation"`
}

type IntelligentTestAccounts struct {
	Items    []IntelligentTestAccountCard `json:"items"`
	Total    int64                        `json:"total"`
	Page     int                          `json:"page"`
	PageSize int                          `json:"page_size"`
	Overview IntelligentTestOverview      `json:"overview"`
}

type IntelligentTestRecords struct {
	Items    []*IntelligentTestRecord `json:"items"`
	Total    int64                    `json:"total"`
	Page     int                      `json:"page"`
	PageSize int                      `json:"page_size"`
}

type IntelligentTestEnqueue struct {
	AccountIDs     []int64           `json:"account_ids"`
	TestTypes      []string          `json:"test_types"`
	Models         map[string]string `json:"models,omitempty"`
	IdempotencyKey string            `json:"idempotency_key"`
}

type IntelligentTestEnqueued struct {
	CreatedCount int                      `json:"created_count"`
	ReusedCount  int                      `json:"reused_count"`
	Records      []*IntelligentTestRecord `json:"records"`
	Reused       bool                     `json:"reused"`
}

type IntelligentTestRepository interface {
	IsAdmin(ctx context.Context, userID int64) (bool, error)
	IsSuperAdmin(ctx context.Context, userID int64) (bool, error)
	Settings(ctx context.Context) ([]IntelligentTestSetting, error)
	UpdateSetting(ctx context.Context, actorID int64, setting *IntelligentTestSetting) error
	Enqueue(ctx context.Context, actorID int64, req IntelligentTestEnqueue) (*IntelligentTestEnqueued, error)
	Accounts(ctx context.Context, filter IntelligentTestFilter) (*IntelligentTestAccounts, error)
	Records(ctx context.Context, filter IntelligentTestFilter) (*IntelligentTestRecords, error)
	Get(ctx context.Context, id int64) (*IntelligentTestRecord, error)
	Claim(ctx context.Context, leaseFor time.Duration) (*IntelligentTestRecord, error)
	Finish(ctx context.Context, record *IntelligentTestRecord) error
	Defer(ctx context.Context, record *IntelligentTestRecord, availableAt time.Time, reason string) error
	Cancel(ctx context.Context, actorID, id int64) (*IntelligentTestRecord, error)
	Reevaluate(ctx context.Context, actorID, id int64, fn func(*IntelligentTestRecord) error) (*IntelligentTestRecord, error)
}

type IntelligentTestRunner interface {
	RunIntelligentTest(ctx context.Context, record *IntelligentTestRecord) error
}
