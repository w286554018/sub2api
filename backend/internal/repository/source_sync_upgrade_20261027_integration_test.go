//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestSourceSync20261027UpgradePreservesProductionSchemaAndMoney(t *testing.T) {
	ctx := context.Background()
	files, err := fs.Glob(migrations.FS, "*.sql")
	require.NoError(t, err)
	baseline := fstest.MapFS{}
	checksums := map[string]string{}
	var frozen strings.Builder
	for _, name := range files {
		// Production 6df9a04 ends at 284. These two lower-numbered migrations
		// are additions from the current upstream merge, not production history.
		if name >= "285_" || name == "247_scoped_admin_api_keys.sql" || name == "248_group_image_count_billing.sql" {
			continue
		}
		content, err := migrations.FS.ReadFile(name)
		require.NoError(t, err)
		checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(string(content)))))
		checksums[name] = checksum
		fmt.Fprintf(&frozen, "%s:%s\n", name, checksum)
		baseline[name] = &fstest.MapFile{Data: content}
	}
	// Independently frozen from git archive 6df9a04ad53acc78e7eb32a369c15edae0bd7c77.
	// An edited, missing or accidentally included old SQL file must fail before
	// constructing the baseline database, rather than seeding its own evidence.
	require.Len(t, baseline, 367)
	require.Equal(t, "b277e786b8459d6a4b8f8aa9ab8b1887a8ccba8b008b3355201f4383446f0925",
		fmt.Sprintf("%x", sha256.Sum256([]byte(frozen.String()))))
	db := sourceSyncUpgradeDB(t)
	require.NoError(t, applyMigrationsFS(ctx, db, baseline))
	require.Equal(t, checksums, readRanxiUpgradeChecksums(t, db))

	var userID, keyID, quotaID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO users(email,password_hash,balance,frozen_balance,role)
		VALUES('source-sync-20261027@example.test','test',12.5,3.125,'super_admin') RETURNING id`).Scan(&userID))
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO api_keys(user_id,key,name,concurrency_limit,quota,quota_used)
		VALUES($1,'source-sync-20261027-key','production-existing',7,100,2.75) RETURNING id`, userID).Scan(&keyID))
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO user_platform_quotas
		(user_id,platform,daily_limit_usd,weekly_limit_usd,monthly_limit_usd,
		daily_usage_usd,weekly_usage_usd,monthly_usage_usd,daily_window_start,weekly_window_start,monthly_window_start)
		VALUES($1,'openai',10,20,30,1.25,2.5,3.75,
		'2026-10-15T00:00:00Z','2026-10-12T00:00:00Z','2026-10-01T00:00:00Z') RETURNING id`, userID).Scan(&quotaID))
	_, err = db.ExecContext(ctx, `INSERT INTO accounts(name,platform,type,credentials)
		VALUES('source-sync-20261027-kiro','kiro','apikey','{"fixture":"preserve-kiro"}'),
		('source-sync-20261027-adobe','adobe','oauth','{"fixture":"preserve-adobe"}')`)
	require.NoError(t, err)
	const accountQuery = `SELECT jsonb_agg(jsonb_build_object('id',id,'name',name,'platform',platform,
		'type',type,'credentials',credentials) ORDER BY id)::text FROM accounts
		WHERE name IN ('source-sync-20261027-kiro','source-sync-20261027-adobe')`
	var beforeAccounts string
	require.NoError(t, db.QueryRowContext(ctx, accountQuery).Scan(&beforeAccounts))
	const stateQuery = `SELECT jsonb_build_object(
		'balance',u.balance,'frozen_balance',u.frozen_balance,'role',u.role,
		'quota',k.quota,'quota_used',k.quota_used,'concurrency_limit',k.concurrency_limit,
		'platform_quota',to_jsonb(q))::text
		FROM users u JOIN api_keys k ON k.user_id=u.id JOIN user_platform_quotas q ON q.user_id=u.id
		WHERE u.id=$1 AND k.id=$2 AND q.id=$3`
	var before string
	require.NoError(t, db.QueryRowContext(ctx, stateQuery, userID, keyID, quotaID).Scan(&before))
	for repeat := 0; repeat < 2; repeat++ {
		require.NoError(t, ApplyMigrations(ctx, db))
		ledger := readRanxiUpgradeChecksums(t, db)
		require.Len(t, ledger, len(files))
		for name, checksum := range checksums {
			require.Equal(t, checksum, ledger[name], name)
		}
		var after string
		require.NoError(t, db.QueryRowContext(ctx, stateQuery, userID, keyID, quotaID).Scan(&after))
		require.JSONEq(t, before, after, "upgrade must preserve spendable/frozen money, roles, key and platform quota state")
		var accounts int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM accounts
			WHERE (name='source-sync-20261027-kiro' AND platform='kiro' AND type='apikey')
			OR (name='source-sync-20261027-adobe' AND platform='adobe' AND type='oauth')`).Scan(&accounts))
		require.Equal(t, 2, accounts)
		var afterAccounts string
		require.NoError(t, db.QueryRowContext(ctx, accountQuery).Scan(&afterAccounts))
		require.JSONEq(t, beforeAccounts, afterAccounts, "existing account identities and platform payloads must survive")
		var unchangedEffectiveQuota bool
		require.NoError(t, db.QueryRowContext(ctx, `SELECT
			ROW(q.daily_usage_usd,q.weekly_usage_usd,q.monthly_usage_usd)=
			ROW(e.daily_usage_usd,e.weekly_usage_usd,e.monthly_usage_usd)
			FROM user_platform_quotas q JOIN usage_platform_quota_effective e ON e.id=q.id WHERE q.id=$1`, quotaID).Scan(&unchangedEffectiveQuota))
		require.True(t, unchangedEffectiveQuota, "the new projection must preserve the existing baseline without inventing deltas")
		for _, table := range []string{"usage_billing_quotes", "usage_billing_events", "usage_platform_quota_totals", "batch_image_money_holds"} {
			var count int
			require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count))
			require.Zero(t, count, "upgrade must not fabricate historical financial records: "+table)
		}
		t.Logf("baseline=6df9a04ad53acc78e7eb32a369c15edae0bd7c77 published=367 candidate=%d upgrade_pass=%d financial_state=preserved", len(files), repeat+1)
	}
}
