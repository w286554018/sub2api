package repository

import (
	"context"
	"database/sql"
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
