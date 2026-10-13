//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

// Exercise a deployed fork upgrade, rather than treating a fresh schema as
// evidence that already-applied checksums and existing platform data survive.
func TestRanxiUpgradePreservesPublishedForkSchemaAndData(t *testing.T) {
	ctx := context.Background()
	raw, err := os.ReadFile("testdata/ranxi_fork_baseline_1f084605_migrations.json")
	require.NoError(t, err)
	var manifest struct {
		Baseline   string   `json:"baseline"`
		Migrations []string `json:"migrations"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.Equal(t, "1f08460552fbac66106baa97d0e3841f93057d58", manifest.Baseline)
	require.Len(t, manifest.Migrations, 310)
	baseline := fstest.MapFS{}
	for _, filename := range manifest.Migrations {
		content, err := migrations.FS.ReadFile(filename)
		require.NoError(t, err, filename)
		baseline[filename] = &fstest.MapFile{Data: content}
	}

	databaseName := fmt.Sprintf("ranxi_fork_upgrade_%d", time.Now().UnixNano())
	_, err = integrationDB.ExecContext(ctx, "CREATE DATABASE "+databaseName)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), "DROP DATABASE "+databaseName+" WITH (FORCE)")
		require.NoError(t, err)
	})
	connection, err := url.Parse(integrationDatabaseDSN)
	require.NoError(t, err)
	connection.Path = "/" + databaseName
	upgradeDB, err := sql.Open("postgres", connection.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, upgradeDB.Close()) })
	require.NoError(t, applyMigrationsFS(ctx, upgradeDB, baseline))
	publishedChecksums := readRanxiUpgradeChecksums(t, upgradeDB)

	var userID, keyID int64
	require.NoError(t, upgradeDB.QueryRowContext(ctx,
		"INSERT INTO users(email,password_hash,balance,role) VALUES ('fork-upgrade@example.test','test',12.50,'super_admin') RETURNING id").Scan(&userID))
	require.NoError(t, upgradeDB.QueryRowContext(ctx,
		"INSERT INTO api_keys(user_id,key,name) VALUES ($1,'fork-upgrade-key','existing') RETURNING id", userID).Scan(&keyID))
	_, err = upgradeDB.ExecContext(ctx,
		"INSERT INTO accounts(name,platform,type,credentials) VALUES ('fork-kiro','kiro','apikey','{}'),('fork-adobe','adobe','oauth','{}')")
	require.NoError(t, err)

	for repeat := 0; repeat < 2; repeat++ {
		require.NoError(t, ApplyMigrations(ctx, upgradeDB))
		currentChecksums := readRanxiUpgradeChecksums(t, upgradeDB)
		for filename, checksum := range publishedChecksums {
			require.Equal(t, checksum, currentChecksums[filename], filename)
		}
		var balance string
		var role string
		require.NoError(t, upgradeDB.QueryRowContext(ctx, "SELECT balance::text,role FROM users WHERE id=$1", userID).Scan(&balance, &role))
		require.Equal(t, "12.50000000", balance)
		require.Equal(t, "super_admin", role)
		var platformCount int
		require.NoError(t, upgradeDB.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM accounts WHERE name IN ('fork-kiro','fork-adobe') AND platform IN ('kiro','adobe')").Scan(&platformCount))
		require.Equal(t, 2, platformCount)
		var limit int
		require.NoError(t, upgradeDB.QueryRowContext(ctx, "SELECT concurrency_limit FROM api_keys WHERE id=$1", keyID).Scan(&limit))
		if repeat == 0 {
			require.Zero(t, limit)
			_, err = upgradeDB.ExecContext(ctx, "UPDATE api_keys SET concurrency_limit=7 WHERE id=$1", keyID)
			require.NoError(t, err)
		} else {
			require.Equal(t, 7, limit, "repeated startup must preserve configured limits")
		}
	}
}

func readRanxiUpgradeChecksums(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query("SELECT filename,checksum FROM schema_migrations")
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()
	result := make(map[string]string)
	for rows.Next() {
		var filename, checksum string
		require.NoError(t, rows.Scan(&filename, &checksum))
		result[filename] = checksum
	}
	require.NoError(t, rows.Err())
	return result
}
