package service

import (
	"context"
	"errors"
	"fmt"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	intelligentTestMinTimeoutSeconds = 30
	intelligentTestMaxTimeoutSeconds = 600
	intelligentTestWorkerLease       = 12 * time.Minute
)

var (
	intelligentTestBearerSecretPattern = regexp.MustCompile(`(?i)(\bBearer\s+)[A-Za-z0-9._~+/=-]{6,}`)
	intelligentTestURLPasswordPattern  = regexp.MustCompile(`(?i)(\b[a-z][a-z0-9+.-]*://[^:/\s@]+:)[^@\s/]+(@)`)
	intelligentTestAPIKeyPattern       = regexp.MustCompile(`(?i)\b(?:sk-(?:proj-|ant-)?|xai-)[A-Za-z0-9._~+/=-]{8,}`)
)

var ErrIntelligentTestNotFound = infraerrors.NotFound("INTELLIGENT_TEST_NOT_FOUND", "test result not found")
var ErrIntelligentTestForbidden = infraerrors.Forbidden("INTELLIGENT_TEST_FORBIDDEN", "administrator access required")
var ErrIntelligentTestConflict = infraerrors.Conflict("INTELLIGENT_TEST_CONFLICT", "idempotency key was already used with different parameters")
var ErrIntelligentTestLeaseLost = infraerrors.Conflict("INTELLIGENT_TEST_LEASE_LOST", "test worker lease is no longer active")

func intelligentTestBad(message string) error {
	return infraerrors.BadRequest("INTELLIGENT_TEST_INVALID", message)
}

type IntelligentTestService struct {
	repo       IntelligentTestRepository
	runner     IntelligentTestRunner
	evaluators map[string]IntelligentTestEvaluator
	cancel     context.CancelFunc
	mu         sync.Mutex
	wg         sync.WaitGroup
	activeMu   sync.Mutex
	active     map[int64]context.CancelFunc
}

func NewIntelligentTestService(repo IntelligentTestRepository, runner IntelligentTestRunner) *IntelligentTestService {
	return &IntelligentTestService{
		repo:       repo,
		runner:     runner,
		evaluators: DefaultIntelligentTestEvaluators(),
		active:     make(map[int64]context.CancelFunc),
	}
}

func (s *IntelligentTestService) authorize(ctx context.Context, actorID int64) error {
	ok, err := s.repo.IsAdmin(ctx, actorID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrIntelligentTestForbidden
	}
	return nil
}

func (s *IntelligentTestService) authorizeSuperAdmin(ctx context.Context, actorID int64) error {
	ok, err := s.repo.IsSuperAdmin(ctx, actorID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrIntelligentTestForbidden
	}
	return nil
}

func normalizeIntelligentFilter(f IntelligentTestFilter) IntelligentTestFilter {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 24
	}
	if f.PageSize > 100 {
		f.PageSize = 100
	}
	return f
}

func (s *IntelligentTestService) Accounts(ctx context.Context, actorID int64, f IntelligentTestFilter) (*IntelligentTestAccounts, error) {
	if err := s.authorize(ctx, actorID); err != nil {
		return nil, err
	}
	return s.repo.Accounts(ctx, normalizeIntelligentFilter(f))
}

func (s *IntelligentTestService) Records(ctx context.Context, actorID int64, f IntelligentTestFilter) (*IntelligentTestRecords, error) {
	if err := s.authorize(ctx, actorID); err != nil {
		return nil, err
	}
	return s.repo.Records(ctx, normalizeIntelligentFilter(f))
}

func (s *IntelligentTestService) Get(ctx context.Context, actorID, id int64) (*IntelligentTestRecord, error) {
	if err := s.authorize(ctx, actorID); err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, id)
}

func (s *IntelligentTestService) Settings(ctx context.Context, actorID int64) ([]IntelligentTestSetting, error) {
	if err := s.authorize(ctx, actorID); err != nil {
		return nil, err
	}
	return s.repo.Settings(ctx)
}

func (s *IntelligentTestService) UpdateSetting(ctx context.Context, actorID int64, setting *IntelligentTestSetting) error {
	if err := s.authorizeSuperAdmin(ctx, actorID); err != nil {
		return err
	}
	if setting == nil || !regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`).MatchString(setting.TestType) {
		return intelligentTestBad("invalid test type")
	}
	if err := validateIntelligentTestConfig(&setting.Config, s.evaluators); err != nil {
		return err
	}
	return s.repo.UpdateSetting(ctx, actorID, setting)
}

func (s *IntelligentTestService) Enqueue(ctx context.Context, actorID int64, req IntelligentTestEnqueue) (*IntelligentTestEnqueued, error) {
	if err := s.authorize(ctx, actorID); err != nil {
		return nil, err
	}
	if len(req.AccountIDs) == 0 || len(req.AccountIDs) > 50 || len(req.TestTypes) == 0 || len(req.TestTypes) > 20 {
		return nil, intelligentTestBad("account_ids and test_types are required and must be bounded")
	}
	seenAccounts := map[int64]bool{}
	for _, id := range req.AccountIDs {
		if id <= 0 || seenAccounts[id] {
			return nil, intelligentTestBad("account_ids must be positive and unique")
		}
		seenAccounts[id] = true
	}
	seenTests := map[string]bool{}
	for _, testType := range req.TestTypes {
		if !regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`).MatchString(testType) || seenTests[testType] {
			return nil, intelligentTestBad("test_types must be valid and unique")
		}
		seenTests[testType] = true
	}
	for testType, model := range req.Models {
		if !seenTests[testType] || utf8.RuneCountInString(model) > 200 {
			return nil, intelligentTestBad("model overrides must target selected tests and be at most 200 characters")
		}
	}
	for accountID, model := range req.AccountModels {
		if !seenAccounts[accountID] || utf8.RuneCountInString(model) > 200 {
			return nil, intelligentTestBad("account model overrides must target selected accounts and be at most 200 characters")
		}
	}
	return s.repo.Enqueue(ctx, actorID, req)
}

func (s *IntelligentTestService) Cancel(ctx context.Context, actorID, id int64) (*IntelligentTestRecord, error) {
	if err := s.authorize(ctx, actorID); err != nil {
		return nil, err
	}
	record, err := s.repo.Cancel(ctx, actorID, id)
	if err != nil {
		return nil, err
	}
	s.activeMu.Lock()
	cancel := s.active[id]
	s.activeMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return record, nil
}

func (s *IntelligentTestService) Reevaluate(ctx context.Context, actorID, id int64) (*IntelligentTestRecord, error) {
	if err := s.authorize(ctx, actorID); err != nil {
		return nil, err
	}
	return s.repo.Reevaluate(ctx, actorID, id, func(record *IntelligentTestRecord) error {
		if record.ConfigSnapshot == nil {
			return intelligentTestBad("missing test configuration snapshot")
		}
		evaluator, ok := s.evaluators[record.ConfigSnapshot.Evaluator]
		if !ok {
			return intelligentTestBad("unknown evaluator")
		}
		evaluation := evaluator.Evaluate(record.Result, *record.ConfigSnapshot)
		record.Status = evaluation.Status
		record.Score = evaluation.Score
		record.ResultImage = evaluation.Image
		record.Evaluation = evaluation.Detail
		return nil
	})
}

func (s *IntelligentTestService) PreviewEvaluation(ctx context.Context, actorID int64, output string, cfg IntelligentTestConfig) (*IntelligentTestRecord, error) {
	if err := s.authorizeSuperAdmin(ctx, actorID); err != nil {
		return nil, err
	}
	if len(output) > 512<<10 {
		return nil, intelligentTestBad("preview output is too large")
	}
	if err := validateIntelligentTestConfig(&cfg, s.evaluators); err != nil {
		return nil, err
	}
	evaluation := s.evaluators[cfg.Evaluator].Evaluate(output, cfg)
	return &IntelligentTestRecord{Status: evaluation.Status, Score: evaluation.Score, Result: trimIntelligentText(output, 4096), ResultImage: evaluation.Image, Evaluation: evaluation.Detail, ConfigSnapshot: &cfg}, nil
}

func (s *IntelligentTestService) Start() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	for i := 0; i < 2; i++ {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					s.work(ctx)
				}
			}
		}()
	}
}

func (s *IntelligentTestService) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *IntelligentTestService) work(ctx context.Context) {
	claimCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	record, err := s.repo.Claim(claimCtx, intelligentTestWorkerLease)
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("intelligent test claim failed", "error", err)
		}
		return
	}
	if record == nil {
		return
	}
	s.run(ctx, record)
	saveCtx, saveCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer saveCancel()
	if record.Status == IntelligentTestStatusQueued {
		availableAt := time.Now().Add(30 * time.Second)
		if record.AvailableAt != nil && record.AvailableAt.After(availableAt) {
			availableAt = *record.AvailableAt
		}
		if err := s.repo.Defer(saveCtx, record, availableAt, record.QueueReason); err != nil {
			slog.Error("intelligent test defer failed", "id", record.ID, "error", err)
		}
		return
	}
	if err := s.repo.Finish(saveCtx, record); err != nil {
		slog.Error("intelligent test finish failed", "id", record.ID, "error", err)
	}
}

func (s *IntelligentTestService) run(ctx context.Context, record *IntelligentTestRecord) {
	started := time.Now()
	defer func() {
		record.DurationMS = time.Since(started).Milliseconds()
		if recovered := recover(); recovered != nil {
			record.Status = IntelligentTestStatusFailed
			record.ErrorMessage = "test runner failed unexpectedly"
			slog.Error("intelligent test panic", "id", record.ID, "panic", fmt.Sprintf("%T", recovered))
		}
		if record.Evaluation == nil {
			record.Evaluation = newIntelligentAssessment("")
		}
		record.ErrorMessage = trimIntelligentText(redactIntelligentTestError(record.ErrorMessage), 2000)
	}()
	if record.ConfigSnapshot == nil {
		record.Status = IntelligentTestStatusFailed
		record.ErrorMessage = "missing test configuration snapshot"
		return
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(record.ConfigSnapshot.TimeoutSeconds)*time.Second)
	s.activeMu.Lock()
	s.active[record.ID] = cancel
	s.activeMu.Unlock()
	defer func() {
		cancel()
		s.activeMu.Lock()
		delete(s.active, record.ID)
		s.activeMu.Unlock()
	}()
	if err := s.runner.RunIntelligentTest(runCtx, record); err != nil {
		if runCtx.Err() != nil {
			record.Status = IntelligentTestStatusFailed
			record.ErrorMessage = "test timed out or service stopped"
			return
		}
		if record.Status == "" || record.Status == IntelligentTestStatusRunning {
			record.Status = classifyIntelligentTestError(err.Error())
		}
		if record.ErrorMessage == "" {
			record.ErrorMessage = trimIntelligentText(err.Error(), 2000)
		}
		return
	}
	evaluator, ok := s.evaluators[record.ConfigSnapshot.Evaluator]
	if !ok {
		record.Status = IntelligentTestStatusFailed
		record.ErrorMessage = "evaluator unavailable"
		return
	}
	evaluation := evaluator.Evaluate(record.Result, *record.ConfigSnapshot)
	record.Status = evaluation.Status
	record.Score = evaluation.Score
	record.ResultImage = evaluation.Image
	record.Evaluation = evaluation.Detail
}

func validateIntelligentTestConfig(cfg *IntelligentTestConfig, evaluators map[string]IntelligentTestEvaluator) error {
	cfg.Prompt = strings.TrimSpace(cfg.Prompt)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.Evaluator = strings.TrimSpace(cfg.Evaluator)
	if cfg.Prompt == "" || utf8.RuneCountInString(cfg.Prompt) > 16000 || utf8.RuneCountInString(cfg.Model) > 200 || utf8.RuneCountInString(cfg.ExpectedAnswer) > 200 {
		return intelligentTestBad("prompt/model/answer length is invalid")
	}
	if cfg.TimeoutSeconds < intelligentTestMinTimeoutSeconds || cfg.TimeoutSeconds > intelligentTestMaxTimeoutSeconds {
		return intelligentTestBad("timeout_seconds must be 30-600")
	}
	if _, ok := evaluators[cfg.Evaluator]; !ok {
		return intelligentTestBad("unknown evaluator")
	}
	if cfg.Evaluator == "exact_answer" && strings.TrimSpace(cfg.ExpectedAnswer) == "" {
		return intelligentTestBad("expected_answer is required")
	}
	if cfg.AnswerType != "" && cfg.AnswerType != "auto" && cfg.AnswerType != "number" && cfg.AnswerType != "text" {
		return intelligentTestBad("invalid answer_type")
	}
	if cfg.AnswerFormat != "" && cfg.AnswerFormat != "answer_line" && cfg.AnswerFormat != "free_text" {
		return intelligentTestBad("invalid answer_format")
	}
	return nil
}

func classifyIntelligentTestError(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "429") || strings.Contains(lower, "rate limit") || strings.Contains(lower, "too many requests"):
		return IntelligentTestStatusRateLimited
	case strings.Contains(lower, "401") || strings.Contains(lower, "403") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "forbidden"):
		return IntelligentTestStatusAccountError
	case strings.Contains(lower, "model"):
		return IntelligentTestStatusModelError
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "connection") || strings.Contains(lower, "network"):
		return IntelligentTestStatusNetworkError
	default:
		return IntelligentTestStatusFailed
	}
}

func trimIntelligentText(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "..."
}

func redactIntelligentTestError(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = intelligentTestBearerSecretPattern.ReplaceAllString(value, `${1}[REDACTED]`)
	value = intelligentTestURLPasswordPattern.ReplaceAllString(value, `${1}[REDACTED]${2}`)
	value = intelligentTestAPIKeyPattern.ReplaceAllString(value, `[REDACTED]`)
	return logredact.RedactText(
		value,
		"api_key",
		"api-key",
		"apikey",
		"key",
		"token",
		"authorization",
		"x-api-key",
		"passwd",
		"pwd",
		"secret",
		"proxy_password",
	)
}

func isIntelligentTerminalStatus(status string) bool {
	switch status {
	case IntelligentTestStatusQueued, IntelligentTestStatusRunning:
		return false
	default:
		return true
	}
}

func intelligentTestRunError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return err
}
