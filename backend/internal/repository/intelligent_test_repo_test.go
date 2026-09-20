package repository

import (
	"context"
	"database/sql/driver"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func intelligentTestRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "account_id", "test_type", "status", "score", "result", "result_image", "input", "raw_response", "raw_truncated", "error_message", "duration_ms", "model", "config_snapshot", "evaluation", "requested_by", "lease_token", "available_at", "queue_reason", "started_at", "finished_at", "created_at"}).
		AddRow(int64(7), int64(3), "candy", service.IntelligentTestStatusQueued, nil, "", "", "prompt", "", false, "", int64(0), "", `{"prompt":"prompt","evaluator":"exact_answer","expected_answer":"12","timeout_seconds":60}`, `{}`, int64(9), "lease", time.Now(), "", nil, nil, time.Now())
}

func TestIntelligentTestRepositoryClaimUsesLeaseAndSkipLocked(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := NewIntelligentTestRepository(db)
	mock.ExpectQuery(`(?s)SET status='failed'.*candidate\.status='queued'.*FOR UPDATE OF candidate SKIP LOCKED`).
		WithArgs(sqlmock.AnyArg(), "300 seconds").
		WillReturnRows(intelligentTestRows())

	record, err := repo.Claim(context.Background(), 5*time.Minute)
	require.NoError(t, err)
	require.Equal(t, int64(7), record.ID)
	require.Equal(t, "lease", record.LeaseToken)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIntelligentTestRepositoryReusesMatchingIdempotencyKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	req := service.IntelligentTestEnqueue{AccountIDs: []int64{3}, TestTypes: []string{"candy"}, IdempotencyKey: "same"}
	fingerprint := intelligentRequestFingerprint(req)
	repo := NewIntelligentTestRepository(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock(hashtextextended($1, 0))")).
		WithArgs("9:same").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT fingerprint,record_ids FROM intelligent_test_requests WHERE actor_id=$1 AND request_key=$2")).
		WithArgs(int64(9), "same").
		WillReturnRows(sqlmock.NewRows([]string{"fingerprint", "record_ids"}).AddRow(fingerprint, pq.Int64Array{7}))
	mock.ExpectCommit()
	mock.ExpectQuery(regexp.QuoteMeta("FROM account_tests t WHERE t.id=ANY($1)")).
		WithArgs(pqArrayArg{values: []int64{7}}).
		WillReturnRows(intelligentTestRows())

	out, err := repo.Enqueue(context.Background(), 9, req)
	require.NoError(t, err)
	require.True(t, out.Reused)
	require.Equal(t, 1, out.ReusedCount)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIntelligentTestRepositoryRejectsMismatchedIdempotencyFingerprint(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := NewIntelligentTestRepository(db)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock(hashtextextended($1, 0))")).
		WithArgs("9:same").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT fingerprint,record_ids FROM intelligent_test_requests WHERE actor_id=$1 AND request_key=$2")).
		WithArgs(int64(9), "same").
		WillReturnRows(sqlmock.NewRows([]string{"fingerprint", "record_ids"}).AddRow("different", pq.Int64Array{7}))
	mock.ExpectRollback()

	_, err = repo.Enqueue(context.Background(), 9, service.IntelligentTestEnqueue{
		AccountIDs:     []int64{3},
		TestTypes:      []string{"candy"},
		IdempotencyKey: "same",
	})
	require.ErrorIs(t, err, service.ErrIntelligentTestConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIntelligentRequestFingerprintIncludesAccountModelOverrides(t *testing.T) {
	base := service.IntelligentTestEnqueue{
		AccountIDs: []int64{3},
		TestTypes:  []string{"candy"},
	}
	withModel := base
	withModel.AccountModels = map[int64]string{3: "gpt-5.6-sol"}

	require.NotEqual(t, intelligentRequestFingerprint(base), intelligentRequestFingerprint(withModel))
}

func TestIntelligentTestRepositoryFinishRejectsLostLease(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := NewIntelligentTestRepository(db)
	mock.ExpectExec(regexp.QuoteMeta("WHERE id=$1 AND status='running' AND lease_token=$2")).
		WithArgs(int64(7), "stale", service.IntelligentTestStatusFailed, nil, "", "", "", "", false, "", int64(0), "", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = repo.Finish(context.Background(), &service.IntelligentTestRecord{
		ID:         7,
		LeaseToken: "stale",
		Status:     service.IntelligentTestStatusFailed,
	})
	require.ErrorIs(t, err, service.ErrIntelligentTestLeaseLost)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIntelligentTestRepositoryDeferRejectsLostLease(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := NewIntelligentTestRepository(db)
	availableAt := time.Now().Add(time.Minute)
	mock.ExpectExec(regexp.QuoteMeta("WHERE id=$1 AND status='running' AND lease_token=$2")).
		WithArgs(int64(7), "stale", availableAt, "busy").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = repo.Defer(context.Background(), &service.IntelligentTestRecord{ID: 7, LeaseToken: "stale"}, availableAt, "busy")
	require.ErrorIs(t, err, service.ErrIntelligentTestLeaseLost)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIntelligentTestRepositoryCancelClearsLease(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := NewIntelligentTestRepository(db)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE account_tests t SET status='cancelled',lease_token=NULL,lease_until=NULL")).
		WithArgs(int64(7)).
		WillReturnRows(intelligentTestRows())
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO audit_logs")).
		WithArgs(int64(9), "intelligent_test.cancel", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	record, err := repo.Cancel(context.Background(), 9, 7)
	require.NoError(t, err)
	require.Equal(t, int64(7), record.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

type pqArrayArg struct{ values []int64 }

func (a pqArrayArg) Match(v driver.Value) bool {
	encoded, err := pq.Array(a.values).Value()
	return err == nil && encoded == v
}
