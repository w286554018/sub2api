package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type accountHealthRepository struct {
	db *sql.DB
}

func NewAccountHealthRepository(db *sql.DB) service.AccountHealthRepository {
	return &accountHealthRepository{db: db}
}

func (r *accountHealthRepository) IsAdmin(ctx context.Context, userID int64) (bool, error) {
	var ok bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND role IN ('admin','super_admin') AND status='active' AND deleted_at IS NULL)`, userID).Scan(&ok)
	return ok, err
}

func (r *accountHealthRepository) IsSuperAdmin(ctx context.Context, userID int64) (bool, error) {
	var ok bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND role='super_admin' AND status='active' AND deleted_at IS NULL)`, userID).Scan(&ok)
	return ok, err
}

func (r *accountHealthRepository) ListWindowStats(ctx context.Context, since time.Time, filter service.AccountHealthFilter) ([]service.AccountHealthWindowStat, error) {
	const query = `
WITH success_stats AS (
  SELECT account_id, COUNT(*) AS success_count, AVG(duration_ms)::float8 AS avg_latency_ms
  FROM usage_logs
  WHERE created_at >= $1
    AND account_id IS NOT NULL
    AND account_id > 0
  GROUP BY account_id
), error_stats AS (
  SELECT account_id, COUNT(*) AS error_count
  FROM ops_error_logs
  WHERE created_at >= $1
    AND account_id IS NOT NULL
    AND account_id > 0
    AND (status_code IS NULL OR status_code >= 400)
    AND NOT COALESCE(is_business_limited, FALSE)
  GROUP BY account_id
)
SELECT a.id,
       COALESCE(a.name, ''),
       COALESCE(a.platform, ''),
       COALESCE(a.status, ''),
       COALESCE(s.success_count, 0),
       s.avg_latency_ms,
       COALESCE(e.error_count, 0),
       a.temp_unschedulable_until,
       COALESCE(a.temp_unschedulable_reason, '')
FROM accounts a
LEFT JOIN success_stats s ON s.account_id = a.id
LEFT JOIN error_stats e ON e.account_id = a.id
WHERE a.deleted_at IS NULL
  AND ($2 = '' OR a.name ILIKE '%' || $2 || '%' OR CAST(a.id AS TEXT) = $2)
  AND ($3 = '' OR LOWER(a.platform) = LOWER($3))
ORDER BY a.id`

	rows, err := r.db.QueryContext(ctx, query, since, strings.TrimSpace(filter.Search), strings.TrimSpace(filter.Platform))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := make([]service.AccountHealthWindowStat, 0)
	for rows.Next() {
		var stat service.AccountHealthWindowStat
		var avgLatency sql.NullFloat64
		var unschedulableUntil sql.NullTime
		if err := rows.Scan(
			&stat.AccountID,
			&stat.Name,
			&stat.Platform,
			&stat.AccountStatus,
			&stat.SuccessCount,
			&avgLatency,
			&stat.ErrorCount,
			&unschedulableUntil,
			&stat.TempUnschedulableReason,
		); err != nil {
			return nil, err
		}
		if avgLatency.Valid {
			value := avgLatency.Float64
			stat.AvgLatencyMS = &value
		}
		if unschedulableUntil.Valid {
			value := unschedulableUntil.Time
			stat.TempUnschedulableUntil = &value
		}
		stats = append(stats, stat)
	}
	return stats, rows.Err()
}

func (r *accountHealthRepository) SetAutoIsolation(ctx context.Context, accountID int64, until time.Time, reason string) (bool, error) {
	if !strings.HasPrefix(reason, service.AccountHealthAutoReasonPrefix) {
		return false, fmt.Errorf("account health auto isolation reason must start with %q", service.AccountHealthAutoReasonPrefix)
	}
	return r.execHealthIsolationMutation(ctx, `
WITH updated AS (
  UPDATE accounts AS a
  SET temp_unschedulable_until = CASE
        WHEN a.temp_unschedulable_until IS NULL OR a.temp_unschedulable_until <= NOW() THEN $2
        ELSE GREATEST(a.temp_unschedulable_until, $2)
      END,
      temp_unschedulable_reason = $3,
      updated_at = NOW()
  WHERE a.id = $1
    AND a.deleted_at IS NULL
    AND a.status = 'active'
    AND a.schedulable IS TRUE
    AND $3 LIKE 'health:auto:%'
    AND (
      a.temp_unschedulable_until IS NULL
      OR a.temp_unschedulable_until <= NOW()
      OR a.temp_unschedulable_reason LIKE 'health:auto:%'
    )
  RETURNING a.id
)
INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
SELECT $4, updated.id, NULL, NULL FROM updated`, accountID, until, reason, service.SchedulerOutboxEventAccountChanged)
}

func (r *accountHealthRepository) ClearAutoIsolation(ctx context.Context, accountID int64) (bool, error) {
	return r.execHealthIsolationMutation(ctx, `
WITH updated AS (
  UPDATE accounts AS a
  SET temp_unschedulable_until = NULL,
      temp_unschedulable_reason = NULL,
      updated_at = NOW()
  WHERE a.id = $1
    AND a.deleted_at IS NULL
    AND a.temp_unschedulable_reason LIKE 'health:auto:%'
  RETURNING a.id
)
INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
SELECT $2, updated.id, NULL, NULL FROM updated`, accountID, service.SchedulerOutboxEventAccountChanged)
}

func (r *accountHealthRepository) SetManualIsolation(ctx context.Context, accountID int64, until time.Time, reason string) (bool, error) {
	if !strings.HasPrefix(reason, service.AccountHealthManualReasonPrefix) {
		return false, fmt.Errorf("account health manual isolation reason must start with %q", service.AccountHealthManualReasonPrefix)
	}
	return r.execHealthIsolationMutation(ctx, `
WITH updated AS (
  UPDATE accounts AS a
  SET temp_unschedulable_until = $2,
      temp_unschedulable_reason = $3,
      updated_at = NOW()
  WHERE a.id = $1
    AND a.deleted_at IS NULL
    AND a.status = 'active'
    AND a.schedulable IS TRUE
    AND $3 LIKE 'health:manual:%'
    AND (
      a.temp_unschedulable_until IS NULL
      OR a.temp_unschedulable_until <= NOW()
      OR a.temp_unschedulable_reason LIKE 'health:auto:%'
      OR a.temp_unschedulable_reason LIKE 'health:manual:%'
    )
  RETURNING a.id
)
INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
SELECT $4, updated.id, NULL, NULL FROM updated`, accountID, until, reason, service.SchedulerOutboxEventAccountChanged)
}

func (r *accountHealthRepository) ClearHealthIsolation(ctx context.Context, accountID int64) (bool, error) {
	return r.execHealthIsolationMutation(ctx, `
WITH updated AS (
  UPDATE accounts AS a
  SET temp_unschedulable_until = NULL,
      temp_unschedulable_reason = NULL,
      updated_at = NOW()
  WHERE a.id = $1
    AND a.deleted_at IS NULL
    AND (
      a.temp_unschedulable_reason LIKE 'health:auto:%'
      OR a.temp_unschedulable_reason LIKE 'health:manual:%'
    )
  RETURNING a.id
)
INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
SELECT $2, updated.id, NULL, NULL FROM updated`, accountID, service.SchedulerOutboxEventAccountChanged)
}

func (r *accountHealthRepository) execHealthIsolationMutation(ctx context.Context, query string, args ...any) (bool, error) {
	if r == nil || r.db == nil {
		return false, fmt.Errorf("account health repository database is unavailable")
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}
