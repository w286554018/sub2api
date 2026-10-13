//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// The deleted-key lookup was removed together with credential retention. Keep
// this integration regression on the supported DeleteWithAudit compatibility
// API: deletion releases the unique credential without retaining its material.
func TestAPIKeyDeletionDoesNotRetainCredentialAudit(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{})
	credential := fmt.Sprintf("sk-delete-audit-%d", user.ID)
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: credential, Name: "original"})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM api_keys WHERE user_id = $1", user.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", user.ID)
	})

	repo := NewAPIKeyRepository(client, integrationDB)
	require.NoError(t, repo.DeleteWithAudit(ctx, key.ID))
	require.NoError(t, repo.DeleteWithAudit(ctx, key.ID), "deletion remains idempotent")
	_, err := repo.GetByKey(ctx, credential)
	require.ErrorIs(t, err, service.ErrAPIKeyNotFound)

	var storedCredential string
	var deletedAt sql.NullTime
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT key, deleted_at FROM api_keys WHERE id = $1", key.ID).Scan(&storedCredential, &deletedAt))
	require.True(t, deletedAt.Valid)
	require.NotEqual(t, credential, storedCredential)
	require.Contains(t, storedCredential, fmt.Sprintf("__deleted__%d__", key.ID))

	var auditCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM deleted_api_key_audits WHERE api_key_id = $1", key.ID).Scan(&auditCount))
	require.Zero(t, auditCount)
	// A replacement key can reuse the released unique value.
	replacement := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: credential, Name: "replacement"})
	require.NotEqual(t, key.ID, replacement.ID)
}
