package keystore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
)

type resetSQLConnector struct {
	exec func(context.Context, string, []driver.NamedValue) (driver.Result, error)
}

func (c resetSQLConnector) Connect(context.Context) (driver.Conn, error) { return resetSQLConn{c}, nil }
func (c resetSQLConnector) Driver() driver.Driver                        { return resetSQLDriver{} }

type resetSQLDriver struct{}

func (resetSQLDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type resetSQLConn struct{ resetSQLConnector }

func (resetSQLConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (resetSQLConn) Close() error              { return nil }
func (resetSQLConn) Begin() (driver.Tx, error) { return nil, errors.New("unexpected begin") }
func (c resetSQLConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.exec(ctx, query, args)
}

func TestPGStoreResetRuntimeStatsSQL(t *testing.T) {
	for _, id := range []string{"", "vendor ' with quotes"} {
		t.Run("vendor="+id, func(t *testing.T) {
			calls := 0
			db := sql.OpenDB(resetSQLConnector{exec: func(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
				calls++
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("unbounded reset while runtime locks are held")
				}
				if !strings.HasPrefix(query, `UPDATE "custom"."keys"`) {
					t.Fatal("wrong target table")
				}
				for _, column := range []string{"total_requests", "success_count", "last_status", "unauthorized_count", "forbidden_count", "rate_limit_count", "other_error_count"} {
					if !strings.Contains(query, column+" = 0") {
						t.Fatalf("missing reset of %s", column)
					}
				}
				if !strings.Contains(query, "last_error = ''") || !strings.Contains(query, "recent_stats = '{}'::jsonb") {
					t.Fatal("diagnostics or recent window not reset")
				}
				for _, column := range []string{"api_key", "status", "version", "remark", "disable_reason", "disabled_at", "disabled_by", "created_at"} {
					if strings.Contains(query, " "+column+" =") {
						t.Fatalf("reset changes metadata %s", column)
					}
				}
				if id == "" {
					if len(args) != 0 || strings.Contains(query, "WHERE") {
						t.Fatal("all scope still filters")
					}
				} else if !strings.HasSuffix(query, "WHERE vendor_id = $1") || len(args) != 1 || args[0].Value != id {
					t.Fatal("vendor scope is not parameterized")
				}
				return driver.RowsAffected(3), nil
			}})
			defer db.Close()
			store := &PGStore{db: db, tableSQL: `"custom"."keys"`}
			if count, err := store.ResetRuntimeStats(id); err != nil || count != 3 || calls != 1 {
				t.Fatalf("reset = %d, %v, calls=%d", count, err, calls)
			}
		})
	}
}

func TestPGStoreResetRuntimeStatsFailure(t *testing.T) {
	failure := errors.New("database unavailable")
	db := sql.OpenDB(resetSQLConnector{exec: func(context.Context, string, []driver.NamedValue) (driver.Result, error) {
		return nil, failure
	}})
	defer db.Close()
	store := &PGStore{db: db, tableSQL: `"keys"`}
	if count, err := store.ResetRuntimeStats(""); count != 0 || !errors.Is(err, failure) {
		t.Fatalf("failed reset = %d, %v", count, err)
	}
}
