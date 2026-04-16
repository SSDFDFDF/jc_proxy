package admin

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"

	"jc_proxy/internal/config"
)

type PGConfigBackend struct {
	db        *sql.DB
	tableSQL  string
	recordKey string
}

func newPGConfigBackend(cfg config.ConfigStorePGSQLConfig) (*PGConfigBackend, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, errors.New("config pgsql dsn is required")
	}
	tableSQL, err := quoteConfigIdentifier(cfg.Table)
	if err != nil {
		return nil, fmt.Errorf("invalid config pgsql table: %w", err)
	}
	db, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("open config pgsql: %w", err)
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	backend := &PGConfigBackend{
		db:        db,
		tableSQL:  tableSQL,
		recordKey: cfg.RecordKey,
	}
	if err := backend.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return backend, nil
}

func (b *PGConfigBackend) Load() (*config.Config, error) {
	query := fmt.Sprintf("SELECT payload FROM %s WHERE config_key = $1", b.tableSQL)
	var payload string
	err := b.db.QueryRow(query, b.recordKey).Scan(&payload)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("load config from pgsql: %w", err)
	}
	cfg, err := config.LoadBytes([]byte(payload))
	if err != nil {
		return nil, fmt.Errorf("parse config from pgsql: %w", err)
	}
	return cfg, nil
}

func (b *PGConfigBackend) Save(cfg *config.Config) error {
	payload, err := config.EncodeYAML(cfg)
	if err != nil {
		return err
	}
	query := fmt.Sprintf(`
INSERT INTO %s (config_key, payload, updated_at)
VALUES ($1, $2, NOW())
ON CONFLICT (config_key)
DO UPDATE SET payload = EXCLUDED.payload, updated_at = NOW()`, b.tableSQL)
	if _, err := b.db.Exec(query, b.recordKey, string(payload)); err != nil {
		return fmt.Errorf("save config to pgsql: %w", err)
	}
	return nil
}

func (b *PGConfigBackend) Close() error {
	return b.db.Close()
}

func (b *PGConfigBackend) init() error {
	ddl := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
  config_key TEXT PRIMARY KEY,
  payload TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)`, b.tableSQL)
	if _, err := b.db.Exec(ddl); err != nil {
		return fmt.Errorf("init config pgsql table: %w", err)
	}
	return nil
}

var configIdentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func quoteConfigIdentifier(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("identifier is empty")
	}
	parts := strings.Split(name, ".")
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if !configIdentPattern.MatchString(part) {
			return "", fmt.Errorf("invalid identifier segment %q", part)
		}
		quoted = append(quoted, `"`+part+`"`)
	}
	return strings.Join(quoted, "."), nil
}
