//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestSourceSyncUpgradePreservesD07PublishedSchemaAndData(t *testing.T) {
	ctx := context.Background()
	raw, err := os.ReadFile("testdata/source_sync_d07adc16_migrations.json")
	require.NoError(t, err)
	var manifest struct {
		Baseline   string            `json:"baseline"`
		Migrations map[string]string `json:"migrations"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.Equal(t, "d07adc16b8481be1ee9a4da2acf803f7a09a115e", manifest.Baseline)
	require.Len(t, manifest.Migrations, 356)
	baseline := fstest.MapFS{}
	for filename, expected := range manifest.Migrations {
		content, err := migrations.FS.ReadFile(filename)
		require.NoError(t, err, filename)
		// Freeze the historical checksum independently of the current embedded
		// files; otherwise an edited old migration would seed its own evidence.
		sum := sha256.Sum256([]byte(strings.TrimSpace(string(content))))
		require.Equal(t, expected, hex.EncodeToString(sum[:]), filename)
		baseline[filename] = &fstest.MapFile{Data: content}
	}
	db := sourceSyncUpgradeDB(t)
	require.NoError(t, applyMigrationsFS(ctx, db, baseline))
	require.Equal(t, manifest.Migrations, readRanxiUpgradeChecksums(t, db))

	var userID, keyID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO users(email,password_hash,balance,role)
		VALUES('source-sync@example.test','test',12.50,'super_admin') RETURNING id`).Scan(&userID))
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO api_keys(user_id,key,name,concurrency_limit)
		VALUES($1,'source-sync-existing-key','existing',7) RETURNING id`, userID).Scan(&keyID))
	_, err = db.ExecContext(ctx, `INSERT INTO accounts(name,platform,type,credentials)
		VALUES('source-sync-kiro','kiro','apikey','{}'),('source-sync-adobe','adobe','oauth','{}')`)
	require.NoError(t, err)

	files, err := fs.Glob(migrations.FS, "*.sql")
	require.NoError(t, err)
	for repeat := 0; repeat < 2; repeat++ {
		require.NoError(t, ApplyMigrations(ctx, db))
		checksums := readRanxiUpgradeChecksums(t, db)
		require.Len(t, checksums, len(files))
		for filename, expected := range manifest.Migrations {
			require.Equal(t, expected, checksums[filename], filename)
		}
		var balance, role string
		require.NoError(t, db.QueryRowContext(ctx, "SELECT balance::text,role FROM users WHERE id=$1", userID).Scan(&balance, &role))
		require.Equal(t, "12.50000000", balance)
		require.Equal(t, "super_admin", role)
		var platforms int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts
			WHERE (name='source-sync-kiro' AND platform='kiro' AND type='apikey')
			OR (name='source-sync-adobe' AND platform='adobe' AND type='oauth')`).Scan(&platforms))
		require.Equal(t, 2, platforms)
		var limit int
		require.NoError(t, db.QueryRowContext(ctx, "SELECT concurrency_limit FROM api_keys WHERE id=$1", keyID).Scan(&limit))
		require.Equal(t, 7, limit, "upgrade must preserve an existing nonzero concurrency limit")
	}
}

func TestSourceSyncMigration284RepairsOnlyProvenBillingMetadata(t *testing.T) {
	ctx := context.Background()
	const additive = "284_regional_allocation_billing_metadata.sql"
	files, err := fs.Glob(migrations.FS, "*.sql")
	require.NoError(t, err)
	before284 := fstest.MapFS{}
	for _, filename := range files {
		if filename >= additive {
			continue
		}
		content, err := migrations.FS.ReadFile(filename)
		require.NoError(t, err)
		before284[filename] = &fstest.MapFile{Data: content}
	}
	_, err = migrations.FS.ReadFile(additive)
	require.NoError(t, err, "the additive metadata migration must be present")
	db := sourceSyncUpgradeDB(t)
	require.NoError(t, applyMigrationsFS(ctx, db, before284))
	checksums := readRanxiUpgradeChecksums(t, db)

	var userID, groupID, currentGroupID, subscriptionID, keyID, otherKeyID, accountID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO users(email,password_hash,balance,role)
		VALUES('source-sync-284@example.test','test',12.50,'super_admin') RETURNING id`).Scan(&userID))
	require.NoError(t, db.QueryRowContext(ctx, "INSERT INTO groups(name) VALUES('historical-usage-group') RETURNING id").Scan(&groupID))
	require.NoError(t, db.QueryRowContext(ctx, "INSERT INTO groups(name) VALUES('current-key-subscription-group') RETURNING id").Scan(&currentGroupID))
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO user_subscriptions(user_id,group_id,starts_at,expires_at,daily_usage_usd)
		VALUES($1,$2,NOW(),NOW()+INTERVAL '1 day',2.5) RETURNING id`, userID, currentGroupID).Scan(&subscriptionID))
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO api_keys(user_id,key,name,group_id)
		VALUES($1,'source-sync-284-key','current group differs',$2) RETURNING id`, userID, currentGroupID).Scan(&keyID))
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO api_keys(user_id,key,name)
		VALUES($1,'source-sync-284-other-key','unrelated') RETURNING id`, userID).Scan(&otherKeyID))
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,credentials)
		VALUES('source-sync-284-account','openai','apikey','{}') RETURNING id`).Scan(&accountID))

	cases := []struct {
		requestID string
		grant     bool
		funding   int64
	}{
		{"request-balance", false, 0},
		{"request-subscription", false, subscriptionID},
		{"event-balance", true, 0},
		{"event-subscription", true, subscriptionID},
	}
	for _, tc := range cases {
		allocation := "request:" + tc.requestID
		if tc.grant {
			leaseID := "lease-" + tc.requestID
			allocation = "grant:" + leaseID
			_, err = db.ExecContext(ctx, `INSERT INTO regional_quota_leases(lease_id,region_id,user_id,granted,spent,issued_at,expires_at)
				VALUES($1,'test',$2,1000000000,200000000,NOW(),NOW()+INTERVAL '1 hour')`, leaseID, userID)
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, `INSERT INTO regional_settled_events(event_id,lease_id,event)
				VALUES($1,$2,'{"proof":"keep"}')`, tc.requestID, leaseID)
			require.NoError(t, err)
		}
		_, err = db.ExecContext(ctx, `INSERT INTO regional_budget_allocations
			(allocation_id,user_id,api_key_id,account_id,subscription_id,platform,maximum,spent,state)
			VALUES($1,$2,$3,$4,$5,'openai',1000000000,200000000,'held')`, allocation, userID, keyID, accountID, tc.funding)
		require.NoError(t, err)
		// Deliberately incorrect funding metadata, but a known historical group.
		// Neither the current Key nor subscription group may replace that group.
		oldSubscription := sql.NullInt64{Int64: subscriptionID, Valid: tc.funding == 0}
		oldBillingType := 0
		if tc.funding == 0 {
			oldBillingType = 1
		}
		_, err = db.ExecContext(ctx, `INSERT INTO usage_logs
			(user_id,api_key_id,account_id,request_id,model,subscription_id,billing_type,billing_mode,group_id,total_cost,actual_cost)
			VALUES($1,$2,$3,$4,'test-model',$5,$6,'per_request',$7,0.2,0.2)`, userID, keyID, accountID, tc.requestID, oldSubscription, oldBillingType, groupID)
		require.NoError(t, err)
	}
	// Same request ID, different Key: the allocation is not attribution proof.
	_, err = db.ExecContext(ctx, `INSERT INTO usage_logs
		(user_id,api_key_id,account_id,request_id,model,billing_type,billing_mode,group_id,total_cost,actual_cost)
		VALUES($1,$2,$3,'request-subscription','test-model',0,'per_request',$4,0.3,0.3)`, userID, otherKeyID, accountID, groupID)
	require.NoError(t, err)

	for repeat := 0; repeat < 2; repeat++ {
		require.NoError(t, ApplyMigrations(ctx, db))
		currentChecksums := readRanxiUpgradeChecksums(t, db)
		for filename, expected := range checksums {
			require.Equal(t, expected, currentChecksums[filename], filename)
		}
		for _, tc := range cases {
			var subscription sql.NullInt64
			var billingType int
			var billingMode, charge string
			var historicalGroup int64
			require.NoError(t, db.QueryRowContext(ctx, `SELECT subscription_id,billing_type,billing_mode,group_id,actual_cost::text
				FROM usage_logs WHERE request_id=$1 AND api_key_id=$2`, tc.requestID, keyID).Scan(&subscription, &billingType, &billingMode, &historicalGroup, &charge))
			require.Equal(t, sql.NullInt64{Int64: tc.funding, Valid: tc.funding != 0}, subscription, tc.requestID)
			expectedType := 0
			if tc.funding != 0 {
				expectedType = 1
			}
			require.Equal(t, expectedType, billingType, tc.requestID)
			require.Equal(t, "token", billingMode)
			require.Equal(t, groupID, historicalGroup, "historical group cannot be guessed from current membership")
			require.Equal(t, "0.2000000000", charge)
		}
		var retained int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM regional_budget_allocations
			WHERE group_id IS NULL AND state='held' AND maximum=1000000000 AND spent=200000000`).Scan(&retained))
		require.Equal(t, 4, retained, "unknown group and unresolved money must survive")
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM regional_settled_events
			WHERE event='{"proof":"keep"}'::jsonb`).Scan(&retained))
		require.Equal(t, 2, retained, "permanent settlement evidence must survive")
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_logs WHERE api_key_id=$1
			AND request_id='request-subscription' AND subscription_id IS NULL AND billing_type=0
			AND billing_mode='per_request' AND actual_cost=0.3`, otherKeyID).Scan(&retained))
		require.Equal(t, 1, retained, "a mismatched Key must remain untouched")
		var balance, subscriptionUsage string
		require.NoError(t, db.QueryRowContext(ctx, "SELECT balance::text FROM users WHERE id=$1", userID).Scan(&balance))
		require.Equal(t, "12.50000000", balance)
		require.NoError(t, db.QueryRowContext(ctx, "SELECT daily_usage_usd::text FROM user_subscriptions WHERE id=$1", subscriptionID).Scan(&subscriptionUsage))
		require.Equal(t, "2.5000000000", subscriptionUsage)
	}
}

func sourceSyncUpgradeDB(t *testing.T) *sql.DB {
	t.Helper()
	databaseName := fmt.Sprintf("source_sync_upgrade_%d", time.Now().UnixNano())
	_, err := integrationDB.ExecContext(context.Background(), "CREATE DATABASE "+databaseName)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), "DROP DATABASE "+databaseName+" WITH (FORCE)")
		require.NoError(t, err)
	})
	connection, err := url.Parse(integrationDatabaseDSN)
	require.NoError(t, err)
	connection.Path = "/" + databaseName
	db, err := sql.Open("postgres", connection.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}
