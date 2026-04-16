package keystore

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"jc_proxy/internal/config"
)

type PGStore struct {
	db       *sql.DB
	table    string
	tableSQL string
}

func NewPGStore(cfg config.UpstreamKeyStorePGSQLConfig) (*PGStore, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, errors.New("upstream key pgsql dsn is required")
	}
	tableSQL, err := quoteQualifiedIdentifier(cfg.Table)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream key pgsql table: %w", err)
	}

	db, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("open upstream key pgsql: %w", err)
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	store := &PGStore{
		db:       db,
		table:    cfg.Table,
		tableSQL: tableSQL,
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
	query := fmt.Sprintf("SELECT vendor, api_key, status, disable_reason, disabled_at, disabled_by, created_at, updated_at FROM %s ORDER BY vendor, api_key", s.tableSQL)
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("query upstream keys: %w", err)
	}
	defer rows.Close()

	out := map[string][]Record{}
	for rows.Next() {
		var vendor string
		var record Record
		if err := rows.Scan(&vendor, &record.Key, &record.Status, &record.DisableReason, &record.DisabledAt, &record.DisabledBy, &record.CreatedAt, &record.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan upstream keys: %w", err)
		}
		out[vendor] = append(out[vendor], NormalizeRecord(record))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate upstream keys: %w", err)
	}
	return out, nil
}

func (s *PGStore) List(vendor string) ([]Record, error) {
	vendor = normalizeVendor(vendor)
	if vendor == "" {
		return nil, errors.New("vendor is required")
	}

	query := fmt.Sprintf("SELECT api_key, status, disable_reason, disabled_at, disabled_by, created_at, updated_at FROM %s WHERE vendor = $1 ORDER BY api_key", s.tableSQL)
	rows, err := s.db.Query(query, vendor)
	if err != nil {
		return nil, fmt.Errorf("query vendor upstream keys: %w", err)
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		var record Record
		if err := rows.Scan(&record.Key, &record.Status, &record.DisableReason, &record.DisabledAt, &record.DisabledBy, &record.CreatedAt, &record.UpdatedAt); err != nil {
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

func (s *PGStore) Replace(vendor string, keys []string) error {
	vendor = normalizeVendor(vendor)
	if vendor == "" {
		return errors.New("vendor is required")
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

	selectQuery := fmt.Sprintf("SELECT api_key, status, disable_reason, disabled_at, disabled_by, created_at, updated_at FROM %s WHERE vendor = $1 ORDER BY api_key", s.tableSQL)
	rows, err := tx.Query(selectQuery, vendor)
	if err != nil {
		return fmt.Errorf("query existing upstream keys: %w", err)
	}
	existing := make([]Record, 0)
	for rows.Next() {
		var record Record
		if err := rows.Scan(&record.Key, &record.Status, &record.DisableReason, &record.DisabledAt, &record.DisabledBy, &record.CreatedAt, &record.UpdatedAt); err != nil {
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

	deleteQuery := fmt.Sprintf("DELETE FROM %s WHERE vendor = $1", s.tableSQL)
	if _, err := tx.Exec(deleteQuery, vendor); err != nil {
		return fmt.Errorf("clear vendor upstream keys: %w", err)
	}
	insertQuery := fmt.Sprintf("INSERT INTO %s (vendor, api_key, status, disable_reason, disabled_at, disabled_by, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)", s.tableSQL)
	if len(keys) == 0 {
		for _, record := range existing {
			if IsActiveStatus(record.Status) {
				continue
			}
			if _, err := tx.Exec(insertQuery, vendor, record.Key, record.Status, record.DisableReason, record.DisabledAt, record.DisabledBy, record.CreatedAt, record.UpdatedAt); err != nil {
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
				record = Record{Key: key, Status: KeyStatusActive, CreatedAt: now, UpdatedAt: now}
			} else {
				record.UpdatedAt = now
			}
			record.Status = KeyStatusActive
			record.DisableReason = ""
			record.DisabledAt = nil
			record.DisabledBy = ""
			record = NormalizeRecord(record)
			if _, err := tx.Exec(insertQuery, vendor, key, record.Status, record.DisableReason, record.DisabledAt, record.DisabledBy, record.CreatedAt, record.UpdatedAt); err != nil {
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
			if _, err := tx.Exec(insertQuery, vendor, record.Key, record.Status, record.DisableReason, record.DisabledAt, record.DisabledBy, record.CreatedAt, record.UpdatedAt); err != nil {
				return fmt.Errorf("insert disabled upstream key: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replace upstream keys: %w", err)
	}
	return nil
}

func (s *PGStore) Append(vendor string, keys []string) (int, error) {
	vendor = normalizeVendor(vendor)
	if vendor == "" {
		return 0, errors.New("vendor is required")
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
		"INSERT INTO %s (vendor, api_key, status, disable_reason, disabled_at, disabled_by, created_at, updated_at) VALUES ($1, $2, $3, '', NULL, '', NOW(), NOW()) ON CONFLICT (vendor, api_key) DO NOTHING",
		s.tableSQL,
	)
	added := 0
	for _, key := range keys {
		res, err := tx.Exec(insertQuery, vendor, key, KeyStatusActive)
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

func (s *PGStore) Delete(vendor string, keys []string) (int, error) {
	vendor = normalizeVendor(vendor)
	if vendor == "" {
		return 0, errors.New("vendor is required")
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

	deleteQuery := fmt.Sprintf("DELETE FROM %s WHERE vendor = $1 AND api_key = $2", s.tableSQL)
	removed := 0
	for _, key := range keys {
		res, err := tx.Exec(deleteQuery, vendor, key)
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

func (s *PGStore) SetStatus(vendor, key, status, reason, actor string) error {
	vendor = normalizeVendor(vendor)
	key = strings.TrimSpace(key)
	if vendor == "" {
		return errors.New("vendor is required")
	}
	if key == "" {
		return errors.New("key is required")
	}

	status = NormalizeStatus(status)
	query := fmt.Sprintf(
		"UPDATE %s SET status = $3, disable_reason = $4, disabled_at = $5, disabled_by = $6, updated_at = NOW() WHERE vendor = $1 AND api_key = $2",
		s.tableSQL,
	)
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
	res, err := s.db.Exec(query, vendor, key, status, reason, disabledAt, actor)
	if err != nil {
		return fmt.Errorf("update upstream key status: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return errors.New("key not found")
	}
	return nil
}

func (s *PGStore) DeleteVendor(vendor string) error {
	vendor = normalizeVendor(vendor)
	if vendor == "" {
		return errors.New("vendor is required")
	}
	query := fmt.Sprintf("DELETE FROM %s WHERE vendor = $1", s.tableSQL)
	if _, err := s.db.Exec(query, vendor); err != nil {
		return fmt.Errorf("delete vendor upstream keys: %w", err)
	}
	return nil
}

func (s *PGStore) Close() error {
	return s.db.Close()
}

func (s *PGStore) init() error {
	ddl := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
  vendor TEXT NOT NULL,
  api_key TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  disable_reason TEXT NOT NULL DEFAULT '',
  disabled_at TIMESTAMPTZ NULL,
  disabled_by TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (vendor, api_key)
)`, s.tableSQL)
	if _, err := s.db.Exec(ddl); err != nil {
		return fmt.Errorf("init upstream key pgsql table: %w", err)
	}
	return nil
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
