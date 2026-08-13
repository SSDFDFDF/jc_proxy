package keystore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"jc_proxy/internal/config"
)

// metaKeySchemaVersion is the row recording the stored layout version.
const metaKeySchemaVersion = "schema_version"

// MetaTableSuffix is appended to the configured upstream key table name to
// derive its companion metadata table.
const MetaTableSuffix = "_meta"

type PGStore struct {
	db           *sql.DB
	table        string
	tableSQL     string
	metaTable    string
	metaTableSQL string
}

func NewPGStore(cfg config.UpstreamKeyStorePGSQLConfig) (*PGStore, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, errors.New("upstream key pgsql dsn is required")
	}
	tableSQL, err := quoteQualifiedIdentifier(cfg.Table)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream key pgsql table: %w", err)
	}
	metaTable := strings.TrimSpace(cfg.Table) + MetaTableSuffix
	metaTableSQL, err := quoteQualifiedIdentifier(metaTable)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream key pgsql meta table: %w", err)
	}

	db, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("open upstream key pgsql: %w", err)
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	store := &PGStore{
		db:           db,
		table:        cfg.Table,
		tableSQL:     tableSQL,
		metaTable:    metaTable,
		metaTableSQL: metaTableSQL,
	}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *PGStore) Info() Info {
	return Info{Driver: "pgsql", Table: s.table}
}

func (s *PGStore) ListAll() (map[string][]Record, error) {
	query := fmt.Sprintf("SELECT vendor_id, api_key, remark, status, disable_reason, disabled_at, disabled_by, total_requests, success_count, last_status, unauthorized_count, forbidden_count, rate_limit_count, other_error_count, last_error, version, created_at, updated_at FROM %s ORDER BY vendor_id, api_key", s.tableSQL)
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("query upstream keys: %w", err)
	}
	defer rows.Close()

	out := map[string][]Record{}
	for rows.Next() {
		var vendorID string
		var record Record
		if err := rows.Scan(&vendorID, &record.Key, &record.Remark, &record.Status, &record.DisableReason, &record.DisabledAt, &record.DisabledBy, &record.TotalRequests, &record.SuccessCount, &record.LastStatus, &record.UnauthorizedCount, &record.ForbiddenCount, &record.RateLimitCount, &record.OtherErrorCount, &record.LastError, &record.Version, &record.CreatedAt, &record.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan upstream keys: %w", err)
		}
		out[vendorID] = append(out[vendorID], NormalizeRecord(record))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate upstream keys: %w", err)
	}
	return out, nil
}

func (s *PGStore) List(vendorID string) ([]Record, error) {
	vendorID = normalizeVendor(vendorID)
	if vendorID == "" {
		return nil, errors.New("vendor id is required")
	}

	query := fmt.Sprintf("SELECT api_key, remark, status, disable_reason, disabled_at, disabled_by, total_requests, success_count, last_status, unauthorized_count, forbidden_count, rate_limit_count, other_error_count, last_error, version, created_at, updated_at FROM %s WHERE vendor_id = $1 ORDER BY api_key", s.tableSQL)
	rows, err := s.db.Query(query, vendorID)
	if err != nil {
		return nil, fmt.Errorf("query vendor upstream keys: %w", err)
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		var record Record
		if err := rows.Scan(&record.Key, &record.Remark, &record.Status, &record.DisableReason, &record.DisabledAt, &record.DisabledBy, &record.TotalRequests, &record.SuccessCount, &record.LastStatus, &record.UnauthorizedCount, &record.ForbiddenCount, &record.RateLimitCount, &record.OtherErrorCount, &record.LastError, &record.Version, &record.CreatedAt, &record.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan vendor upstream keys: %w", err)
		}
		out = append(out, NormalizeRecord(record))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vendor upstream keys: %w", err)
	}
	return out, nil
}

func (s *PGStore) KeyMap() (map[string][]string, error) {
	all, err := s.ListAll()
	if err != nil {
		return nil, err
	}
	return toKeyMap(all), nil
}

func (s *PGStore) Replace(vendorID string, keys []string) error {
	vendorID = normalizeVendor(vendorID)
	if vendorID == "" {
		return errors.New("vendor id is required")
	}
	keys = NormalizeKeys(keys)
	selected := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		selected[key] = struct{}{}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin replace upstream keys: %w", err)
	}
	defer tx.Rollback()

	selectQuery := fmt.Sprintf("SELECT api_key, remark, status, disable_reason, disabled_at, disabled_by, total_requests, success_count, last_status, unauthorized_count, forbidden_count, rate_limit_count, other_error_count, last_error, version, created_at, updated_at FROM %s WHERE vendor_id = $1 ORDER BY api_key", s.tableSQL)
	rows, err := tx.Query(selectQuery, vendorID)
	if err != nil {
		return fmt.Errorf("query existing upstream keys: %w", err)
	}
	existing := make([]Record, 0)
	for rows.Next() {
		var record Record
		if err := rows.Scan(&record.Key, &record.Remark, &record.Status, &record.DisableReason, &record.DisabledAt, &record.DisabledBy, &record.TotalRequests, &record.SuccessCount, &record.LastStatus, &record.UnauthorizedCount, &record.ForbiddenCount, &record.RateLimitCount, &record.OtherErrorCount, &record.LastError, &record.Version, &record.CreatedAt, &record.UpdatedAt); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan existing upstream keys: %w", err)
		}
		existing = append(existing, NormalizeRecord(record))
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate existing upstream keys: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close existing upstream key rows: %w", err)
	}

	deleteQuery := fmt.Sprintf("DELETE FROM %s WHERE vendor_id = $1", s.tableSQL)
	if _, err := tx.Exec(deleteQuery, vendorID); err != nil {
		return fmt.Errorf("clear vendor upstream keys: %w", err)
	}
	insertQuery := fmt.Sprintf("INSERT INTO %s (vendor_id, api_key, remark, status, disable_reason, disabled_at, disabled_by, total_requests, success_count, last_status, unauthorized_count, forbidden_count, rate_limit_count, other_error_count, last_error, version, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)", s.tableSQL)
	if len(keys) == 0 {
		for _, record := range existing {
			if IsActiveStatus(record.Status) {
				continue
			}
			if _, err := tx.Exec(insertQuery, vendorID, record.Key, record.Remark, record.Status, record.DisableReason, record.DisabledAt, record.DisabledBy, record.TotalRequests, record.SuccessCount, record.LastStatus, record.UnauthorizedCount, record.ForbiddenCount, record.RateLimitCount, record.OtherErrorCount, record.LastError, record.Version, record.CreatedAt, record.UpdatedAt); err != nil {
				return fmt.Errorf("preserve disabled upstream key: %w", err)
			}
		}
	} else {
		existingIndex := make(map[string]Record, len(existing))
		for _, record := range existing {
			existingIndex[record.Key] = record
		}
		now := time.Now().UTC()
		for _, key := range keys {
			record, ok := existingIndex[key]
			if !ok {
				record = Record{Key: key, Status: KeyStatusActive, Version: 1, CreatedAt: now, UpdatedAt: now}
			} else {
				record.Version = nextRecordVersion(record.Version)
				record.UpdatedAt = now
			}
			record = NormalizeRecord(record)
			if _, err := tx.Exec(insertQuery, vendorID, key, record.Remark, record.Status, record.DisableReason, record.DisabledAt, record.DisabledBy, record.TotalRequests, record.SuccessCount, record.LastStatus, record.UnauthorizedCount, record.ForbiddenCount, record.RateLimitCount, record.OtherErrorCount, record.LastError, record.Version, record.CreatedAt, record.UpdatedAt); err != nil {
				return fmt.Errorf("insert upstream key: %w", err)
			}
		}
		for _, record := range existing {
			if IsActiveStatus(record.Status) {
				continue
			}
			if _, ok := selected[record.Key]; ok {
				continue
			}
			if _, err := tx.Exec(insertQuery, vendorID, record.Key, record.Remark, record.Status, record.DisableReason, record.DisabledAt, record.DisabledBy, record.TotalRequests, record.SuccessCount, record.LastStatus, record.UnauthorizedCount, record.ForbiddenCount, record.RateLimitCount, record.OtherErrorCount, record.LastError, record.Version, record.CreatedAt, record.UpdatedAt); err != nil {
				return fmt.Errorf("insert disabled upstream key: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replace upstream keys: %w", err)
	}
	return nil
}

func (s *PGStore) Append(vendorID string, keys []string) (int, error) {
	vendorID = normalizeVendor(vendorID)
	if vendorID == "" {
		return 0, errors.New("vendor id is required")
	}
	keys = NormalizeKeys(keys)
	if len(keys) == 0 {
		return 0, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin append upstream keys: %w", err)
	}
	defer tx.Rollback()

	insertQuery := fmt.Sprintf(
		"INSERT INTO %s (vendor_id, api_key, status, disable_reason, disabled_at, disabled_by, total_requests, success_count, last_status, unauthorized_count, forbidden_count, rate_limit_count, other_error_count, last_error, version, created_at, updated_at) VALUES ($1, $2, $3, '', NULL, '', 0, 0, 0, 0, 0, 0, 0, '', 1, NOW(), NOW()) ON CONFLICT (vendor_id, api_key) DO NOTHING",
		s.tableSQL,
	)
	added := 0
	for _, key := range keys {
		res, err := tx.Exec(insertQuery, vendorID, key, KeyStatusActive)
		if err != nil {
			return 0, fmt.Errorf("append upstream key: %w", err)
		}
		rows, _ := res.RowsAffected()
		added += int(rows)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit append upstream keys: %w", err)
	}
	return added, nil
}

func (s *PGStore) Delete(vendorID string, keys []string) (int, error) {
	vendorID = normalizeVendor(vendorID)
	if vendorID == "" {
		return 0, errors.New("vendor id is required")
	}
	keys = NormalizeKeys(keys)
	if len(keys) == 0 {
		return 0, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin delete upstream keys: %w", err)
	}
	defer tx.Rollback()

	deleteQuery := fmt.Sprintf("DELETE FROM %s WHERE vendor_id = $1 AND api_key = $2", s.tableSQL)
	removed := 0
	for _, key := range keys {
		res, err := tx.Exec(deleteQuery, vendorID, key)
		if err != nil {
			return 0, fmt.Errorf("delete upstream key: %w", err)
		}
		rows, _ := res.RowsAffected()
		removed += int(rows)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit delete upstream keys: %w", err)
	}
	return removed, nil
}

func (s *PGStore) SetStatus(vendorID, key, status, reason, actor string) error {
	return s.updateStatus(context.Background(), vendorID, key, -1, false, status, reason, actor)
}

func (s *PGStore) SetRemark(vendorID, key, remark string) error {
	vendorID = normalizeVendor(vendorID)
	key = strings.TrimSpace(key)
	if vendorID == "" {
		return errors.New("vendor id is required")
	}
	if key == "" {
		return errors.New("key is required")
	}
	query := fmt.Sprintf("UPDATE %s SET remark = $3, updated_at = NOW() WHERE vendor_id = $1 AND api_key = $2", s.tableSQL)
	res, err := s.db.Exec(query, vendorID, key, strings.TrimSpace(remark))
	if err != nil {
		return fmt.Errorf("update upstream key remark: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read upstream key remark update result: %w", err)
	}
	if rows == 0 {
		return ErrKeyNotFound
	}
	return nil
}

func (s *PGStore) SetStatusIfVersion(vendorID, key string, expectedVersion int64, status, reason, actor string) error {
	return s.updateStatus(context.Background(), vendorID, key, expectedVersion, true, status, reason, actor)
}

func (s *PGStore) SetStatusIfVersionContext(ctx context.Context, vendorID, key string, expectedVersion int64, status, reason, actor string) error {
	return s.updateStatus(ctx, vendorID, key, expectedVersion, true, status, reason, actor)
}

func (s *PGStore) ApplyRuntimeStatsDeltas(deltas map[string][]RuntimeStatsDelta) error {
	if len(deltas) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin apply runtime stats deltas: %w", err)
	}
	defer tx.Rollback()

	query := fmt.Sprintf(
		`UPDATE %s
SET total_requests = total_requests + $3,
    success_count = success_count + $4,
    last_status = $5,
    unauthorized_count = unauthorized_count + $6,
    forbidden_count = forbidden_count + $7,
    rate_limit_count = rate_limit_count + $8,
    other_error_count = other_error_count + $9,
    last_error = $10
WHERE vendor_id = $1 AND api_key = $2`,
		s.tableSQL,
	)
	for vendorID, records := range deltas {
		vendorID = normalizeVendor(vendorID)
		if vendorID == "" {
			continue
		}
		for _, delta := range records {
			key := strings.TrimSpace(delta.Key)
			if key == "" {
				continue
			}
			if delta.RuntimeStats.IsZero() {
				continue
			}
			if _, err := tx.Exec(
				query,
				vendorID,
				key,
				delta.TotalRequests,
				delta.SuccessCount,
				delta.LastStatus,
				delta.UnauthorizedCount,
				delta.ForbiddenCount,
				delta.RateLimitCount,
				delta.OtherErrorCount,
				normalizeRuntimeLastError(delta.LastError),
			); err != nil {
				return fmt.Errorf("apply runtime stats delta: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit runtime stats deltas: %w", err)
	}
	return nil
}

func (s *PGStore) updateStatus(ctx context.Context, vendorID, key string, expectedVersion int64, checkVersion bool, status, reason, actor string) error {
	vendorID = normalizeVendor(vendorID)
	key = strings.TrimSpace(key)
	if vendorID == "" {
		return errors.New("vendor id is required")
	}
	if key == "" {
		return errors.New("key is required")
	}

	status = NormalizeStatus(status)
	var disabledAt any
	reason = strings.TrimSpace(reason)
	actor = strings.TrimSpace(actor)
	if status == KeyStatusActive {
		reason = ""
		actor = ""
		disabledAt = nil
	} else {
		disabledAt = time.Now().UTC()
	}
	var (
		query string
		res   sql.Result
		err   error
	)
	if checkVersion {
		query = fmt.Sprintf(
			"UPDATE %s SET status = $3, disable_reason = $4, disabled_at = $5, disabled_by = $6, updated_at = NOW(), version = version + 1 WHERE vendor_id = $1 AND api_key = $2 AND version = $7",
			s.tableSQL,
		)
		res, err = s.db.ExecContext(ctx, query, vendorID, key, status, reason, disabledAt, actor, expectedVersion)
	} else {
		query = fmt.Sprintf(
			"UPDATE %s SET status = $3, disable_reason = $4, disabled_at = $5, disabled_by = $6, updated_at = NOW(), version = version + 1 WHERE vendor_id = $1 AND api_key = $2",
			s.tableSQL,
		)
		res, err = s.db.ExecContext(ctx, query, vendorID, key, status, reason, disabledAt, actor)
	}
	if err != nil {
		return fmt.Errorf("update upstream key status: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		if checkVersion {
			exists, err := s.keyExists(ctx, vendorID, key)
			if err != nil {
				return err
			}
			if exists {
				return ErrVersionMismatch
			}
		}
		return ErrKeyNotFound
	}
	return nil
}

func (s *PGStore) DeleteVendor(vendorID string) error {
	vendorID = normalizeVendor(vendorID)
	if vendorID == "" {
		return errors.New("vendor id is required")
	}
	query := fmt.Sprintf("DELETE FROM %s WHERE vendor_id = $1", s.tableSQL)
	if _, err := s.db.Exec(query, vendorID); err != nil {
		return fmt.Errorf("delete vendor upstream keys: %w", err)
	}
	return nil
}

func (s *PGStore) Close() error {
	return s.db.Close()
}

func (s *PGStore) init() error {
	exists, err := s.tableExists()
	if err != nil {
		return err
	}
	if !exists {
		return s.createSchema()
	}
	// The table already exists. Never migrate it in place: read the recorded
	// layout version and refuse anything this binary cannot address. Migrating
	// forward is the standalone upgrade command's job.
	version, err := s.storedSchemaVersion()
	if err != nil {
		return err
	}
	if version != config.CurrentSchemaVersion {
		return config.NewSchemaVersionError("upstream key table "+s.table, version)
	}
	return nil
}

// createSchema installs the current layout in one shot. The historical
// incremental ALTER TABLE steps are intentionally gone: anything older is
// handled by the upgrade command, which keeps this a single flat definition and
// leaves a clean slate for the next migration.
func (s *PGStore) createSchema() error {
	ddl := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
  vendor_id TEXT NOT NULL,
  api_key TEXT NOT NULL,
  remark TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active',
  disable_reason TEXT NOT NULL DEFAULT '',
  disabled_at TIMESTAMPTZ NULL,
  disabled_by TEXT NOT NULL DEFAULT '',
  total_requests BIGINT NOT NULL DEFAULT 0,
  success_count BIGINT NOT NULL DEFAULT 0,
  last_status INTEGER NOT NULL DEFAULT 0,
  unauthorized_count BIGINT NOT NULL DEFAULT 0,
  forbidden_count BIGINT NOT NULL DEFAULT 0,
  rate_limit_count BIGINT NOT NULL DEFAULT 0,
  other_error_count BIGINT NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  version BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (vendor_id, api_key)
)`, s.tableSQL)
	if _, err := s.db.Exec(ddl); err != nil {
		return fmt.Errorf("init upstream key pgsql table: %w", err)
	}
	if err := s.EnsureMetaTable(); err != nil {
		return err
	}
	return s.SetStoredSchemaVersion(config.CurrentSchemaVersion)
}

func (s *PGStore) tableExists() (bool, error) {
	var name *string
	if err := s.db.QueryRow(`SELECT to_regclass($1)::text`, s.table).Scan(&name); err != nil {
		return false, fmt.Errorf("probe upstream key pgsql table: %w", err)
	}
	return name != nil, nil
}

// EnsureMetaTable creates the companion metadata table when missing. Exported
// so the upgrade command can prepare it before stamping a version.
func (s *PGStore) EnsureMetaTable() error {
	ddl := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
  meta_key TEXT PRIMARY KEY,
  meta_value TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)`, s.metaTableSQL)
	if _, err := s.db.Exec(ddl); err != nil {
		return fmt.Errorf("init upstream key pgsql meta table: %w", err)
	}
	return nil
}

// storedSchemaVersion reads the recorded layout version. A missing meta table
// or row means the data predates version tracking, i.e. v1.
func (s *PGStore) storedSchemaVersion() (int, error) {
	var exists *string
	if err := s.db.QueryRow(`SELECT to_regclass($1)::text`, s.metaTable).Scan(&exists); err != nil {
		return 0, fmt.Errorf("probe upstream key pgsql meta table: %w", err)
	}
	if exists == nil {
		return config.LegacySchemaVersion, nil
	}
	query := fmt.Sprintf("SELECT meta_value FROM %s WHERE meta_key = $1", s.metaTableSQL)
	var raw string
	if err := s.db.QueryRow(query, metaKeySchemaVersion).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return config.LegacySchemaVersion, nil
		}
		return 0, fmt.Errorf("read upstream key pgsql schema version: %w", err)
	}
	version, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("parse upstream key pgsql schema version %q: %w", raw, err)
	}
	return version, nil
}

// SetStoredSchemaVersion stamps the recorded layout version.
func (s *PGStore) SetStoredSchemaVersion(version int) error {
	query := fmt.Sprintf(`
INSERT INTO %s (meta_key, meta_value, updated_at)
VALUES ($1, $2, NOW())
ON CONFLICT (meta_key)
DO UPDATE SET meta_value = EXCLUDED.meta_value, updated_at = NOW()`, s.metaTableSQL)
	if _, err := s.db.Exec(query, metaKeySchemaVersion, strconv.Itoa(version)); err != nil {
		return fmt.Errorf("record upstream key pgsql schema version: %w", err)
	}
	return nil
}

func (s *PGStore) keyExists(ctx context.Context, vendorID, key string) (bool, error) {
	query := fmt.Sprintf("SELECT 1 FROM %s WHERE vendor_id = $1 AND api_key = $2", s.tableSQL)
	var marker int
	err := s.db.QueryRowContext(ctx, query, vendorID, key).Scan(&marker)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, fmt.Errorf("query upstream key existence: %w", err)
}

var identPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func quoteQualifiedIdentifier(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("identifier is empty")
	}
	parts := strings.Split(name, ".")
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if !identPattern.MatchString(part) {
			return "", fmt.Errorf("invalid identifier segment %q", part)
		}
		quoted = append(quoted, `"`+part+`"`)
	}
	return strings.Join(quoted, "."), nil
}
