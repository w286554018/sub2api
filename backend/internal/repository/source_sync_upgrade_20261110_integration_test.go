//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestSourceSyncUpgrade20261110From31fProductionBaseline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var manifest struct {
		Baseline   string
		Migrations map[string]map[string]string
		Additions  []string
	}
	raw, err := os.ReadFile("testdata/source_sync_31f924258_migrations.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.Equal(t, "31f92425856c800245a85b2d858728b9fd5cbc93", manifest.Baseline)
	require.Len(t, manifest.Migrations, 419)
	require.Len(t, manifest.Additions, 16)
	files, err := fs.Glob(migrations.FS, "*.sql")
	require.NoError(t, err)
	require.Len(t, files, 435)
	baseline := fstest.MapFS{}
	baselineChecksums := map[string]string{}
	candidateChecksums := map[string]string{}
	var additions []string
	for _, name := range files {
		content, readErr := migrations.FS.ReadFile(name)
		require.NoError(t, readErr, name)
		digest := sha256.Sum256([]byte(strings.TrimSpace(string(content))))
		candidateChecksums[name] = hex.EncodeToString(digest[:])
		published, ok := manifest.Migrations[name]
		if !ok {
			additions = append(additions, name)
			continue
		}
		rawDigest := sha256.Sum256(content)
		require.Equal(t, published["raw_sha256"], hex.EncodeToString(rawDigest[:]), "published raw SQL changed: "+name)
		require.Equal(t, published["checksum"], candidateChecksums[name], "published ledger changed: "+name)
		baseline[name] = &fstest.MapFile{Data: content, Mode: 0444}
		baselineChecksums[name] = published["checksum"]
	}
	require.Len(t, baseline, 419)
	sort.Strings(additions)
	require.Equal(t, manifest.Additions, additions)
	require.Contains(t, baseline, "325_account_performance_monitor.sql")
	require.Contains(t, baseline, "325_lottery_invitations.sql")
	db := sourceSyncUpgradeDB(t)
	require.NoError(t, applyMigrationsFS(ctx, db, baseline))
	require.Equal(t, baselineChecksums, readRanxiUpgradeChecksums(t, db))
	userID, keyID, accountID, quotaID := seedSourceSyncUpgrade20261031Fixture(t, ctx, db)
	before := sourceSyncUpgrade20261031FinancialState(t, ctx, db, userID, keyID, quotaID)
	// The new API-key policy adds these three defaults. Preserve the complete
	// historical snapshot while explicitly checking only the reviewed additions.
	var expectedFinancialState map[string]any
	require.NoError(t, json.Unmarshal([]byte(before), &expectedFinancialState))
	expectedAPIKey, ok := expectedFinancialState["api_key"].(map[string]any)
	require.True(t, ok)
	expectedAPIKey["model_mapping"] = map[string]any{}
	expectedAPIKey["reasoning_effort"] = ""
	expectedAPIKey["allow_long_context"] = true
	expectedFinancialJSON, err := json.Marshal(expectedFinancialState)
	require.NoError(t, err)
	assertSourceSyncUpgrade20261031LegacyOAuthSlots(t, ctx, db, accountID)
	_, err = db.ExecContext(ctx, "INSERT INTO users(email,password_hash,role,balance,frozen_balance) VALUES ('source-sync-20261110-admin@example.test','test','admin',17,2), ('source-sync-20261110-observer@example.test','test','observer',19,3), ('source-sync-20261110-user@example.test','test','user',23,5)")
	require.NoError(t, err)
	const rolesQuery = "SELECT jsonb_agg(jsonb_build_object('id',id,'role',role,'status',status,'balance',balance,'frozen_balance',frozen_balance) ORDER BY id)::text FROM users WHERE email LIKE 'source-sync-20261110-%@example.test'"
	var rolesBefore string
	require.NoError(t, db.QueryRowContext(ctx, rolesQuery).Scan(&rolesBefore))
	for pass := 1; pass <= 2; pass++ {
		require.NoError(t, ApplyMigrations(ctx, db))
		require.Equal(t, candidateChecksums, readRanxiUpgradeChecksums(t, db), "full-filename ledger pass %d", pass)
		require.JSONEq(t, string(expectedFinancialJSON), sourceSyncUpgrade20261031FinancialState(t, ctx, db, userID, keyID, quotaID), "financial state and reviewed API-key defaults pass %d", pass)
		assertSourceSyncUpgrade20261031LegacyOAuthSlots(t, ctx, db, accountID)
		var rolesAfter string
		require.NoError(t, db.QueryRowContext(ctx, rolesQuery).Scan(&rolesAfter))
		require.JSONEq(t, rolesBefore, rolesAfter, "protected roles and funds pass %d", pass)
		for _, table := range []string{"auto_credit_ledger", "auto_credit_audit", "auto_credit_cache_outbox"} {
			var count int
			var exists bool
			require.NoError(t, db.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists))
			require.True(t, exists, "required auto-credit table %s", table)
			require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count))
			require.Zero(t, count, table)
		}
		t.Logf("baseline=%s published=419 candidate=435 upgrade_pass=%d financial_state=preserved", manifest.Baseline, pass)
	}
}
