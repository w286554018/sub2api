package handler

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRefreshTokenBackendModeUsesSharedAdminRoleCheck(t *testing.T) {
	content, err := os.ReadFile("auth_handler.go")
	require.NoError(t, err)

	source := string(content)
	require.Contains(t, source, "service.IsAdminRole(result.UserRole)")
	require.NotContains(t, source, `result.UserRole != "admin"`)
}
