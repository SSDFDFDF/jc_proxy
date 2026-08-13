// Command jc_proxy_upgrade migrates persisted jc_proxy data forward one schema
// version at a time. The server itself never migrates in place: it detects the
// stored version and refuses to start, pointing here.
//
// v1 -> v2 moves every internal reference off the mutable vendor name and onto
// an immutable vendor id:
//
//	config   vendors: {name: {...}}  ->  vendors: [{id, name, ...}]
//	         aggregate children reference vendor_id instead of vendor
//	keys     partitioned by vendor name -> partitioned by vendor id
//	         (PostgreSQL column vendor -> vendor_id)
//
// Run with -dry-run first. Without -dry-run the config file is backed up next to
// itself and the PostgreSQL migration runs in a single transaction, after
// snapshotting the key table into an in-database backup table that survives
// the migration for manual rollback.
package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"gopkg.in/yaml.v3"

	"jc_proxy/internal/config"
)

func main() {
	configPath := flag.String("config", "./config.yaml", "config file path (empty when config lives only in PostgreSQL)")
	dryRun := flag.Bool("dry-run", false, "report what would change without writing anything")
	flag.Parse()

	if err := run(strings.TrimSpace(*configPath), *dryRun); err != nil {
		log.Fatalf("upgrade failed: %v", err)
	}
}

func run(configPath string, dryRun bool) error {
	if dryRun {
		log.Printf("dry run: no data will be modified")
		log.Printf("dry run: the vendor ids below are illustrative; the real run mints its own")
	}

	filePayload, err := readConfigFile(configPath)
	if err != nil {
		return err
	}

	storage, err := config.LoadStorageOnly(filePayload)
	if err != nil {
		return err
	}
	log.Printf("storage drivers: config=%s upstream_keys=%s", storage.Config.Driver, storage.UpstreamKeys.Driver)
	if storage.Config.Driver == "pgsql" {
		warnPoolerDSN("config store", storage.Config.PGSQL.DSN)
	}
	if storage.UpstreamKeys.Driver == "pgsql" {
		warnPoolerDSN("upstream key store", storage.UpstreamKeys.PGSQL.DSN)
	}

	// The authoritative config lives in PostgreSQL when that driver is set;
	// the local file is then only a bootstrap layer. Migrate whichever copies
	// exist so a restart cannot pick up a stale v1 payload.
	var remote *pgConfigStore
	if storage.Config.Driver == "pgsql" {
		remote, err = openPGConfigStore(storage.Config.PGSQL)
		if err != nil {
			return err
		}
		defer remote.Close()
	}

	// Build one authoritative name -> id mapping and reuse it everywhere, so a
	// partially completed run can be resumed without ids shifting.
	mapping, err := resolveVendorIDs(filePayload, remote)
	if err != nil {
		return err
	}
	if len(mapping) == 0 {
		log.Printf("no vendors found; nothing to remap")
	}
	for name, id := range mapping {
		log.Printf("vendor mapping: %q -> %s", name, id)
	}

	if len(filePayload) > 0 {
		if err := upgradeConfigFile(configPath, filePayload, mapping, dryRun); err != nil {
			return err
		}
	}
	if remote != nil {
		if err := remote.Upgrade(mapping, dryRun); err != nil {
			return err
		}
	}

	switch storage.UpstreamKeys.Driver {
	case "pgsql":
		if err := upgradeKeysPG(storage.UpstreamKeys.PGSQL, mapping, dryRun); err != nil {
			return err
		}
	case "file":
		if err := upgradeKeysFile(storage.UpstreamKeys.FilePath, mapping, dryRun); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported upstream key driver %q", storage.UpstreamKeys.Driver)
	}

	if dryRun {
		log.Printf("dry run complete; re-run without -dry-run to apply")
		return nil
	}
	log.Printf("upgrade to schema v%d complete", config.CurrentSchemaVersion)
	return nil
}

func readConfigFile(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("config file %s not found; assuming config lives in PostgreSQL", path)
			return nil, nil
		}
		return nil, fmt.Errorf("read config file: %w", err)
	}
	return payload, nil
}

// resolveVendorIDs collects the vendor set from every available config copy and
// assigns an id per name. Entries that already carry an id keep it, which makes
// the command safe to re-run after a partial failure.
func resolveVendorIDs(filePayload []byte, remote *pgConfigStore) (map[string]string, error) {
	mapping := map[string]string{}

	collect := func(payload []byte, origin string) error {
		if len(payload) == 0 {
			return nil
		}
		vendors, err := parseVendorSection(payload)
		if err != nil {
			return fmt.Errorf("%s: %w", origin, err)
		}
		for _, v := range vendors {
			if existing, ok := mapping[v.name]; ok {
				if v.id != "" && existing != v.id {
					return fmt.Errorf("%s: vendor %q already mapped to %s but carries id %s", origin, v.name, existing, v.id)
				}
				continue
			}
			if v.id != "" {
				mapping[v.name] = v.id
				continue
			}
			id, err := config.NewVendorID()
			if err != nil {
				return err
			}
			mapping[v.name] = id
		}
		return nil
	}

	if err := collect(filePayload, "config file"); err != nil {
		return nil, err
	}
	if remote != nil {
		if err := collect(remote.payload, "config database"); err != nil {
			return nil, err
		}
	}
	return mapping, nil
}

type vendorRef struct {
	name string
	id   string
}

// parseVendorSection reads the vendor list from either layout so the mapping
// step works on v1 maps and already-migrated v2 arrays alike.
func parseVendorSection(payload []byte) ([]vendorRef, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(payload, &doc); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	root := documentRoot(&doc)
	if root == nil {
		return nil, nil
	}
	vendors := mappingValue(root, "vendors")
	if vendors == nil {
		return nil, nil
	}
	switch vendors.Kind {
	case yaml.MappingNode:
		out := make([]vendorRef, 0, len(vendors.Content)/2)
		for i := 0; i+1 < len(vendors.Content); i += 2 {
			name := vendors.Content[i].Value
			id := ""
			if node := mappingValue(vendors.Content[i+1], "id"); node != nil {
				id = node.Value
			}
			out = append(out, vendorRef{name: name, id: id})
		}
		return out, nil
	case yaml.SequenceNode:
		out := make([]vendorRef, 0, len(vendors.Content))
		for _, item := range vendors.Content {
			ref := vendorRef{}
			if node := mappingValue(item, "name"); node != nil {
				ref.name = node.Value
			}
			if node := mappingValue(item, "id"); node != nil {
				ref.id = node.Value
			}
			out = append(out, ref)
		}
		return out, nil
	default:
		return nil, errors.New("vendors must be a mapping or a sequence")
	}
}

func upgradeConfigFile(path string, payload []byte, mapping map[string]string, dryRun bool) error {
	version, err := config.DetectSchemaVersion(payload)
	if err != nil {
		return err
	}
	if version == config.CurrentSchemaVersion {
		log.Printf("config file %s already at schema v%d", path, version)
		return nil
	}
	if version > config.CurrentSchemaVersion {
		return config.NewSchemaVersionError("config file "+path, version)
	}

	migrated, err := migrateConfigPayload(payload, mapping)
	if err != nil {
		return fmt.Errorf("migrate config file: %w", err)
	}
	if dryRun {
		log.Printf("would rewrite config file %s from v%d to v%d", path, version, config.CurrentSchemaVersion)
		return nil
	}

	backup := fmt.Sprintf("%s.v%d.%s.bak", path, version, time.Now().UTC().Format("20060102T150405Z"))
	if err := os.WriteFile(backup, payload, 0o600); err != nil {
		return fmt.Errorf("write config backup: %w", err)
	}
	log.Printf("config backup written to %s", backup)

	tmp := path + ".upgrade.tmp"
	if err := os.WriteFile(tmp, migrated, 0o600); err != nil {
		return fmt.Errorf("write migrated config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace config file: %w", err)
	}
	log.Printf("config file %s migrated to v%d", path, config.CurrentSchemaVersion)
	return nil
}

// migrateConfigPayload rewrites the YAML document in place through the node
// tree, so unrelated sections, field order and comments survive untouched.
func migrateConfigPayload(payload []byte, mapping map[string]string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(payload, &doc); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	root := documentRoot(&doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return nil, errors.New("config root is not a mapping")
	}

	setMappingValue(root, "schema_version", &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!int",
		Value: fmt.Sprintf("%d", config.CurrentSchemaVersion),
	}, true)

	vendors := mappingValue(root, "vendors")
	if vendors == nil {
		return marshalDocument(&doc)
	}
	if vendors.Kind == yaml.SequenceNode {
		return marshalDocument(&doc)
	}
	if vendors.Kind != yaml.MappingNode {
		return nil, errors.New("vendors must be a mapping in a v1 config")
	}

	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for i := 0; i+1 < len(vendors.Content); i += 2 {
		name := vendors.Content[i].Value
		body := vendors.Content[i+1]
		if body.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("vendor %q is not a mapping", name)
		}
		id, ok := mapping[name]
		if !ok {
			return nil, fmt.Errorf("vendor %q has no assigned id", name)
		}
		if err := rewriteAggregateChildren(body, name, mapping); err != nil {
			return nil, err
		}
		// Drop the retired inline key list; keys have lived in the key store
		// since before this migration and the field no longer exists.
		if upstream := mappingValue(body, "upstream"); upstream != nil {
			deleteMappingKey(upstream, "keys")
		}
		deleteMappingKey(body, "id")
		deleteMappingKey(body, "name")
		entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		appendMappingPair(entry, "id", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: id})
		appendMappingPair(entry, "name", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name})
		entry.Content = append(entry.Content, body.Content...)
		seq.Content = append(seq.Content, entry)
	}
	setMappingValue(root, "vendors", seq, false)
	return marshalDocument(&doc)
}

func rewriteAggregateChildren(vendorBody *yaml.Node, vendorName string, mapping map[string]string) error {
	aggregate := mappingValue(vendorBody, "aggregate")
	if aggregate == nil {
		return nil
	}
	children := mappingValue(aggregate, "children")
	if children == nil || children.Kind != yaml.SequenceNode {
		return nil
	}
	for _, child := range children.Content {
		if child.Kind != yaml.MappingNode {
			continue
		}
		if mappingValue(child, "vendor_id") != nil {
			continue
		}
		ref := mappingValue(child, "vendor")
		if ref == nil {
			return fmt.Errorf("vendor %q aggregate child is missing vendor", vendorName)
		}
		id, ok := mapping[ref.Value]
		if !ok {
			return fmt.Errorf("vendor %q aggregate child %q has no assigned id", vendorName, ref.Value)
		}
		renameMappingKey(child, "vendor", "vendor_id")
		ref.Value = id
		ref.Tag = "!!str"
	}
	return nil
}

func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc == nil {
		return nil
	}
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return nil
		}
		return doc.Content[0]
	}
	return doc
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// setMappingValue replaces a key's value, or inserts the pair. prepend places a
// newly inserted key first, which keeps schema_version at the top of the file
// where a reader expects it.
func setMappingValue(node *yaml.Node, key string, value *yaml.Node, prepend bool) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content[i+1] = value
			return
		}
	}
	if prepend {
		pair := []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			value,
		}
		node.Content = append(pair, node.Content...)
		return
	}
	appendMappingPair(node, key, value)
}

func appendMappingPair(node *yaml.Node, key string, value *yaml.Node) {
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value,
	)
}

func deleteMappingKey(node *yaml.Node, key string) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
			return
		}
	}
}

func renameMappingKey(node *yaml.Node, from, to string) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == from {
			node.Content[i].Value = to
			return
		}
	}
}

func marshalDocument(doc *yaml.Node) ([]byte, error) {
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal yaml: %w", err)
	}
	// Round-trip through the real loader so a migration that produces an
	// unreadable config fails here instead of at the next server start.
	if _, err := config.LoadBootstrapBytesNoEnv(out); err != nil {
		return nil, fmt.Errorf("migrated config does not validate: %w", err)
	}
	return out, nil
}

type pgConfigStore struct {
	db        *sql.DB
	table     string
	recordKey string
	payload   []byte
}

func openPGConfigStore(cfg config.ConfigStorePGSQLConfig) (*pgConfigStore, error) {
	db, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("open config pgsql: %w", err)
	}
	store := &pgConfigStore{db: db, table: cfg.Table, recordKey: cfg.RecordKey}
	query := fmt.Sprintf("SELECT payload FROM %s WHERE config_key = $1", quoteIdent(cfg.Table))
	var payload string
	switch err := db.QueryRow(query, cfg.RecordKey).Scan(&payload); {
	case err == nil:
		store.payload = []byte(payload)
	case errors.Is(err, sql.ErrNoRows):
		log.Printf("config database has no row %q; nothing to migrate there", cfg.RecordKey)
	default:
		_ = db.Close()
		return nil, fmt.Errorf("read config from pgsql: %w", err)
	}
	return store, nil
}

func (s *pgConfigStore) Close() { _ = s.db.Close() }

func (s *pgConfigStore) Upgrade(mapping map[string]string, dryRun bool) error {
	if len(s.payload) == 0 {
		return nil
	}
	version, err := config.DetectSchemaVersion(s.payload)
	if err != nil {
		return err
	}
	if version == config.CurrentSchemaVersion {
		log.Printf("config database row %q already at schema v%d", s.recordKey, version)
		return nil
	}
	if version > config.CurrentSchemaVersion {
		return config.NewSchemaVersionError("config database", version)
	}
	migrated, err := migrateConfigPayload(s.payload, mapping)
	if err != nil {
		return fmt.Errorf("migrate config database row: %w", err)
	}
	if dryRun {
		log.Printf("would rewrite config database row %q from v%d to v%d", s.recordKey, version, config.CurrentSchemaVersion)
		return nil
	}
	backupKey := fmt.Sprintf("%s.v%d.backup.%s", s.recordKey, version, time.Now().UTC().Format("20060102T150405Z"))
	insert := fmt.Sprintf(`
INSERT INTO %s (config_key, payload, updated_at)
VALUES ($1, $2, NOW())
ON CONFLICT (config_key)
DO UPDATE SET payload = EXCLUDED.payload, updated_at = NOW()`, quoteIdent(s.table))
	if _, err := s.db.Exec(insert, backupKey, string(s.payload)); err != nil {
		return fmt.Errorf("write config database backup: %w", err)
	}
	log.Printf("config database backup written to row %q", backupKey)
	if _, err := s.db.Exec(insert, s.recordKey, string(migrated)); err != nil {
		return fmt.Errorf("write migrated config to pgsql: %w", err)
	}
	log.Printf("config database row %q migrated to v%d", s.recordKey, config.CurrentSchemaVersion)
	return nil
}

func upgradeKeysPG(cfg config.UpstreamKeyStorePGSQLConfig, mapping map[string]string, dryRun bool) error {
	db, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return fmt.Errorf("open upstream key pgsql: %w", err)
	}
	defer db.Close()

	table := quoteIdent(cfg.Table)
	metaTable := quoteIdent(cfg.Table + "_meta")

	var exists *string
	if err := db.QueryRow(`SELECT to_regclass($1)::text`, cfg.Table).Scan(&exists); err != nil {
		return fmt.Errorf("probe upstream key table: %w", err)
	}
	if exists == nil {
		log.Printf("upstream key table %s does not exist; the server will create it at v%d", cfg.Table, config.CurrentSchemaVersion)
		return nil
	}

	hasVendorID, err := columnExists(db, cfg.Table, "vendor_id")
	if err != nil {
		return err
	}
	if hasVendorID {
		log.Printf("upstream key table %s already uses vendor_id", cfg.Table)
		if dryRun {
			return nil
		}
		return stampKeyStoreVersion(db, metaTable)
	}

	var rowCount int
	if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&rowCount); err != nil {
		return fmt.Errorf("count upstream keys: %w", err)
	}

	var names []string
	perPartition := map[string]int{}
	rows, err := db.Query(fmt.Sprintf("SELECT vendor, COUNT(*) FROM %s GROUP BY vendor ORDER BY vendor", table))
	if err != nil {
		return fmt.Errorf("list upstream key vendors: %w", err)
	}
	for rows.Next() {
		var name string
		var count int
		if err := rows.Scan(&name, &count); err != nil {
			rows.Close()
			return fmt.Errorf("scan upstream key vendor: %w", err)
		}
		names = append(names, name)
		perPartition[name] = count
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate upstream key vendors: %w", err)
	}

	var unmapped []string
	for _, name := range names {
		if _, ok := mapping[name]; !ok {
			unmapped = append(unmapped, name)
		}
	}
	if len(unmapped) > 0 {
		// These partitions belong to vendors that are no longer configured.
		// Mint ids for them so no key rows are silently dropped; the admin
		// console surfaces them as unconfigured partitions afterwards.
		for _, name := range unmapped {
			id, err := config.NewVendorID()
			if err != nil {
				return err
			}
			mapping[name] = id
			log.Printf("orphan key partition %q mapped to %s (no matching vendor in config)", name, id)
		}
	}

	for _, name := range names {
		log.Printf("key partition %q -> %s (%d row(s))", name, mapping[name], perPartition[name])
	}
	log.Printf("upstream key table %s: %d row(s) across %d vendor partition(s) to remap", cfg.Table, rowCount, len(names))
	if dryRun {
		log.Printf("would snapshot %s into an in-database backup table, rename column vendor -> vendor_id and remap %d partition(s)", cfg.Table, len(names))
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin upstream key migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Snapshot the v1 table inside the same transaction: a committed migration
	// always has its backup, a rolled back one leaves nothing behind.
	backupTable := fmt.Sprintf("%s_v1_backup_%s", cfg.Table, time.Now().UTC().Format("20060102T150405Z"))
	if _, err := tx.Exec(fmt.Sprintf("CREATE TABLE %s AS TABLE %s", quoteIdent(backupTable), table)); err != nil {
		return fmt.Errorf("create upstream key backup table: %w", err)
	}

	for _, name := range names {
		if _, err := tx.Exec(fmt.Sprintf("UPDATE %s SET vendor = $1 WHERE vendor = $2", table), mapping[name], name); err != nil {
			return fmt.Errorf("remap upstream key partition %q: %w", name, err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf("ALTER TABLE %s RENAME COLUMN vendor TO vendor_id", table)); err != nil {
		return fmt.Errorf("rename upstream key column: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit upstream key migration: %w", err)
	}
	log.Printf("in-database backup table %s created (v1 layout; drop it once the upgrade is verified)", backupTable)
	if err := verifyKeyMigration(db, table, mapping, rowCount); err != nil {
		return fmt.Errorf("post-migration verification failed (data is committed; backup table %s holds the v1 layout): %w", backupTable, err)
	}
	log.Printf("verified: %d row(s) intact, every partition uses an assigned vendor id", rowCount)
	log.Printf("upstream key table %s migrated to vendor_id", cfg.Table)
	return stampKeyStoreVersion(db, metaTable)
}

// verifyKeyMigration re-reads the migrated table and confirms nothing was lost:
// the row count is unchanged and every partition key is one of the assigned
// vendor ids. It runs after commit, so a failure cannot be undone here — the
// version stamp is withheld (the server keeps refusing to start) and the
// operator restores from the backup table instead.
func verifyKeyMigration(db *sql.DB, tableSQL string, mapping map[string]string, wantRows int) error {
	var gotRows int
	if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", tableSQL)).Scan(&gotRows); err != nil {
		return fmt.Errorf("recount upstream keys: %w", err)
	}
	if gotRows != wantRows {
		return fmt.Errorf("row count changed: had %d, now %d", wantRows, gotRows)
	}
	validIDs := make(map[string]struct{}, len(mapping))
	for _, id := range mapping {
		validIDs[id] = struct{}{}
	}
	rows, err := db.Query(fmt.Sprintf("SELECT DISTINCT vendor_id FROM %s", tableSQL))
	if err != nil {
		return fmt.Errorf("list migrated partitions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("scan migrated partition: %w", err)
		}
		if _, ok := validIDs[id]; !ok {
			return fmt.Errorf("partition %q is not an assigned vendor id", id)
		}
	}
	return rows.Err()
}

// warnPoolerDSN flags connection strings that point at a connection pooler
// endpoint (for example the Neon "-pooler" host). The migration issues DDL and
// expects its statements to share one session, so it must run against the
// direct database endpoint.
func warnPoolerDSN(origin, dsn string) {
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		return
	}
	if strings.Contains(u.Hostname(), "-pooler") {
		log.Printf("WARNING: %s DSN host %q looks like a pooler endpoint; use the direct database endpoint for this migration", origin, u.Hostname())
	}
}

func stampKeyStoreVersion(db *sql.DB, metaTable string) error {
	ddl := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
  meta_key TEXT PRIMARY KEY,
  meta_value TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)`, metaTable)
	if _, err := db.Exec(ddl); err != nil {
		return fmt.Errorf("create upstream key meta table: %w", err)
	}
	upsert := fmt.Sprintf(`
INSERT INTO %s (meta_key, meta_value, updated_at)
VALUES ('schema_version', $1, NOW())
ON CONFLICT (meta_key)
DO UPDATE SET meta_value = EXCLUDED.meta_value, updated_at = NOW()`, metaTable)
	if _, err := db.Exec(upsert, fmt.Sprintf("%d", config.CurrentSchemaVersion)); err != nil {
		return fmt.Errorf("stamp upstream key schema version: %w", err)
	}
	log.Printf("upstream key store stamped at schema v%d", config.CurrentSchemaVersion)
	return nil
}

func columnExists(db *sql.DB, table, column string) (bool, error) {
	// An unqualified table name resolves via the session's search_path, so ask
	// the server for the effective schema instead of assuming "public".
	schema, name := "", table
	if idx := strings.Index(table, "."); idx >= 0 {
		schema, name = table[:idx], table[idx+1:]
	}
	var marker int
	err := db.QueryRow(`
SELECT 1 FROM information_schema.columns
WHERE table_schema = COALESCE(NULLIF($1, ''), current_schema()) AND table_name = $2 AND column_name = $3`, schema, name, column).Scan(&marker)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("probe column %s.%s: %w", table, column, err)
	}
	return true, nil
}

func quoteIdent(name string) string {
	parts := strings.Split(strings.TrimSpace(name), ".")
	for i, part := range parts {
		parts[i] = `"` + strings.ReplaceAll(part, `"`, `""`) + `"`
	}
	return strings.Join(parts, ".")
}
