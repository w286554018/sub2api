//go:build integration

package repository

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

const (
	sourceSyncUpgrade20261031ProductionCommit = "27788bdcd740dc84cff7ddc7a8aff7d78f7dfc0f"
	sourceSyncUpgrade20261031BaselineCount    = 399
	sourceSyncUpgrade20261031CandidateCount   = 411
	sourceSyncUpgrade20261031BPSMigration     = "319_openai_bps_supported_capabilities.sql"
	sourceSyncUpgrade20261031CandidateSuffix  = `310_scoped_admin_api_keys.sql
311_group_image_count_billing.sql
312_openai_auth_connections.sql
312_upstream_channel_entities.sql
313_openai_connection_scheduling.sql
313_upstream_channel_binding_delete_revision.sql
314_openai_connection_usage_attribution.sql
315_openai_connection_affinity.sql
316_openai_connection_preserve_legacy_extra.sql
317_openai_connection_snapshot_object.sql
318_openai_connection_refresh_fencing.sql
319_openai_bps_supported_capabilities.sql`

	// SHA-256 over sorted "filename:git-blob-id\n" entries from the production
	// commit tree. Git blob IDs cover the exact raw SQL bytes, including whitespace.
	sourceSyncUpgrade20261031BaselineBlobAggregate = "44027dd0fc7d313a8bb63a3d3bd292684b2c4226eea9d46ea3b46131438e2d6a"
	// SHA-256 over sorted "filename:checksum\n" entries in manifest-final.json's
	// expected_migrations map (the migration runner hashes trimmed SQL text).
	sourceSyncUpgrade20261031BaselineLedgerAggregate = "9494f20090fc78dbe78c4f554193349d55ec308322ae47b7e7c2adaaecd80fb4"
)

func TestSourceSyncUpgrade20261031From27788ProductionBaseline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	files, err := fs.Glob(migrations.FS, "*.sql")
	require.NoError(t, err)
	require.Len(t, files, sourceSyncUpgrade20261031CandidateCount,
		"this fixture targets the reviewed 411-migration candidate and excludes later releases")
	require.Equal(t, sourceSyncUpgrade20261031BPSMigration, files[len(files)-1])
	var candidateSuffix []string
	for _, name := range files {
		if name >= "310_" {
			candidateSuffix = append(candidateSuffix, name)
		}
	}
	require.Equal(t, strings.Fields(sourceSyncUpgrade20261031CandidateSuffix), candidateSuffix,
		"the candidate suffix must match the reviewed 310-319 migration set")

	baseline := fstest.MapFS{}
	pre319 := fstest.MapFS{}
	baselineChecksums := make(map[string]string, sourceSyncUpgrade20261031BaselineCount)
	candidateChecksums := make(map[string]string, len(files))
	var blobAggregate, ledgerAggregate strings.Builder
	for _, name := range files {
		content, readErr := migrations.FS.ReadFile(name)
		require.NoError(t, readErr, name)
		file := &fstest.MapFile{Data: content, Mode: 0444}
		if name != sourceSyncUpgrade20261031BPSMigration {
			pre319[name] = file
		}

		checksum := sha256.Sum256([]byte(strings.TrimSpace(string(content))))
		candidateChecksums[name] = hex.EncodeToString(checksum[:])
		if name >= "310_" {
			continue
		}

		baseline[name] = file
		baselineChecksums[name] = candidateChecksums[name]
		fmt.Fprintf(&blobAggregate, "%s:%s\n", name, sourceSyncUpgrade20261031GitBlobID(content))
		fmt.Fprintf(&ledgerAggregate, "%s:%s\n", name, candidateChecksums[name])
	}
	require.Len(t, baseline, sourceSyncUpgrade20261031BaselineCount)
	require.Len(t, pre319, sourceSyncUpgrade20261031CandidateCount-1)
	require.Equal(t, sourceSyncUpgrade20261031BaselineBlobAggregate,
		sourceSyncUpgrade20261031SHA256(blobAggregate.String()),
		"the production migration filenames and raw SQL bytes must match commit "+sourceSyncUpgrade20261031ProductionCommit)
	require.Equal(t, sourceSyncUpgrade20261031BaselineLedgerAggregate,
		sourceSyncUpgrade20261031SHA256(ledgerAggregate.String()),
		"the 399 production migration checksums must match manifest-final.json")

	db := sourceSyncUpgradeDB(t)
	require.NoError(t, applyMigrationsFS(ctx, db, baseline))
	require.Equal(t, baselineChecksums, readRanxiUpgradeChecksums(t, db),
		"the isolated starting schema must have the exact published migration ledger")

	userID, keyID, accountID, quotaID := seedSourceSyncUpgrade20261031Fixture(t, ctx, db)
	financialBefore := sourceSyncUpgrade20261031FinancialState(t, ctx, db, userID, keyID, quotaID)
	assertSourceSyncUpgrade20261031LegacyOAuthSlots(t, ctx, db, accountID)

	// Apply the candidate through migration 318 first so migration 319 sees
	// pre-existing BPS capability rows and must perform its documented cleanup.
	require.NoError(t, applyMigrationsFS(ctx, db, pre319))
	pre319Checksums := make(map[string]string, len(candidateChecksums)-1)
	for name, checksum := range candidateChecksums {
		if name != sourceSyncUpgrade20261031BPSMigration {
			pre319Checksums[name] = checksum
		}
	}
	require.Equal(t, pre319Checksums, readRanxiUpgradeChecksums(t, db))
	require.JSONEq(t, financialBefore,
		sourceSyncUpgrade20261031FinancialState(t, ctx, db, userID, keyID, quotaID),
		"candidate migrations through 318 must preserve existing billing rows and balances")
	assertSourceSyncUpgrade20261031LegacyOAuthSlots(t, ctx, db, accountID)
	var adoptedSlots int
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT count(*) FROM openai_auth_connections WHERE account_id=$1", accountID).Scan(&adoptedSlots))
	require.Zero(t, adoptedSlots, "legacy OAuth slots must not be copied into a second credential lifecycle")

	const changedBPSID = "00000000-0000-4000-8000-000000000319"
	const unchangedBPSID = "00000000-0000-4000-8000-000000000318"
	const unchangedCodexID = "00000000-0000-4000-8000-000000000317"
	_, err = db.ExecContext(ctx, `INSERT INTO openai_auth_connections
		(id,account_id,kind,label,status,capabilities,credentials_ciphertext,revision)
		VALUES
		($1::uuid,$4,'bps','synthetic BPS chat capability','active','["responses","chat_completions"]','fake-encrypted-bps-slot',7),
		($2::uuid,$4,'bps','synthetic BPS responses capability','active','["responses"]','fake-encrypted-bps-clean-slot',12),
		($3::uuid,$4,'codex','synthetic Codex chat capability','active','["chat_completions"]','fake-encrypted-codex-slot',3)`,
		changedBPSID, unchangedBPSID, unchangedCodexID, accountID)
	require.NoError(t, err)
	assertSourceSyncUpgrade20261031LegacyOAuthSlots(t, ctx, db, accountID)

	for pass := 1; pass <= 2; pass++ {
		require.NoError(t, ApplyMigrations(ctx, db))
		require.Equal(t, candidateChecksums, readRanxiUpgradeChecksums(t, db),
			"all 411 candidate migration checksums must remain stable on pass %d", pass)
		require.JSONEq(t, financialBefore,
			sourceSyncUpgrade20261031FinancialState(t, ctx, db, userID, keyID, quotaID),
			"candidate upgrade pass %d must preserve existing billing rows and balances", pass)
		assertSourceSyncUpgrade20261031LegacyOAuthSlots(t, ctx, db, accountID)
		assertSourceSyncUpgrade20261031Connection(t, ctx, db, changedBPSID,
			"[\"responses\"]", 8, "bps", "synthetic BPS chat capability", "fake-encrypted-bps-slot")
		assertSourceSyncUpgrade20261031Connection(t, ctx, db, unchangedBPSID,
			"[\"responses\"]", 12, "bps", "synthetic BPS responses capability", "fake-encrypted-bps-clean-slot")
		assertSourceSyncUpgrade20261031Connection(t, ctx, db, unchangedCodexID,
			"[\"chat_completions\"]", 3, "codex", "synthetic Codex chat capability", "fake-encrypted-codex-slot")
		t.Logf("baseline=%s published=%d candidate=%d upgrade_pass=%d historical_finance_and_oauth=preserved bps_capabilities=responses",
			sourceSyncUpgrade20261031ProductionCommit, sourceSyncUpgrade20261031BaselineCount,
			sourceSyncUpgrade20261031CandidateCount, pass)
	}
}

func seedSourceSyncUpgrade20261031Fixture(t *testing.T, ctx context.Context, db *sql.DB) (int64, int64, int64, int64) {
	t.Helper()
	var userID, keyID, accountID, quotaID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO users
		(email,password_hash,role,balance,frozen_balance,concurrency)
		VALUES('upgrade-20261031@example.test','synthetic-test-hash','super_admin',42.75,6.25,9)
		RETURNING id`).Scan(&userID))
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO api_keys
		(user_id,key,name,concurrency_limit,quota,quota_used)
		VALUES($1,'fake-upgrade-key-20261031','existing synthetic key',7,250,3.5) RETURNING id`, userID).Scan(&keyID))
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO accounts
		(name,platform,type,credentials,extra,concurrency)
		VALUES('synthetic-upgrade-oauth','openai','oauth',
		'{"access_token":"fake-access-slot","refresh_token":"fake-refresh-slot","client_id":"fake-client-slot"}',
		'{"legacy_marker":"retain","openai_siwc":true}',11) RETURNING id`).Scan(&accountID))
	_, err := db.ExecContext(ctx, `INSERT INTO openai_excel_oauth_credentials(account_id,credentials_ciphertext)
		VALUES($1,'fake-encrypted-excel-slot')`, accountID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO openai_siwc_credentials(account_id,host_id,credentials_ciphertext)
		VALUES($1,'urn:uuid:synthetic-upgrade-host','fake-encrypted-siwc-slot')`, accountID)
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO user_platform_quotas
		(user_id,platform,daily_limit_usd,weekly_limit_usd,monthly_limit_usd,
		daily_usage_usd,weekly_usage_usd,monthly_usage_usd,daily_window_start,weekly_window_start,monthly_window_start)
		VALUES($1,'openai',10,20,30,1.25,2.5,3.75,
		'2026-10-15T00:00:00Z','2026-10-12T00:00:00Z','2026-10-01T00:00:00Z') RETURNING id`, userID).Scan(&quotaID))

	const requestID = "upgrade-20261031-existing-billing-row"
	_, err = db.ExecContext(ctx, `INSERT INTO usage_logs
		(user_id,api_key_id,account_id,request_id,model,total_cost,actual_cost)
		VALUES($1,$2,$3,$4,'synthetic-upgrade-model',2.125,1.125)`, userID, keyID, accountID, requestID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO usage_billing_quotes
		(request_id,api_key_id,request_fingerprint,quoted_actual_cost,canonical_actual_cost)
		VALUES($1,$2,$3,1.1250000000,1.12500000)`, requestID, keyID, strings.Repeat("a", 64))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO usage_billing_events
		(request_id,api_key_id,request_fingerprint,schema_version,payload,state,result,observed_at)
		VALUES($1,$2,$3,1,'{"fixture":"synthetic historical event"}'::jsonb,
		'complete','{"applied":true}'::jsonb,'2026-10-31T23:00:00Z')`, requestID, keyID, strings.Repeat("b", 64))
	require.NoError(t, err)
	return userID, keyID, accountID, quotaID
}

func sourceSyncUpgrade20261031FinancialState(t *testing.T, ctx context.Context, db *sql.DB, userID, keyID, quotaID int64) string {
	t.Helper()
	const query = `SELECT jsonb_build_object(
		'user',to_jsonb(u),'api_key',to_jsonb(k),'platform_quota',to_jsonb(q),
		'usage_log',(SELECT to_jsonb(l) FROM usage_logs l
			WHERE l.request_id='upgrade-20261031-existing-billing-row' AND l.api_key_id=k.id),
		'billing_quote',(SELECT to_jsonb(bq) FROM usage_billing_quotes bq
			WHERE bq.request_id='upgrade-20261031-existing-billing-row' AND bq.api_key_id=k.id),
		'billing_event',(SELECT to_jsonb(be) FROM usage_billing_events be
			WHERE be.request_id='upgrade-20261031-existing-billing-row' AND be.api_key_id=k.id),
		'quota_projection_rows',(SELECT count(*) FROM usage_platform_quota_totals p WHERE p.quota_id=q.id),
		'owned_money_holds',(SELECT count(*) FROM batch_image_money_holds h WHERE h.api_key_id=k.id))::text
		FROM users u JOIN api_keys k ON k.user_id=u.id
		JOIN user_platform_quotas q ON q.user_id=u.id
		WHERE u.id=$1 AND k.id=$2 AND q.id=$3`
	var state string
	require.NoError(t, db.QueryRowContext(ctx, query, userID, keyID, quotaID).Scan(&state))
	return state
}

func assertSourceSyncUpgrade20261031LegacyOAuthSlots(t *testing.T, ctx context.Context, db *sql.DB, accountID int64) {
	t.Helper()
	var credentials string
	var name, platform, accountType, marker, siwcExtra string
	var concurrency int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT name,platform,type,concurrency,
		credentials::text,extra->>'legacy_marker',extra->>'openai_siwc'
		FROM accounts WHERE id=$1`, accountID).Scan(&name, &platform, &accountType, &concurrency,
		&credentials, &marker, &siwcExtra))
	require.Equal(t, "synthetic-upgrade-oauth", name)
	require.Equal(t, "openai", platform)
	require.Equal(t, "oauth", accountType)
	require.EqualValues(t, 11, concurrency)
	require.JSONEq(t, `{"access_token":"fake-access-slot","refresh_token":"fake-refresh-slot","client_id":"fake-client-slot"}`, credentials)
	require.Equal(t, "retain", marker)
	require.Equal(t, "true", siwcExtra)
	var excelCredentials, siwcCredentials, siwcHost string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT credentials_ciphertext
		FROM openai_excel_oauth_credentials WHERE account_id=$1`, accountID).Scan(&excelCredentials))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT credentials_ciphertext,host_id
		FROM openai_siwc_credentials WHERE account_id=$1`, accountID).Scan(&siwcCredentials, &siwcHost))
	require.Equal(t, "fake-encrypted-excel-slot", excelCredentials)
	require.Equal(t, "fake-encrypted-siwc-slot", siwcCredentials)
	require.Equal(t, "urn:uuid:synthetic-upgrade-host", siwcHost)
}

func assertSourceSyncUpgrade20261031Connection(t *testing.T, ctx context.Context, db *sql.DB, id, capabilities string, revision int64, kind, label, ciphertext string) {
	t.Helper()
	var actualCapabilities, actualKind, actualLabel, actualCiphertext string
	var actualRevision int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT capabilities::text,revision,kind,label,credentials_ciphertext
		FROM openai_auth_connections WHERE id=$1::uuid`, id).Scan(
		&actualCapabilities, &actualRevision, &actualKind, &actualLabel, &actualCiphertext))
	require.JSONEq(t, capabilities, actualCapabilities)
	require.Equal(t, revision, actualRevision)
	require.Equal(t, kind, actualKind)
	require.Equal(t, label, actualLabel)
	require.Equal(t, ciphertext, actualCiphertext)
}

func sourceSyncUpgrade20261031GitBlobID(content []byte) string {
	hash := sha1.New()
	_, _ = hash.Write([]byte(fmt.Sprintf("blob %d\x00", len(content))))
	_, _ = hash.Write(content)
	return hex.EncodeToString(hash.Sum(nil))
}

func sourceSyncUpgrade20261031SHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
