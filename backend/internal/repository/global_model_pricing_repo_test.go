package repository

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func newGlobalPricingRepoTest(t *testing.T) (service.GlobalModelPricingRepository, sqlmock.Sqlmock, func()) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	return NewGlobalModelPricingRepository(db), mock, func() { _ = db.Close() }
}

func globalPricingRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "model_pattern", "billing_mode", "input_price", "output_price", "cache_write_price", "cache_write_1h_price", "cache_read_price", "per_request_price", "enabled", "created_at", "updated_at"})
}

func TestGlobalModelPricingRepositoryCreateNormalizesAndReturnsRow(t *testing.T) {
	repo, mock, cleanup := newGlobalPricingRepoTest(t)
	defer cleanup()
	now := time.Now()
	input, output := 0.000001, 0.000002
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO global_model_pricing")).
		WithArgs("gpt-5", "token", input, output, nil, nil, nil, nil).
		WillReturnRows(globalPricingRows().AddRow(int64(1), "gpt-5", "token", input, output, nil, nil, nil, nil, true, now, now))

	got, err := repo.Create(context.Background(), service.GlobalModelPriceInput{
		ModelPattern: " GPT-5 ", InputPrice: &input, OutputPrice: &output,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), got.ID)
	require.Equal(t, "gpt-5", got.ModelPattern)
	require.Equal(t, input, *got.InputPrice)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGlobalModelPricingRepositoryUpdateDoesNotWriteEnabled(t *testing.T) {
	repo, mock, cleanup := newGlobalPricingRepoTest(t)
	defer cleanup()
	now := time.Now()
	perRequest := 0.01
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE global_model_pricing SET")).
		WithArgs(int64(7), "imagen-*", "image", nil, nil, nil, nil, nil, perRequest).
		WillReturnRows(globalPricingRows().AddRow(int64(7), "imagen-*", "image", nil, nil, nil, nil, nil, perRequest, false, now, now))

	got, err := repo.Update(context.Background(), 7, service.GlobalModelPriceInput{
		ModelPattern: "imagen-*", BillingMode: service.BillingModeImage, PerRequestPrice: &perRequest,
	})
	require.NoError(t, err)
	require.False(t, got.Enabled)
	require.Equal(t, perRequest, *got.PerRequestPrice)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGlobalModelPricingRepositorySetEnabledReturnsRow(t *testing.T) {
	repo, mock, cleanup := newGlobalPricingRepoTest(t)
	defer cleanup()
	now := time.Now()
	input, output := 1.0, 2.0
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE global_model_pricing SET enabled = $2")).
		WithArgs(int64(9), false).
		WillReturnRows(globalPricingRows().AddRow(int64(9), "gpt-5", "token", input, output, nil, nil, nil, nil, false, now, now))

	got, err := repo.SetEnabled(context.Background(), 9, false)
	require.NoError(t, err)
	require.False(t, got.Enabled)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGlobalModelPricingRepositoryDuplicate(t *testing.T) {
	repo, mock, cleanup := newGlobalPricingRepoTest(t)
	defer cleanup()
	input, output := 1.0, 2.0
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO global_model_pricing")).
		WillReturnError(&pq.Error{Code: "23505"})

	_, err := repo.Create(context.Background(), service.GlobalModelPriceInput{
		ModelPattern: "gpt-5", InputPrice: &input, OutputPrice: &output,
	})
	require.ErrorIs(t, err, service.ErrGlobalModelPricingDuplicate)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGlobalModelPricingRepositoryNotFound(t *testing.T) {
	repo, mock, cleanup := newGlobalPricingRepoTest(t)
	defer cleanup()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + globalModelPricingColumns + " FROM global_model_pricing WHERE id = $1")).
		WithArgs(int64(404)).
		WillReturnError(sql.ErrNoRows)

	_, err := repo.GetByID(context.Background(), 404)
	require.ErrorIs(t, err, sql.ErrNoRows)
	require.NoError(t, mock.ExpectationsWereMet())
}
