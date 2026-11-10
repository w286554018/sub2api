//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func (s *UserRepoSuite) TestEmailBlacklistProbeOnlyTargetsRegularUsers() {
	target := s.mustCreateUser(&service.User{Email: "compat-blacklist@gmail.com", Role: service.RoleUser})
	for i, role := range []string{service.RoleAdmin, service.RoleSuperAdmin, service.RoleObserver} {
		s.mustCreateUser(&service.User{
			Email: fmt.Sprintf("compat-blacklist+staff%d@gmail.com", i), Role: role,
		})
	}
	got, err := s.repo.ListActiveUsersByEmailAliasProbeKeys(s.ctx, emailBlacklistProbeKeys(target.Email))
	s.Require().NoError(err)
	s.Require().Len(got, 1)
	s.Require().Equal(target.ID, got[0].ID)
}

func TestEmailBlacklistCompatibilityProtectsStaffGrantsAndInvalidations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	users := NewUserRepository(integrationEntClient, integrationDB)
	blacklist := users.(service.UserEmailBlacklistRepository)
	var ids []int64
	type trackedUser struct {
		fixture mcpOAuthIntegrationFixture
		role    string
		key     string
	}
	var tracked []trackedUser
	for _, role := range []string{service.RoleUser, service.RoleAdmin, service.RoleSuperAdmin, service.RoleObserver} {
		f := newMCPOAuthIntegrationFixture(t, ctx)
		// Change role directly so setup does not revoke the existing grant.
		_, err := integrationDB.ExecContext(ctx, `UPDATE users SET role=$2 WHERE id=$1`, f.userID, role)
		require.NoError(t, err)
		key := fmt.Sprintf("sk-blacklist-compat-%d-%d", f.userID, time.Now().UnixNano())
		_, err = integrationDB.ExecContext(ctx,
			`INSERT INTO api_keys(user_id,key,name) VALUES($1,$2,'blacklist compatibility')`, f.userID, key)
		require.NoError(t, err)
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cleanupCancel()
			_, cleanupErr := integrationDB.ExecContext(cleanupCtx, `DELETE FROM api_keys WHERE key=$1`, key)
			require.NoError(t, cleanupErr)
			_, cleanupErr = integrationDB.ExecContext(cleanupCtx,
				`DELETE FROM auth_cache_invalidation_outbox WHERE cache_key=encode(sha256(convert_to($1,'UTF8')),'hex')`, key)
			require.NoError(t, cleanupErr)
		})
		ids = append(ids, f.userID)
		tracked = append(tracked, trackedUser{fixture: f, role: role, key: key})
	}
	disabled, err := blacklist.BatchDisableUsers(ctx, ids)
	require.NoError(t, err)
	require.Equal(t, []int64{tracked[0].fixture.userID}, disabled)
	for _, item := range tracked {
		got, err := users.GetByID(ctx, item.fixture.userID)
		require.NoError(t, err)
		grant, err := item.fixture.repo.GetGrant(ctx, item.fixture.grantID)
		require.NoError(t, err)
		var count int
		require.NoError(t, integrationDB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM auth_cache_invalidation_outbox WHERE cache_key=encode(sha256(convert_to($1,'UTF8')),'hex')`,
			item.key).Scan(&count))
		if item.role == service.RoleUser {
			require.Equal(t, service.StatusDisabled, got.Status)
			require.NotNil(t, grant.RevokedAt)
			require.Equal(t, 1, count, "migration 184 must enqueue exactly once for the disabled user's API key")
		} else {
			require.Equal(t, service.StatusActive, got.Status, item.role)
			require.Nil(t, grant.RevokedAt, item.role)
			require.Zero(t, count, item.role)
		}
	}
}

func TestEmailBlacklistCompatibilityDisableGrantAndOutboxRollbackTogether(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := newMCPOAuthIntegrationFixture(t, ctx)
	key := fmt.Sprintf("sk-blacklist-rollback-%d-%d", f.userID, time.Now().UnixNano())
	_, err := integrationDB.ExecContext(ctx,
		`INSERT INTO api_keys(user_id,key,name) VALUES($1,$2,'blacklist rollback')`, f.userID, key)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, cleanupErr := integrationDB.ExecContext(cleanupCtx, `DELETE FROM api_keys WHERE key=$1`, key)
		require.NoError(t, cleanupErr)
		_, cleanupErr = integrationDB.ExecContext(cleanupCtx,
			`DELETE FROM auth_cache_invalidation_outbox WHERE cache_key=encode(sha256(convert_to($1,'UTF8')),'hex')`, key)
		require.NoError(t, cleanupErr)
	})
	tx, err := integrationEntClient.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	users := NewUserRepository(tx.Client(), integrationDB).(service.UserEmailBlacklistRepository)
	disabled, err := users.BatchDisableUsers(txCtx, []int64{f.userID})
	require.NoError(t, err)
	require.Equal(t, []int64{f.userID}, disabled)
	exec := txAwareSQLExecutor(txCtx, integrationDB, tx.Client())
	rows, err := exec.QueryContext(txCtx, `
SELECT u.status,
       (SELECT revoked_at IS NOT NULL FROM mcp_oauth_grants WHERE id=$2),
       (SELECT COUNT(*) FROM auth_cache_invalidation_outbox
        WHERE cache_key=encode(sha256(convert_to($3,'UTF8')),'hex'))
FROM users u WHERE u.id=$1`, f.userID, f.grantID, key)
	require.NoError(t, err)
	require.True(t, rows.Next())
	var status string
	var revoked bool
	var count int
	require.NoError(t, rows.Scan(&status, &revoked, &count))
	require.NoError(t, rows.Close())
	require.Equal(t, service.StatusDisabled, status)
	require.True(t, revoked)
	require.Equal(t, 1, count)
	require.NoError(t, tx.Rollback())

	var outsideStatus string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status FROM users WHERE id=$1`, f.userID).Scan(&outsideStatus))
	require.Equal(t, service.StatusActive, outsideStatus)
	grant, err := f.repo.GetGrant(ctx, f.grantID)
	require.NoError(t, err)
	require.Nil(t, grant.RevokedAt)
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM auth_cache_invalidation_outbox WHERE cache_key=encode(sha256(convert_to($1,'UTF8')),'hex')`,
		key).Scan(&count))
	require.Zero(t, count, "rolled-back user changes must not publish invalidations")
}
