package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSuperAdminMigrationPromotesEarliestActiveAdmin(t *testing.T) {
	content, err := FS.ReadFile("239_super_admin.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "UPDATE users SET role = 'super_admin'")
	require.Contains(t, sql, "WHERE role = 'admin'")
	require.Contains(t, sql, "AND status = 'active'")
	require.Contains(t, sql, "AND deleted_at IS NULL")
	require.Contains(t, sql, "NOT EXISTS")
	require.Contains(t, sql, "WHERE role = 'super_admin'")
	require.Contains(t, sql, "ORDER BY created_at ASC, id ASC")
	require.Contains(t, sql, "LIMIT 1")
}
