package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"jc_proxy/internal/config"
	"jc_proxy/internal/keystore"
)

func TestUpgradeV2FilesPreservesIDsAndCounters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	original := []byte(`{"schema_version":2,"vendors":{"vid_stable":[{"key":"k","status":"active","total_requests":12,"success_count":10}]}}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	mapping := map[string]string{"renamed": "vid_stable"}
	if err := upgradeKeysFile(path, mapping, true); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := os.ReadFile(path)
	if string(unchanged) != string(original) {
		t.Fatal("dry run modified key file")
	}
	if err := upgradeKeysFile(path, mapping, false); err != nil {
		t.Fatal(err)
	}
	if err := upgradeKeysFile(path, mapping, false); err != nil {
		t.Fatal(err)
	}
	payload, _ := os.ReadFile(path)
	var snapshot fileKeySnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.SchemaVersion != 3 || len(snapshot.Vendors) != 1 || len(snapshot.Vendors["vid_stable"]) != 1 || snapshot.Vendors["vid_stable"][0].TotalRequests != 12 {
		t.Fatalf("migrated = %+v", snapshot)
	}
	cfg, err := migrateConfigPayload([]byte("schema_version: 2\nvendors:\n  - id: vid_stable\n    name: renamed\n    upstream:\n      base_url: https://example.com\n"), mapping)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadBootstrapBytesNoEnv(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SchemaVersion != 3 || loaded.Vendors[0].ID != "vid_stable" {
		t.Fatalf("config = %+v", loaded)
	}
}

// Opt-in only; uses dedicated, uniquely named tables and never the application's
// configured table. Run with JC_PROXY_TEST_PG_DSN pointing to a TEST database.
func TestPostgresRecentStatsMigrationAndPersistence(t *testing.T) {
	dsn := os.Getenv("JC_PROXY_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("JC_PROXY_TEST_PG_DSN not set; PostgreSQL integration not run")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			table := fmt.Sprintf("jc_test_timing_%d_%d", time.Now().UnixNano(), version)
			cfg := config.UpstreamKeyStorePGSQLConfig{DSN: dsn, Table: table, MaxOpenConns: 2, MaxIdleConns: 1}
			store, err := keystore.NewPGStore(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			defer db.Exec("DROP TABLE IF EXISTS " + quoteIdent(table) + ", " + quoteIdent(table+"_meta"))
			if _, err := store.Append("vid_stable", []string{"k"}); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("ALTER TABLE " + quoteIdent(table) + " DROP COLUMN recent_stats"); err != nil {
				t.Fatal(err)
			}
			if version == 1 {
				if _, err := db.Exec("ALTER TABLE " + quoteIdent(table) + " RENAME COLUMN vendor_id TO vendor"); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec("UPDATE " + quoteIdent(table) + " SET vendor = 'oldname'"); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.SetStoredSchemaVersion(version); err != nil {
				t.Fatal(err)
			}
			if unexpected, err := keystore.NewPGStore(cfg); err == nil {
				unexpected.Close()
				t.Fatal("server accepted old schema")
			}
			mapping := map[string]string{"oldname": "vid_stable"}
			if err := upgradeKeysPG(cfg, mapping, true); err != nil {
				t.Fatal(err)
			}
			if has, err := columnExists(db, table, "recent_stats"); err != nil || has {
				t.Fatalf("dry-run column = %v, %v", has, err)
			}
			if err := upgradeKeysPG(cfg, mapping, false); err != nil {
				t.Fatal(err)
			}
			if version == 1 {
				// The v1 upgrader intentionally retains a rollback table.
				rows, err := db.Query("SELECT tablename FROM pg_tables WHERE schemaname=current_schema() AND tablename LIKE $1", table+"_v1_backup_%")
				if err != nil {
					t.Fatal(err)
				}
				var backups []string
				for rows.Next() {
					var name string
					if err := rows.Scan(&name); err != nil {
						t.Fatal(err)
					}
					backups = append(backups, name)
				}
				rows.Close()
				for _, name := range backups {
					defer db.Exec("DROP TABLE " + quoteIdent(name))
				}
			}
			if err := upgradeKeysPG(cfg, mapping, false); err != nil {
				t.Fatal(err)
			}
			stats := keystore.RuntimeStats{TotalRequests: 1, SuccessCount: 1, LastStatus: 200, RecentStats: keystore.RecentStats{RecentRequests: 5, RecentSuccessCount: 4, HeaderSamples: 5, AvgHeaderMS: 30, ResponseSamples: 4, AvgResponseMS: 80}}
			if err := store.ApplyRuntimeStatsDeltas(map[string][]keystore.RuntimeStatsDelta{"vid_stable": {{Key: "k", RuntimeStats: stats}}}); err != nil {
				t.Fatal(err)
			}
			stats.AvgHeaderMS = 12
			if err := store.ApplyRuntimeStatsDeltas(map[string][]keystore.RuntimeStatsDelta{"vid_stable": {{Key: "k", RuntimeStats: keystore.RuntimeStats{RecentStats: stats.RecentStats}}}}); err != nil {
				t.Fatal(err)
			}
			if err := store.Replace("vid_stable", []string{"k"}); err != nil {
				t.Fatal(err)
			}
			all, err := store.ListAll()
			if err != nil {
				t.Fatal(err)
			}
			one, err := store.List("vid_stable")
			if err != nil {
				t.Fatal(err)
			}
			if all["vid_stable"][0].RuntimeStats != stats || one[0].RuntimeStats != stats {
				t.Fatalf("stored = %+v", one)
			}
			if err := store.SetStoredSchemaVersion(99); err != nil {
				t.Fatal(err)
			}
			var versionErr *config.SchemaVersionError
			if err := upgradeKeysPG(cfg, mapping, false); !errors.As(err, &versionErr) {
				t.Fatalf("future schema accepted: %v", err)
			}
		})
	}
}
