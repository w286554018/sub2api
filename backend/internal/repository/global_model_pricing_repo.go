package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type globalModelPricingRepository struct {
	sql sqlExecutor
}

func NewGlobalModelPricingRepository(sqlDB *sql.DB) service.GlobalModelPricingRepository {
	return &globalModelPricingRepository{sql: sqlDB}
}

const globalModelPricingColumns = `id, model_pattern, billing_mode, input_price, output_price, cache_write_price, cache_write_1h_price, cache_read_price, per_request_price, enabled, created_at, updated_at`

func scanGlobalModelPricingRow(scan func(dest ...any) error) (*service.GlobalModelPrice, error) {
	var p service.GlobalModelPrice
	var input, output, cacheWrite, cacheWrite1h, cacheRead, perRequest sql.NullFloat64
	var created, updated sql.NullTime
	if err := scan(&p.ID, &p.ModelPattern, &p.BillingMode, &input, &output, &cacheWrite, &cacheWrite1h, &cacheRead, &perRequest, &p.Enabled, &created, &updated); err != nil {
		return nil, err
	}
	if input.Valid {
		p.InputPrice = &input.Float64
	}
	if output.Valid {
		p.OutputPrice = &output.Float64
	}
	if cacheWrite.Valid {
		p.CacheWritePrice = &cacheWrite.Float64
	}
	if cacheWrite1h.Valid {
		p.CacheWrite1hPrice = &cacheWrite1h.Float64
	}
	if cacheRead.Valid {
		p.CacheReadPrice = &cacheRead.Float64
	}
	if perRequest.Valid {
		p.PerRequestPrice = &perRequest.Float64
	}
	if created.Valid {
		p.CreatedAt = created.Time
	}
	if updated.Valid {
		p.UpdatedAt = updated.Time
	}
	return &p, nil
}

func (r *globalModelPricingRepository) ListEnabled(ctx context.Context) ([]service.GlobalModelPrice, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT `+globalModelPricingColumns+` FROM global_model_pricing WHERE enabled = TRUE ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]service.GlobalModelPrice, 0)
	for rows.Next() {
		p, err := scanGlobalModelPricingRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (r *globalModelPricingRepository) ListAll(ctx context.Context) ([]service.GlobalModelPrice, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT `+globalModelPricingColumns+` FROM global_model_pricing ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]service.GlobalModelPrice, 0)
	for rows.Next() {
		p, err := scanGlobalModelPricingRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func globalPricingNullFloat64(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func (r *globalModelPricingRepository) Create(ctx context.Context, in service.GlobalModelPriceInput) (*service.GlobalModelPrice, error) {
	pattern := strings.ToLower(strings.TrimSpace(in.ModelPattern))
	mode := strings.TrimSpace(string(in.BillingMode))
	if mode == "" {
		mode = string(service.BillingModeToken)
	}
	rows, err := r.sql.QueryContext(ctx, `
		INSERT INTO global_model_pricing
			(model_pattern, billing_mode, input_price, output_price, cache_write_price,
			 cache_write_1h_price, cache_read_price, per_request_price, enabled, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,TRUE,NOW(),NOW())
		RETURNING `+globalModelPricingColumns,
		pattern, mode, globalPricingNullFloat64(in.InputPrice), globalPricingNullFloat64(in.OutputPrice),
		globalPricingNullFloat64(in.CacheWritePrice), globalPricingNullFloat64(in.CacheWrite1hPrice),
		globalPricingNullFloat64(in.CacheReadPrice), globalPricingNullFloat64(in.PerRequestPrice))
	if err != nil {
		if isUniqueConstraintViolation(err) {
			return nil, fmt.Errorf("create global model pricing %q: %w", pattern, service.ErrGlobalModelPricingDuplicate)
		}
		return nil, fmt.Errorf("create global model pricing: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, fmt.Errorf("create global model pricing: no row returned")
	}
	p, err := scanGlobalModelPricingRow(rows.Scan)
	if err != nil {
		return nil, err
	}
	return p, rows.Err()
}

func (r *globalModelPricingRepository) GetByID(ctx context.Context, id int64) (*service.GlobalModelPrice, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT `+globalModelPricingColumns+` FROM global_model_pricing WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	p, err := scanGlobalModelPricingRow(rows.Scan)
	if err != nil {
		return nil, err
	}
	return p, rows.Err()
}

func (r *globalModelPricingRepository) Update(ctx context.Context, id int64, in service.GlobalModelPriceInput) (*service.GlobalModelPrice, error) {
	pattern := strings.ToLower(strings.TrimSpace(in.ModelPattern))
	mode := strings.TrimSpace(string(in.BillingMode))
	if mode == "" {
		mode = string(service.BillingModeToken)
	}
	rows, err := r.sql.QueryContext(ctx, `
		UPDATE global_model_pricing SET
			model_pattern = $2, billing_mode = $3,
			input_price = $4, output_price = $5,
			cache_write_price = $6, cache_write_1h_price = $7, cache_read_price = $8,
			per_request_price = $9, updated_at = NOW()
		WHERE id = $1
		RETURNING `+globalModelPricingColumns, id, pattern, mode,
		globalPricingNullFloat64(in.InputPrice), globalPricingNullFloat64(in.OutputPrice),
		globalPricingNullFloat64(in.CacheWritePrice), globalPricingNullFloat64(in.CacheWrite1hPrice),
		globalPricingNullFloat64(in.CacheReadPrice), globalPricingNullFloat64(in.PerRequestPrice))
	if err != nil {
		if isUniqueConstraintViolation(err) {
			return nil, fmt.Errorf("update global model pricing %q: %w", pattern, service.ErrGlobalModelPricingDuplicate)
		}
		return nil, fmt.Errorf("update global model pricing: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	p, err := scanGlobalModelPricingRow(rows.Scan)
	if err != nil {
		return nil, err
	}
	return p, rows.Err()
}

func (r *globalModelPricingRepository) Delete(ctx context.Context, id int64) error {
	rows, err := r.sql.QueryContext(ctx, `DELETE FROM global_model_pricing WHERE id = $1 RETURNING id`, id)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return sql.ErrNoRows
	}
	return rows.Err()
}

func (r *globalModelPricingRepository) SetEnabled(ctx context.Context, id int64, enabled bool) (*service.GlobalModelPrice, error) {
	rows, err := r.sql.QueryContext(ctx, `UPDATE global_model_pricing SET enabled = $2, updated_at = NOW() WHERE id = $1 RETURNING `+globalModelPricingColumns, id, enabled)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	p, err := scanGlobalModelPricingRow(rows.Scan)
	if err != nil {
		return nil, err
	}
	return p, rows.Err()
}
