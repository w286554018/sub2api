package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIntelligentTestsMigrationDefinesDurableQueueContracts(t *testing.T) {
	content, err := FS.ReadFile("240_intelligent_tests.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS test_settings")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS account_tests")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS intelligent_test_requests")
	require.Contains(t, sql, "CREATE UNIQUE INDEX IF NOT EXISTS idx_account_tests_active")
	require.Contains(t, sql, "WHERE status IN ('queued','running')")
	require.Contains(t, sql, "lease_token VARCHAR(64)")
	require.Contains(t, sql, "lease_until TIMESTAMPTZ")
	require.Contains(t, sql, "available_at TIMESTAMPTZ NOT NULL DEFAULT NOW()")
	require.Contains(t, sql, "PRIMARY KEY(actor_id, request_key)")
	require.NotContains(t, strings.ToLower(sql), "user_visible")
	require.NotContains(t, strings.ToLower(sql), "access_token")
	require.NotContains(t, strings.ToLower(sql), "refresh_token")
}
