//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
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
		Baseline                  string
		Migrations                map[string]map[string]string
		Additions                 []string
		CandidateCount            int                          `json:"candidate_count"`
		AdditionChecksums         map[string]map[string]string `json:"addition_checksums"`
		Sources                   map[string]string
		ProductionLedgerOverrides map[string]string `json:"production_ledger_overrides"`
	}
	raw, err := os.ReadFile("testdata/source_sync_31f924258_migrations.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.Equal(t, "31f92425856c800245a85b2d858728b9fd5cbc93", manifest.Baseline)
	require.Len(t, manifest.Migrations, 419)
	require.Equal(t, map[string]string{
		"157_user_platform_quotas_add_grok.sql": "5cace8fa32c6174a72721cd9b01f28f4545de1fd7bcd9ca196a4225056ec4fb8",
	}, manifest.ProductionLedgerOverrides, "only the observed, already supported production checksum may differ")
	require.Equal(t, "a918734da39c2e5a82e4a5e9511bac1f4cf7e310ceadd647df52692320633c1b",
		manifest.Migrations["157_user_platform_quotas_add_grok.sql"]["checksum"])
	require.Equal(t, map[string]string{
		"nianzs": "bf19252291c57dab59d031955036df51b3e320a4",
		"ranxi":  "8132f162a0ad76acd60b701ac4694fd70d2d75f1",
	}, manifest.Sources)
	require.Len(t, manifest.Additions, manifest.CandidateCount-len(manifest.Migrations))
	require.Len(t, manifest.AdditionChecksums, len(manifest.Additions))
	files, err := fs.Glob(migrations.FS, "*.sql")
	require.NoError(t, err)
	require.Len(t, files, manifest.CandidateCount, "candidate must match the reviewed manifest")
	baseline := fstest.MapFS{}
	preTrustedRevocation := fstest.MapFS{}
	baselineChecksums := map[string]string{}
	candidateChecksums := map[string]string{}
	var additions []string
	for _, name := range files {
		content, readErr := migrations.FS.ReadFile(name)
		require.NoError(t, readErr, name)
		digest := sha256.Sum256([]byte(strings.TrimSpace(string(content))))
		candidateChecksums[name] = hex.EncodeToString(digest[:])
		if name != "339_quality_oauth_401_trusted_revocation.sql" {
			preTrustedRevocation[name] = &fstest.MapFile{Data: content, Mode: 0444}
		}
		published, ok := manifest.Migrations[name]
		if !ok {
			additions = append(additions, name)
			expected, reviewed := manifest.AdditionChecksums[name]
			require.True(t, reviewed, "unreviewed candidate migration: "+name)
			rawDigest := sha256.Sum256(content)
			require.Equal(t, expected["raw_sha256"], hex.EncodeToString(rawDigest[:]), "reviewed new SQL changed: "+name)
			require.Equal(t, expected["checksum"], candidateChecksums[name], "reviewed new ledger changed: "+name)
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
	// The live database retains the published upstream 157 checksum. Its exact
	// compatibility pair is already allowed by the runner; canonical SQL bytes
	// above stay locked while this fixture reproduces the observed ledger.
	for name, productionChecksum := range manifest.ProductionLedgerOverrides {
		canonicalChecksum, exists := baselineChecksums[name]
		require.True(t, exists, "production override must name a historical migration")
		result, updateErr := db.ExecContext(ctx, `UPDATE schema_migrations SET checksum=$1
			WHERE filename=$2 AND checksum=$3`, productionChecksum, name, canonicalChecksum)
		require.NoError(t, updateErr)
		updated, rowsErr := result.RowsAffected()
		require.NoError(t, rowsErr)
		require.EqualValues(t, 1, updated)
		baselineChecksums[name] = productionChecksum
		candidateChecksums[name] = productionChecksum
	}
	require.Equal(t, baselineChecksums, readRanxiUpgradeChecksums(t, db), "starting ledger must match observed production")
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
	expectedUsage, ok := expectedFinancialState["usage_log"].(map[string]any)
	require.True(t, ok)
	expectedUsage["cache_creation_30m_tokens"] = 0
	expectedUsage["cache_creation_12h_tokens"] = 0
	expectedFinancialJSON, err := json.Marshal(expectedFinancialState)
	require.NoError(t, err)
	assertSourceSyncUpgrade20261031LegacyOAuthSlots(t, ctx, db, accountID)
	_, err = db.ExecContext(ctx, "INSERT INTO users(email,password_hash,role,balance,frozen_balance) VALUES ('source-sync-20261110-admin@example.test','test','admin',17,2), ('source-sync-20261110-observer@example.test','test','observer',19,3), ('source-sync-20261110-user@example.test','test','user',23,5)")
	require.NoError(t, err)
	const rolesQuery = "SELECT jsonb_agg(jsonb_build_object('id',id,'role',role,'status',status,'balance',balance,'frozen_balance',frozen_balance) ORDER BY id)::text FROM users WHERE email LIKE 'source-sync-20261110-%@example.test'"
	var rolesBefore string
	require.NoError(t, db.QueryRowContext(ctx, rolesQuery).Scan(&rolesBefore))
	validDegradedID, invalidDegradedID := seedSourceSyncUpgrade20261110Degradation(t, ctx, db)
	pricingBefore := seedSourceSyncUpgrade20261110CachePricing(t, ctx, db)
	// Exercise the trusted-revocation correction against a real row created by
	// its immediately preceding migration, rather than only an empty new table.
	require.NoError(t, applyMigrationsFS(ctx, db, preTrustedRevocation))
	var planID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO scheduled_test_plans
		(account_id,model_id,cron_expression) VALUES($1,'gpt-6-luna','*/30 * * * *') RETURNING id`,
		validDegradedID).Scan(&planID))
	configUpdatedAt := time.Date(2026, time.November, 10, 12, 0, 0, 0, time.UTC)
	claimedUntil := configUpdatedAt.Add(time.Hour)
	_, err = db.ExecContext(ctx, `INSERT INTO quality_oauth_401_streaks
		(plan_id,failures,config_updated_at,config_revision,credentials_revision,claimed_until,last_round_until)
		VALUES($1,4,$2,'fixture-config','fixture-credentials',$3,$2)`, planID, configUpdatedAt, claimedUntil)
	require.NoError(t, err)
	expectedFailures := 0
	expectedLastRound := sql.NullTime{}
	expectedOwnRevoked := false
	for pass := 1; pass <= 2; pass++ {
		require.NoError(t, ApplyMigrations(ctx, db))
		require.Equal(t, candidateChecksums, readRanxiUpgradeChecksums(t, db), "full-filename ledger pass %d", pass)
		require.JSONEq(t, string(expectedFinancialJSON), sourceSyncUpgrade20261031FinancialState(t, ctx, db, userID, keyID, quotaID), "financial state and reviewed API-key defaults pass %d", pass)
		assertSourceSyncUpgrade20261031LegacyOAuthSlots(t, ctx, db, accountID)
		var rolesAfter string
		require.NoError(t, db.QueryRowContext(ctx, rolesQuery).Scan(&rolesAfter))
		require.JSONEq(t, rolesBefore, rolesAfter, "protected roles and funds pass %d", pass)
		assertSourceSyncUpgrade20261110Degradation(t, ctx, db, validDegradedID, invalidDegradedID)
		require.JSONEq(t, pricingBefore, sourceSyncUpgrade20261110CachePricingState(t, ctx, db),
			"existing generic cache prices preserved and new 12h prices remain NULL, pass %d", pass)
		var failures int
		var lastRound sql.NullTime
		var ownRevoked bool
		var actualConfigUpdatedAt, actualClaimedUntil time.Time
		var configRevision, credentialsRevision string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT failures,last_round_until,own_revoked_401,
			config_revision,credentials_revision,config_updated_at,claimed_until
			FROM quality_oauth_401_streaks WHERE plan_id=$1`, planID).
			Scan(&failures, &lastRound, &ownRevoked, &configRevision, &credentialsRevision,
				&actualConfigUpdatedAt, &actualClaimedUntil))
		require.Equal(t, expectedFailures, failures, "historical streak resets once; new trusted streak survives replay, pass %d", pass)
		require.Equal(t, expectedLastRound.Valid, lastRound.Valid, "streak round validity pass %d", pass)
		if expectedLastRound.Valid {
			require.True(t, expectedLastRound.Time.Equal(lastRound.Time), "new trusted round survives replay")
		}
		require.Equal(t, expectedOwnRevoked, ownRevoked, "trusted revocation state pass %d", pass)
		require.Equal(t, "fixture-config", configRevision)
		require.Equal(t, "fixture-credentials", credentialsRevision)
		require.True(t, configUpdatedAt.Equal(actualConfigUpdatedAt), "config timestamp preserved, pass %d", pass)
		require.True(t, claimedUntil.Equal(actualClaimedUntil), "claim timestamp preserved, pass %d", pass)
		for _, table := range []string{"auto_credit_config", "auto_credit_ledger", "auto_credit_audit", "auto_credit_cache_outbox"} {
			var count int
			var exists bool
			require.NoError(t, db.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists))
			require.True(t, exists, "required auto-credit table %s", table)
			require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count))
			require.Zero(t, count, table)
		}
		var enabledAutoCreditConfigs int
		require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM auto_credit_config WHERE enabled").Scan(&enabledAutoCreditConfigs))
		require.Zero(t, enabledAutoCreditConfigs, "upgrade must not enable auto-credit, pass %d", pass)
		var autoCreditEnabledDefault string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT column_default FROM information_schema.columns
			WHERE table_schema=current_schema() AND table_name='auto_credit_config' AND column_name='enabled'`).
			Scan(&autoCreditEnabledDefault))
		require.Equal(t, "false", autoCreditEnabledDefault, "auto-credit schema must default to disabled, pass %d", pass)
		t.Logf("baseline=%s published=%d candidate=%d upgrade_pass=%d financial_state=preserved oauth_controls=verified cache_ttl=verified",
			manifest.Baseline, len(manifest.Migrations), manifest.CandidateCount, pass)
		if pass == 1 {
			// A later valid streak must survive startup replay; otherwise rerunning
			// migration 339 could silently erase fresh trusted revocation evidence.
			expectedFailures = 2
			expectedLastRound = sql.NullTime{Time: configUpdatedAt.Add(30 * time.Minute), Valid: true}
			expectedOwnRevoked = true
			configUpdatedAt = configUpdatedAt.Add(time.Minute)
			claimedUntil = claimedUntil.Add(time.Minute)
			result, updateErr := db.ExecContext(ctx, `UPDATE quality_oauth_401_streaks
				SET failures=$2,last_round_until=$3,own_revoked_401=$4,config_updated_at=$5,claimed_until=$6
				WHERE plan_id=$1`, planID, expectedFailures, expectedLastRound.Time, expectedOwnRevoked,
				configUpdatedAt, claimedUntil)
			require.NoError(t, updateErr)
			updated, rowsErr := result.RowsAffected()
			require.NoError(t, rowsErr)
			require.EqualValues(t, 1, updated)
		}
	}
}

func seedSourceSyncUpgrade20261110Degradation(t *testing.T, ctx context.Context, db *sql.DB) (int64, int64) {
	t.Helper()
	var validID, invalidID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO accounts
		(name,platform,type,credentials,extra,concurrency,schedulable)
		VALUES('source-sync-valid-degradation','openai','oauth',
		'{"access_token":"fixture-token","compact_model_mapping":{"gpt-6-luna":"gpt-6-astra"}}',
		'{"fixture_marker":"preserve","openai_passthrough":true,"openai_oauth_passthrough":true,
		  "oauth_degradation_action":{"models":["gpt-6-luna"],"concurrency_cap":20}}',7,true)
		RETURNING id`).Scan(&validID))
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO accounts
		(name,platform,type,credentials,extra,concurrency,schedulable)
		VALUES('source-sync-invalid-degradation','openai','oauth','{"access_token":"fixture-invalid-token"}',
		'{"oauth_degradation_action":{"models":[],"limit_concurrency":false}}',13,true)
		RETURNING id`).Scan(&invalidID))
	return validID, invalidID
}

func assertSourceSyncUpgrade20261110Degradation(t *testing.T, ctx context.Context, db *sql.DB, validID, invalidID int64) {
	t.Helper()
	for _, tc := range []struct {
		id          int64
		concurrency int
		schedulable bool
		credentials string
		extra       string
	}{
		{validID, 7, true,
			`{"access_token":"fixture-token","model_mapping":{"gpt-6-luna":"gpt-6-luna"},"model_mapping_mode":"whitelist"}`,
			`{"fixture_marker":"preserve","openai_long_context_billing_enabled":false,"openai_passthrough":false,"openai_oauth_passthrough":false,
			"oauth_degradation_action":{"models":["gpt-6-luna"],"concurrency_cap":20,"native_controls":true,"native_scheduling_paused":false},
			"auto_config_concurrency":{"concurrency":7,"degradation_hold":true}}`},
		{invalidID, 13, false, `{"access_token":"fixture-invalid-token"}`,
			`{"openai_long_context_billing_enabled":false,"oauth_degradation_action":{"models":[],"limit_concurrency":false,"native_controls":true,"native_scheduling_paused":true}}`},
	} {
		var credentials, extra string
		var concurrency, events int
		var schedulable bool
		require.NoError(t, db.QueryRowContext(ctx, `SELECT credentials::text,extra::text,concurrency,schedulable
			FROM accounts WHERE id=$1`, tc.id).Scan(&credentials, &extra, &concurrency, &schedulable))
		require.JSONEq(t, tc.credentials, credentials)
		require.JSONEq(t, tc.extra, extra)
		require.Equal(t, tc.concurrency, concurrency)
		require.Equal(t, tc.schedulable, schedulable)
		require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM scheduler_outbox
			WHERE account_id=$1 AND event_type='account_changed'`, tc.id).Scan(&events))
		require.Equal(t, 1, events, "one-time migration emits exactly one event even after replay")
	}
}

func seedSourceSyncUpgrade20261110CachePricing(t *testing.T, ctx context.Context, db *sql.DB) string {
	t.Helper()
	_, err := db.ExecContext(ctx, `WITH channel AS (
		INSERT INTO channels(name) VALUES('source-sync-cache-ttl') RETURNING id
	), pricing AS (
		INSERT INTO channel_model_pricing(channel_id,models,cache_write_price)
		SELECT id,'["source-sync-cache-ttl"]'::jsonb,0.0000123 FROM channel RETURNING id
	), rule AS (
		INSERT INTO channel_account_stats_pricing_rules(channel_id,name)
		SELECT id,'source-sync-cache-ttl' FROM channel RETURNING id
	), stats AS (
		INSERT INTO channel_account_stats_model_pricing(rule_id,models,cache_write_price)
		SELECT id,'["source-sync-cache-ttl"]'::jsonb,0.0000234 FROM rule RETURNING id
	), intervals AS (
		INSERT INTO channel_pricing_intervals(pricing_id,cache_write_price)
		SELECT id,0.0000345 FROM pricing
	)
	INSERT INTO channel_account_stats_pricing_intervals(pricing_id,cache_write_price)
	SELECT id,0.0000456 FROM stats`)
	require.NoError(t, err)
	return sourceSyncUpgrade20261110CachePricingState(t, ctx, db)
}

func sourceSyncUpgrade20261110CachePricingState(t *testing.T, ctx context.Context, db *sql.DB) string {
	t.Helper()
	// Merge the reviewed nullable default into the pre-upgrade snapshot as well;
	// every pre-existing column, including each generic cache price, stays exact.
	const query = `SELECT jsonb_build_object(
		'channel_model_pricing','{"cache_write_12h_price":null}'::jsonb||to_jsonb(p),
		'channel_pricing_intervals','{"cache_write_12h_price":null}'::jsonb||to_jsonb(i),
		'channel_account_stats_model_pricing','{"cache_write_12h_price":null}'::jsonb||to_jsonb(s),
		'channel_account_stats_pricing_intervals','{"cache_write_12h_price":null}'::jsonb||to_jsonb(si)
	)::text FROM channels c JOIN channel_model_pricing p ON p.channel_id=c.id
	JOIN channel_pricing_intervals i ON i.pricing_id=p.id
	JOIN channel_account_stats_pricing_rules r ON r.channel_id=c.id
	JOIN channel_account_stats_model_pricing s ON s.rule_id=r.id
	JOIN channel_account_stats_pricing_intervals si ON si.pricing_id=s.id
	WHERE c.name='source-sync-cache-ttl'`
	var state string
	require.NoError(t, db.QueryRowContext(ctx, query).Scan(&state))
	return state
}
