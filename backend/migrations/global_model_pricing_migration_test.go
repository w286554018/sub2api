package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGlobalModelPricingMigration(t *testing.T) {
	content, err := FS.ReadFile("238_global_model_pricing.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS global_model_pricing")
	require.Contains(t, sql, "UNIQUE INDEX IF NOT EXISTS idx_global_model_pricing_pattern")
	require.Contains(t, sql, "billing_mode IN ('token', 'per_request', 'image', 'video')")
	require.Contains(t, sql, "input_price IS NOT NULL AND output_price IS NOT NULL")
	require.Contains(t, sql, "billing_mode <> 'token' AND input_price IS NULL")
	require.Contains(t, sql, "cache_read_price IS NULL AND per_request_price IS NOT NULL")
	require.Contains(t, sql, "position('*' IN model_pattern) = 0 OR right(model_pattern, 1) = '*'")
	require.Contains(t, sql, "input_price IS NULL OR (input_price >= 0 AND input_price < 'Infinity'::double precision)")
}
