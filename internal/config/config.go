package config

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

type envLookup func(string) (string, bool)

type AdminCredentialLayer struct {
	Username     *string
	Password     *string
	PasswordHash *string
}

type Config struct {
	Server  ServerConfig            `yaml:"server" json:"server"`
	Admin   AdminConfig             `yaml:"admin" json:"admin"`
	Storage StorageConfig           `yaml:"storage" json:"-"`
	Vendors map[string]VendorConfig `yaml:"vendors" json:"vendors"`
}

type ServerConfig struct {
	Listen          string        `yaml:"listen" json:"listen"`
	ReadTimeout     time.Duration `yaml:"read_timeout" json:"read_timeout"`
	WriteTimeout    time.Duration `yaml:"write_timeout" json:"write_timeout"`
	IdleTimeout     time.Duration `yaml:"idle_timeout" json:"idle_timeout"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout" json:"shutdown_timeout"`
}

type AdminConfig struct {
	Enabled           bool          `yaml:"enabled" json:"enabled"`
	Username          string        `yaml:"username" json:"username"`
	Password          string        `yaml:"password" json:"password"`
	PasswordHash      string        `yaml:"password_hash" json:"password_hash"`
	SessionTTL        time.Duration `yaml:"session_ttl" json:"session_ttl"`
	AuditLogPath      string        `yaml:"audit_log_path" json:"audit_log_path"`
	AllowedCIDRs      []string      `yaml:"allowed_cidrs" json:"allowed_cidrs"`
	TrustedProxyCIDRs []string      `yaml:"trusted_proxy_cidrs" json:"trusted_proxy_cidrs"`
}

type StorageConfig struct {
	Config       ConfigStoreConfig      `yaml:"config" json:"-"`
	UpstreamKeys UpstreamKeyStoreConfig `yaml:"upstream_keys" json:"-"`
}

type ConfigStoreConfig struct {
	Driver string                 `yaml:"driver" json:"-"`
	PGSQL  ConfigStorePGSQLConfig `yaml:"pgsql" json:"-"`
}

type ConfigStorePGSQLConfig struct {
	DSN             string        `yaml:"dsn" json:"-"`
	Table           string        `yaml:"table" json:"-"`
	RecordKey       string        `yaml:"record_key" json:"-"`
	MaxOpenConns    int           `yaml:"max_open_conns" json:"-"`
	MaxIdleConns    int           `yaml:"max_idle_conns" json:"-"`
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime" json:"-"`
}

type UpstreamKeyStoreConfig struct {
	Driver   string                      `yaml:"driver" json:"-"`
	FilePath string                      `yaml:"file_path" json:"-"`
	PGSQL    UpstreamKeyStorePGSQLConfig `yaml:"pgsql" json:"-"`
}

type UpstreamKeyStorePGSQLConfig struct {
	DSN             string        `yaml:"dsn" json:"-"`
	Table           string        `yaml:"table" json:"-"`
	MaxOpenConns    int           `yaml:"max_open_conns" json:"-"`
	MaxIdleConns    int           `yaml:"max_idle_conns" json:"-"`
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime" json:"-"`
}

type VendorConfig struct {
	Provider       string              `yaml:"provider" json:"provider"`
	Upstream       UpstreamConfig      `yaml:"upstream" json:"upstream"`
	LoadBalance    string              `yaml:"load_balance" json:"load_balance"`
	UpstreamAuth   UpstreamAuthConfig  `yaml:"upstream_auth" json:"upstream_auth"`
	ClientAuth     ClientAuthConfig    `yaml:"client_auth" json:"client_auth"`
	ClientHeaders  ClientHeadersConfig `yaml:"client_headers" json:"client_headers"`
	InjectedHeader map[string]string   `yaml:"inject_headers" json:"inject_headers"`
	PathRewrites   map[string]string   `yaml:"path_rewrites" json:"path_rewrites"`
	Backoff        BackoffConfig       `yaml:"backoff" json:"backoff"`
	ErrorPolicy    ErrorPolicyConfig   `yaml:"error_policy" json:"error_policy"`
	Resin          ResinConfig         `yaml:"resin" json:"resin"`
}

type UpstreamConfig struct {
	BaseURL string   `yaml:"base_url" json:"base_url"`
	Keys    []string `yaml:"keys,omitempty" json:"-"`
}

type UpstreamAuthConfig struct {
	Mode   string `yaml:"mode" json:"mode"`     // bearer | header | passthrough
	Header string `yaml:"header" json:"header"` // default Authorization
	Prefix string `yaml:"prefix" json:"prefix"` // default Bearer
}

type ClientAuthConfig struct {
	Enabled bool     `yaml:"enabled" json:"enabled"`
	Keys    []string `yaml:"keys" json:"keys"`
}

type ClientHeadersConfig struct {
	Allowlist []string `yaml:"allowlist" json:"allowlist"`
}

type BackoffConfig struct {
	Threshold int           `yaml:"threshold" json:"threshold"`
	Duration  time.Duration `yaml:"duration" json:"duration"`
}

type ErrorPolicyConfig struct {
	AutoDisable ErrorAutoDisableConfig `yaml:"auto_disable" json:"auto_disable"`
	Cooldown    ErrorCooldownConfig    `yaml:"cooldown" json:"cooldown"`
	Failover    ErrorFailoverConfig    `yaml:"failover" json:"failover"`
}

type ErrorAutoDisableConfig struct {
	InvalidKey            *bool    `yaml:"invalid_key" json:"invalid_key"`
	InvalidKeyStatusCodes []int    `yaml:"invalid_key_status_codes,omitempty" json:"invalid_key_status_codes,omitempty"`
	InvalidKeyKeywords    []string `yaml:"invalid_key_keywords,omitempty" json:"invalid_key_keywords,omitempty"`
	PaymentRequired       *bool    `yaml:"payment_required" json:"payment_required"`
	QuotaExhausted        *bool    `yaml:"quota_exhausted" json:"quota_exhausted"`
}

type ErrorCooldownConfig struct {
	RequestError    ErrorCooldownRule           `yaml:"request_error" json:"request_error"`
	Unauthorized    ErrorCooldownRule           `yaml:"unauthorized" json:"unauthorized"`
	PaymentRequired ErrorCooldownRule           `yaml:"payment_required" json:"payment_required"`
	Forbidden       ErrorCooldownRule           `yaml:"forbidden" json:"forbidden"`
	RateLimit       ErrorCooldownRule           `yaml:"rate_limit" json:"rate_limit"`
	ServerError     ErrorCooldownRule           `yaml:"server_error" json:"server_error"`
	OpenAISlowDown  ErrorCooldownRule           `yaml:"openai_slow_down" json:"openai_slow_down"`
	ResponseRules   []ErrorResponseCooldownRule `yaml:"response_rules,omitempty" json:"response_rules,omitempty"`
}

type ErrorCooldownRule struct {
	Enabled  *bool         `yaml:"enabled" json:"enabled"`
	Duration time.Duration `yaml:"duration" json:"duration"`
}

type ErrorResponseCooldownRule struct {
	StatusCodes []int         `yaml:"status_codes,omitempty" json:"status_codes,omitempty"`
	Keywords    []string      `yaml:"keywords,omitempty" json:"keywords,omitempty"`
	Duration    time.Duration `yaml:"duration" json:"duration"`
	RetryAfter  string        `yaml:"retry_after,omitempty" json:"retry_after,omitempty"` // ignore | override | max
}

type ErrorFailoverConfig struct {
	RequestError        *bool `yaml:"request_error" json:"request_error"`
	Unauthorized        *bool `yaml:"unauthorized" json:"unauthorized"`
	PaymentRequired     *bool `yaml:"payment_required" json:"payment_required"`
	Forbidden           *bool `yaml:"forbidden" json:"forbidden"`
	RateLimit           *bool `yaml:"rate_limit" json:"rate_limit"`
	ServerError         *bool `yaml:"server_error" json:"server_error"`
	ResponseStatusCodes []int `yaml:"response_status_codes,omitempty" json:"response_status_codes,omitempty"`
}

type ResinConfig struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`
	URL      string `yaml:"url" json:"url"`
	Platform string `yaml:"platform" json:"platform"`
	Mode     string `yaml:"mode" json:"mode"` // reverse only for now
}

func boolValue(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

func boolPtr(v bool) *bool {
	b := v
	return &b
}

func Load(path string) (*Config, error) {
	if err := loadDotEnv(path); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return loadBytesWithEnv(b, false)
}

func LoadBootstrap(path string) (*Config, error) {
	if err := loadDotEnv(path); err != nil {
		return nil, err
	}
	cfg, err := loadBytesWithEnv(nil, true)
	if err != nil {
		return nil, err
	}
	if cfg.Storage.Config.Driver != "pgsql" {
		return nil, errors.New("bootstrap without config file requires storage.config.driver=pgsql")
	}
	return cfg, nil
}

func LoadBytes(b []byte) (*Config, error) {
	return loadBytesWithEnv(b, false)
}

func LoadBootstrapBytes(b []byte) (*Config, error) {
	return loadBytesWithEnv(b, true)
}

func LoadBootstrapBytesNoEnv(b []byte) (*Config, error) {
	return loadBytes(b, true, nil)
}

func ParseAdminCredentialLayerYAML(b []byte) (AdminCredentialLayer, error) {
	var raw struct {
		Admin struct {
			Username     *string `yaml:"username"`
			Password     *string `yaml:"password"`
			PasswordHash *string `yaml:"password_hash"`
		} `yaml:"admin"`
	}
	if len(b) == 0 {
		return AdminCredentialLayer{}, nil
	}
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return AdminCredentialLayer{}, fmt.Errorf("parse admin credential layer yaml: %w", err)
	}
	return AdminCredentialLayer{
		Username:     raw.Admin.Username,
		Password:     raw.Admin.Password,
		PasswordHash: raw.Admin.PasswordHash,
	}, nil
}

func loadBytesWithEnv(b []byte, bootstrap bool) (*Config, error) {
	return loadBytes(b, bootstrap, os.LookupEnv)
}

func loadBytes(b []byte, bootstrap bool, lookup envLookup) (*Config, error) {
	var cfg Config
	if len(b) > 0 {
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			return nil, fmt.Errorf("parse config yaml: %w", err)
		}
	}
	if err := cfg.applyCriticalEnvOverrides(lookup); err != nil {
		return nil, err
	}
	if bootstrap {
		if err := cfg.PrepareBootstrap(); err != nil {
			return nil, err
		}
		return &cfg, nil
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func loadDotEnv(configPath string) error {
	if strings.TrimSpace(configPath) == "" {
		return nil
	}
	envPath := filepath.Join(filepath.Dir(configPath), ".env")
	if _, err := os.Stat(envPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat dotenv: %w", err)
	}
	if err := godotenv.Load(envPath); err != nil {
		return fmt.Errorf("load dotenv: %w", err)
	}
	return nil
}

func (c *Config) applyCriticalEnvOverrides(lookup envLookup) error {
	if c == nil || lookup == nil {
		return nil
	}

	if listen, ok, err := lookupListenOverride(lookup); err != nil {
		return err
	} else if ok {
		c.Server.Listen = listen
	}
	if enabled, ok, err := lookupEnvBool(lookup, "JC_PROXY_ADMIN_ENABLED"); err != nil {
		return err
	} else if ok {
		c.Admin.Enabled = enabled
	}
	if username, ok := lookupEnvValue(lookup, "JC_PROXY_ADMIN_USERNAME"); ok && strings.TrimSpace(c.Admin.Username) == "" {
		c.Admin.Username = username
	}
	if !c.Admin.HasCredentials() {
		if password, ok := lookupEnvValue(lookup, "JC_PROXY_ADMIN_PASSWORD"); ok {
			c.Admin.Password = password
			c.Admin.PasswordHash = ""
		}
		if passwordHash, ok := lookupEnvValue(lookup, "JC_PROXY_ADMIN_PASSWORD_HASH"); ok {
			c.Admin.PasswordHash = passwordHash
			c.Admin.Password = ""
		}
	}
	if value, ok, err := lookupEnvDuration(lookup, "JC_PROXY_ADMIN_SESSION_TTL"); err != nil {
		return err
	} else if ok {
		c.Admin.SessionTTL = value
	}
	if auditLogPath, ok := lookupEnvValue(lookup, "JC_PROXY_ADMIN_AUDIT_LOG_PATH"); ok {
		c.Admin.AuditLogPath = auditLogPath
	}
	if cidrs, ok := lookupEnvValue(lookup, "JC_PROXY_ADMIN_ALLOWED_CIDRS"); ok {
		c.Admin.AllowedCIDRs = splitEnvList(cidrs)
	}
	if cidrs, ok := lookupEnvValue(lookup, "JC_PROXY_ADMIN_TRUSTED_PROXY_CIDRS"); ok {
		c.Admin.TrustedProxyCIDRs = splitEnvList(cidrs)
	}

	if driver, ok := lookupEnvValue(lookup, "JC_PROXY_STORAGE_MODE"); ok {
		c.Storage.Config.Driver = driver
		c.Storage.UpstreamKeys.Driver = driver
	}
	if driver, ok := lookupEnvValue(lookup, "JC_PROXY_STORAGE_CONFIG_DRIVER"); ok {
		c.Storage.Config.Driver = driver
	}
	if driver, ok := lookupEnvValue(lookup, "JC_PROXY_STORAGE_UPSTREAM_KEYS_DRIVER"); ok {
		c.Storage.UpstreamKeys.Driver = driver
	}

	if dsn, ok := lookupEnvValue(lookup, "JC_PROXY_STORAGE_PGSQL_DSN", "DATABASE_URL"); ok {
		c.Storage.Config.PGSQL.DSN = dsn
		c.Storage.UpstreamKeys.PGSQL.DSN = dsn
	}
	if value, ok, err := lookupEnvInt(lookup, "JC_PROXY_STORAGE_PGSQL_MAX_OPEN_CONNS"); err != nil {
		return err
	} else if ok {
		c.Storage.Config.PGSQL.MaxOpenConns = value
		c.Storage.UpstreamKeys.PGSQL.MaxOpenConns = value
	}
	if value, ok, err := lookupEnvInt(lookup, "JC_PROXY_STORAGE_PGSQL_MAX_IDLE_CONNS"); err != nil {
		return err
	} else if ok {
		c.Storage.Config.PGSQL.MaxIdleConns = value
		c.Storage.UpstreamKeys.PGSQL.MaxIdleConns = value
	}
	if value, ok, err := lookupEnvDuration(lookup, "JC_PROXY_STORAGE_PGSQL_CONN_MAX_LIFETIME"); err != nil {
		return err
	} else if ok {
		c.Storage.Config.PGSQL.ConnMaxLifetime = value
		c.Storage.UpstreamKeys.PGSQL.ConnMaxLifetime = value
	}

	if dsn, ok := lookupEnvValue(lookup, "JC_PROXY_STORAGE_CONFIG_PGSQL_DSN"); ok {
		c.Storage.Config.PGSQL.DSN = dsn
	}
	if table, ok := lookupEnvValue(lookup, "JC_PROXY_STORAGE_CONFIG_PGSQL_TABLE"); ok {
		c.Storage.Config.PGSQL.Table = table
	}
	if recordKey, ok := lookupEnvValue(lookup, "JC_PROXY_STORAGE_CONFIG_PGSQL_RECORD_KEY"); ok {
		c.Storage.Config.PGSQL.RecordKey = recordKey
	}
	if value, ok, err := lookupEnvInt(lookup, "JC_PROXY_STORAGE_CONFIG_PGSQL_MAX_OPEN_CONNS"); err != nil {
		return err
	} else if ok {
		c.Storage.Config.PGSQL.MaxOpenConns = value
	}
	if value, ok, err := lookupEnvInt(lookup, "JC_PROXY_STORAGE_CONFIG_PGSQL_MAX_IDLE_CONNS"); err != nil {
		return err
	} else if ok {
		c.Storage.Config.PGSQL.MaxIdleConns = value
	}
	if value, ok, err := lookupEnvDuration(lookup, "JC_PROXY_STORAGE_CONFIG_PGSQL_CONN_MAX_LIFETIME"); err != nil {
		return err
	} else if ok {
		c.Storage.Config.PGSQL.ConnMaxLifetime = value
	}

	if filePath, ok := lookupEnvValue(lookup, "JC_PROXY_STORAGE_UPSTREAM_KEYS_FILE_PATH"); ok {
		c.Storage.UpstreamKeys.FilePath = filePath
	}
	if dsn, ok := lookupEnvValue(lookup, "JC_PROXY_STORAGE_UPSTREAM_KEYS_PGSQL_DSN"); ok {
		c.Storage.UpstreamKeys.PGSQL.DSN = dsn
	}
	if table, ok := lookupEnvValue(lookup, "JC_PROXY_STORAGE_UPSTREAM_KEYS_PGSQL_TABLE"); ok {
		c.Storage.UpstreamKeys.PGSQL.Table = table
	}
	if value, ok, err := lookupEnvInt(lookup, "JC_PROXY_STORAGE_UPSTREAM_KEYS_PGSQL_MAX_OPEN_CONNS"); err != nil {
		return err
	} else if ok {
		c.Storage.UpstreamKeys.PGSQL.MaxOpenConns = value
	}
	if value, ok, err := lookupEnvInt(lookup, "JC_PROXY_STORAGE_UPSTREAM_KEYS_PGSQL_MAX_IDLE_CONNS"); err != nil {
		return err
	} else if ok {
		c.Storage.UpstreamKeys.PGSQL.MaxIdleConns = value
	}
	if value, ok, err := lookupEnvDuration(lookup, "JC_PROXY_STORAGE_UPSTREAM_KEYS_PGSQL_CONN_MAX_LIFETIME"); err != nil {
		return err
	} else if ok {
		c.Storage.UpstreamKeys.PGSQL.ConnMaxLifetime = value
	}

	return nil
}

func lookupListenOverride(lookup envLookup) (string, bool, error) {
	if listen, ok := lookupEnvValue(lookup, "JC_PROXY_SERVER_LISTEN"); ok {
		return listen, true, nil
	}
	if raw, key, ok := lookupEnv(lookup, "JC_PROXY_SERVER_PORT", "PORT"); ok {
		port := strings.TrimSpace(strings.TrimPrefix(raw, ":"))
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", false, fmt.Errorf("invalid env %s: %q", key, raw)
		}
		return net.JoinHostPort("", strconv.Itoa(value)), true, nil
	}
	return "", false, nil
}

func lookupEnvDuration(lookup envLookup, key string) (time.Duration, bool, error) {
	raw, ok := lookupEnvValue(lookup, key)
	if !ok {
		return 0, false, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, false, fmt.Errorf("invalid env %s: %w", key, err)
	}
	return value, true, nil
}

func lookupEnvInt(lookup envLookup, key string) (int, bool, error) {
	raw, ok := lookupEnvValue(lookup, key)
	if !ok {
		return 0, false, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false, fmt.Errorf("invalid env %s: %w", key, err)
	}
	return value, true, nil
}

func lookupEnvBool(lookup envLookup, key string) (bool, bool, error) {
	raw, ok := lookupEnvValue(lookup, key)
	if !ok {
		return false, false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, false, fmt.Errorf("invalid env %s: %w", key, err)
	}
	return value, true, nil
}

func lookupEnvValue(lookup envLookup, keys ...string) (string, bool) {
	value, _, ok := lookupEnv(lookup, keys...)
	return value, ok
}

func lookupEnv(lookup envLookup, keys ...string) (string, string, bool) {
	for _, key := range keys {
		raw, ok := lookup(key)
		if !ok {
			continue
		}
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		return value, key, true
	}
	return "", "", false
}

func splitEnvList(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', ';', '\n', '\r', '\t':
			return true
		default:
			return false
		}
	})
}

func EncodeYAML(cfg *Config) ([]byte, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	copyCfg, err := cfg.Clone()
	if err != nil {
		return nil, err
	}
	copyCfg.StripExternalizedData()
	return yaml.Marshal(copyCfg)
}

func (c *Config) Clone() (*Config, error) {
	if c == nil {
		return nil, errors.New("config is nil")
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	var out Config
	if err := yaml.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("unmarshal config clone: %w", err)
	}
	if err := out.PrepareBootstrap(); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Config) PrepareAndValidate() error {
	if c == nil {
		return errors.New("config is nil")
	}
	c.applyDefaults()
	return c.Validate()
}

func (c *Config) PrepareBootstrap() error {
	if c == nil {
		return errors.New("config is nil")
	}
	c.applyDefaults()
	return c.ValidateBootstrap()
}

func (c *Config) applyDefaults() {
	if c.Server.Listen == "" {
		c.Server.Listen = ":8092"
	}
	if c.Server.ReadTimeout <= 0 {
		c.Server.ReadTimeout = 30 * time.Second
	}
	if c.Server.WriteTimeout < 0 {
		c.Server.WriteTimeout = 0
	}
	if c.Server.IdleTimeout <= 0 {
		c.Server.IdleTimeout = 90 * time.Second
	}
	if c.Server.ShutdownTimeout <= 0 {
		c.Server.ShutdownTimeout = 10 * time.Second
	}

	if c.Admin.Username == "" {
		c.Admin.Username = "admin"
	}
	if c.Admin.AuditLogPath == "" {
		c.Admin.AuditLogPath = "./data/admin_audit.log"
	}
	if c.Admin.SessionTTL <= 0 {
		c.Admin.SessionTTL = 12 * time.Hour
	}

	if c.Storage.Config.Driver == "" {
		c.Storage.Config.Driver = "file"
	}
	if c.Storage.Config.PGSQL.Table == "" {
		c.Storage.Config.PGSQL.Table = "jc_proxy_configs"
	}
	if c.Storage.Config.PGSQL.RecordKey == "" {
		c.Storage.Config.PGSQL.RecordKey = "default"
	}
	if c.Storage.Config.PGSQL.MaxOpenConns <= 0 {
		c.Storage.Config.PGSQL.MaxOpenConns = 4
	}
	if c.Storage.Config.PGSQL.MaxIdleConns <= 0 {
		c.Storage.Config.PGSQL.MaxIdleConns = 2
	}
	if c.Storage.Config.PGSQL.ConnMaxLifetime <= 0 {
		c.Storage.Config.PGSQL.ConnMaxLifetime = 30 * time.Minute
	}

	if c.Storage.UpstreamKeys.Driver == "" {
		c.Storage.UpstreamKeys.Driver = "file"
	}
	if c.Storage.UpstreamKeys.FilePath == "" {
		c.Storage.UpstreamKeys.FilePath = "./data/upstream_keys.json"
	}
	if c.Storage.UpstreamKeys.PGSQL.Table == "" {
		c.Storage.UpstreamKeys.PGSQL.Table = "jc_proxy_upstream_keys"
	}
	if c.Storage.UpstreamKeys.PGSQL.MaxOpenConns <= 0 {
		c.Storage.UpstreamKeys.PGSQL.MaxOpenConns = 4
	}
	if c.Storage.UpstreamKeys.PGSQL.MaxIdleConns <= 0 {
		c.Storage.UpstreamKeys.PGSQL.MaxIdleConns = 2
	}
	if c.Storage.UpstreamKeys.PGSQL.ConnMaxLifetime <= 0 {
		c.Storage.UpstreamKeys.PGSQL.ConnMaxLifetime = 30 * time.Minute
	}

	for name, v := range c.Vendors {
		v.Provider = NormalizeProvider(v.Provider, name)
		if v.LoadBalance == "" {
			v.LoadBalance = "round_robin"
		}
		if v.UpstreamAuth.Mode == "" {
			v.UpstreamAuth.Mode = "bearer"
		}
		if v.UpstreamAuth.Header == "" {
			v.UpstreamAuth.Header = "Authorization"
		}
		if v.UpstreamAuth.Prefix == "" && v.UpstreamAuth.Mode == "bearer" {
			v.UpstreamAuth.Prefix = "Bearer "
		}
		if v.Backoff.Threshold <= 0 {
			v.Backoff.Threshold = 3
		}
		if v.Backoff.Duration <= 0 {
			v.Backoff.Duration = 3 * time.Hour
		}
		applyErrorPolicyDefaults(&v.ErrorPolicy)
		if v.Resin.Mode == "" {
			v.Resin.Mode = "reverse"
		}
		if v.Resin.Platform == "" {
			v.Resin.Platform = "Default"
		}
		if v.PathRewrites == nil {
			v.PathRewrites = map[string]string{}
		}
		if v.InjectedHeader == nil {
			v.InjectedHeader = map[string]string{}
		}
		if v.ClientHeaders.Allowlist == nil {
			v.ClientHeaders.Allowlist = []string{}
		}
		c.Vendors[name] = v
	}
}

func (c *Config) Validate() error {
	return c.validate(true)
}

func (c *Config) ValidateBootstrap() error {
	return c.validate(false)
}

func (c *Config) validate(requireVendors bool) error {
	if requireVendors && len(c.Vendors) == 0 {
		return errors.New("config vendors is empty")
	}
	if c.Admin.Enabled {
		if strings.TrimSpace(c.Admin.Username) == "" {
			return errors.New("admin.username is required")
		}
	}
	if _, err := ParseAdminAllowedCIDRs(c.Admin.AllowedCIDRs); err != nil {
		return fmt.Errorf("admin.allowed_cidrs invalid: %w", err)
	}
	if _, err := ParseAdminTrustedProxyCIDRs(c.Admin.TrustedProxyCIDRs); err != nil {
		return fmt.Errorf("admin.trusted_proxy_cidrs invalid: %w", err)
	}

	switch c.Storage.Config.Driver {
	case "file", "pgsql":
	default:
		return fmt.Errorf("storage.config.driver invalid: %s", c.Storage.Config.Driver)
	}
	if c.Storage.Config.Driver == "pgsql" && strings.TrimSpace(c.Storage.Config.PGSQL.DSN) == "" {
		return errors.New("storage.config.pgsql.dsn is required when storage.config.driver=pgsql")
	}

	switch c.Storage.UpstreamKeys.Driver {
	case "file", "pgsql":
	default:
		return fmt.Errorf("storage.upstream_keys.driver invalid: %s", c.Storage.UpstreamKeys.Driver)
	}
	if c.Storage.UpstreamKeys.Driver == "file" && strings.TrimSpace(c.Storage.UpstreamKeys.FilePath) == "" {
		return errors.New("storage.upstream_keys.file_path is required when storage.upstream_keys.driver=file")
	}
	if c.Storage.UpstreamKeys.Driver == "pgsql" && strings.TrimSpace(c.Storage.UpstreamKeys.PGSQL.DSN) == "" {
		return errors.New("storage.upstream_keys.pgsql.dsn is required when storage.upstream_keys.driver=pgsql")
	}

	for vendorName, vendor := range c.Vendors {
		if strings.TrimSpace(vendor.Upstream.BaseURL) == "" {
			return fmt.Errorf("vendor %q upstream.base_url is required", vendorName)
		}
		for _, key := range vendor.Upstream.Keys {
			if strings.TrimSpace(key) == "" {
				return fmt.Errorf("vendor %q has empty upstream key", vendorName)
			}
		}
		switch vendor.LoadBalance {
		case "round_robin", "random", "least_used", "least_requests":
		default:
			return fmt.Errorf("vendor %q invalid load_balance: %s", vendorName, vendor.LoadBalance)
		}
		switch NormalizeProvider(vendor.Provider, vendorName) {
		case "openai", "anthropic", "gemini", "deepseek", "azure_openai", "generic":
		default:
			return fmt.Errorf("vendor %q invalid provider: %s", vendorName, vendor.Provider)
		}
		switch vendor.UpstreamAuth.Mode {
		case "bearer", "header", "passthrough":
		default:
			return fmt.Errorf("vendor %q invalid upstream_auth.mode: %s", vendorName, vendor.UpstreamAuth.Mode)
		}
		if vendor.ClientAuth.Enabled && len(vendor.ClientAuth.Keys) == 0 {
			return fmt.Errorf("vendor %q client_auth enabled but keys empty", vendorName)
		}
		if err := validateErrorPolicy(vendor.ErrorPolicy); err != nil {
			return fmt.Errorf("vendor %q invalid error_policy: %w", vendorName, err)
		}
		if vendor.Resin.Enabled {
			if strings.TrimSpace(vendor.Resin.URL) == "" {
				return fmt.Errorf("vendor %q resin.url is required when resin enabled", vendorName)
			}
			if strings.Contains(vendor.Resin.Platform, "/") {
				return fmt.Errorf("vendor %q resin.platform must be a single path segment", vendorName)
			}
			if vendor.Resin.Mode != "reverse" {
				return fmt.Errorf("vendor %q resin.mode currently supports reverse only", vendorName)
			}
		}
	}
	return nil
}

func applyErrorPolicyDefaults(policy *ErrorPolicyConfig) {
	if policy == nil {
		return
	}

	if policy.AutoDisable.InvalidKey == nil {
		policy.AutoDisable.InvalidKey = boolPtr(true)
	}
	if policy.AutoDisable.PaymentRequired == nil {
		policy.AutoDisable.PaymentRequired = boolPtr(true)
	}
	if policy.AutoDisable.QuotaExhausted == nil {
		policy.AutoDisable.QuotaExhausted = boolPtr(true)
	}

	applyCooldownRuleDefaults(&policy.Cooldown.RequestError, 2*time.Second)
	applyCooldownRuleDefaults(&policy.Cooldown.Unauthorized, 30*time.Minute)
	applyCooldownRuleDefaults(&policy.Cooldown.PaymentRequired, 3*time.Hour)
	applyCooldownRuleDefaults(&policy.Cooldown.Forbidden, 30*time.Minute)
	applyCooldownRuleDefaults(&policy.Cooldown.RateLimit, 5*time.Second)
	applyCooldownRuleDefaults(&policy.Cooldown.ServerError, 2*time.Second)
	applyCooldownRuleDefaults(&policy.Cooldown.OpenAISlowDown, 15*time.Minute)

	if policy.Failover.RequestError == nil {
		policy.Failover.RequestError = boolPtr(true)
	}
	if policy.Failover.Unauthorized == nil {
		policy.Failover.Unauthorized = boolPtr(true)
	}
	if policy.Failover.PaymentRequired == nil {
		policy.Failover.PaymentRequired = boolPtr(true)
	}
	if policy.Failover.Forbidden == nil {
		policy.Failover.Forbidden = boolPtr(true)
	}
	if policy.Failover.RateLimit == nil {
		policy.Failover.RateLimit = boolPtr(true)
	}
	if policy.Failover.ServerError == nil {
		policy.Failover.ServerError = boolPtr(true)
	}
}

func applyCooldownRuleDefaults(rule *ErrorCooldownRule, duration time.Duration) {
	if rule == nil {
		return
	}
	if rule.Enabled == nil {
		rule.Enabled = boolPtr(true)
	}
	if rule.Duration <= 0 {
		rule.Duration = duration
	}
}

func validateErrorPolicy(policy ErrorPolicyConfig) error {
	if err := validateStatusCodes("auto_disable.invalid_key_status_codes", policy.AutoDisable.InvalidKeyStatusCodes); err != nil {
		return err
	}
	if err := validateCooldownRule("request_error", policy.Cooldown.RequestError); err != nil {
		return err
	}
	if err := validateCooldownRule("unauthorized", policy.Cooldown.Unauthorized); err != nil {
		return err
	}
	if err := validateCooldownRule("payment_required", policy.Cooldown.PaymentRequired); err != nil {
		return err
	}
	if err := validateCooldownRule("forbidden", policy.Cooldown.Forbidden); err != nil {
		return err
	}
	if err := validateCooldownRule("rate_limit", policy.Cooldown.RateLimit); err != nil {
		return err
	}
	if err := validateCooldownRule("server_error", policy.Cooldown.ServerError); err != nil {
		return err
	}
	if err := validateCooldownRule("openai_slow_down", policy.Cooldown.OpenAISlowDown); err != nil {
		return err
	}
	if err := validateResponseCooldownRules(policy.Cooldown.ResponseRules); err != nil {
		return err
	}
	if err := validateStatusCodes("failover.response_status_codes", policy.Failover.ResponseStatusCodes); err != nil {
		return err
	}
	return nil
}

func validateCooldownRule(name string, rule ErrorCooldownRule) error {
	if boolValue(rule.Enabled, true) && rule.Duration <= 0 {
		return fmt.Errorf("cooldown.%s.duration must be > 0 when enabled", name)
	}
	if rule.Duration < 0 {
		return fmt.Errorf("cooldown.%s.duration must be >= 0", name)
	}
	return nil
}

func validateResponseCooldownRules(rules []ErrorResponseCooldownRule) error {
	for i, rule := range rules {
		name := fmt.Sprintf("cooldown.response_rules[%d]", i)
		if len(rule.StatusCodes) == 0 && !hasNonEmptyString(rule.Keywords) {
			return fmt.Errorf("%s must define status_codes or keywords", name)
		}
		if err := validateStatusCodes(name+".status_codes", rule.StatusCodes); err != nil {
			return err
		}
		if rule.Duration <= 0 {
			return fmt.Errorf("%s.duration must be > 0", name)
		}
		switch strings.ToLower(strings.TrimSpace(rule.RetryAfter)) {
		case "", "ignore", "override", "max":
		default:
			return fmt.Errorf("%s.retry_after must be one of ignore, override, max", name)
		}
	}
	return nil
}

func validateStatusCodes(name string, codes []int) error {
	for _, code := range codes {
		if code < 100 || code > 599 {
			return fmt.Errorf("%s contains invalid status code %d", name, code)
		}
	}
	return nil
}

func hasNonEmptyString(items []string) bool {
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			return true
		}
	}
	return false
}

func (c *Config) StripExternalizedData() {
	if c == nil {
		return
	}
	for name, vendor := range c.Vendors {
		vendor.Upstream.Keys = nil
		c.Vendors[name] = vendor
	}
}

func (c *Config) HasLegacyUpstreamKeys() bool {
	if c == nil {
		return false
	}
	for _, vendor := range c.Vendors {
		if len(vendor.Upstream.Keys) > 0 {
			return true
		}
	}
	return false
}

func VendorNames(vendors map[string]VendorConfig) []string {
	out := make([]string, 0, len(vendors))
	for name := range vendors {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func NormalizePath(path string) string {
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}

func NormalizeProvider(provider, fallback string) string {
	raw := strings.ToLower(strings.TrimSpace(provider))
	if raw == "" {
		raw = strings.ToLower(strings.TrimSpace(fallback))
	}
	switch {
	case raw == "azure_openai" || raw == "azure-openai":
		return "azure_openai"
	case strings.Contains(raw, "openai"):
		return "openai"
	case strings.Contains(raw, "anthropic") || strings.Contains(raw, "claude"):
		return "anthropic"
	case strings.Contains(raw, "gemini") || strings.Contains(raw, "google"):
		return "gemini"
	case strings.Contains(raw, "deepseek"):
		return "deepseek"
	case strings.Contains(raw, "azure"):
		return "azure_openai"
	case raw == "generic":
		return "generic"
	default:
		return "generic"
	}
}

func (a AdminConfig) HasCredentials() bool {
	return strings.TrimSpace(a.Password) != "" || strings.TrimSpace(a.PasswordHash) != ""
}

func (c *Config) NeedsBootstrapAdminPassword() bool {
	return c != nil && c.Admin.Enabled && !c.Admin.HasCredentials()
}

func ParseAdminAllowedCIDRs(values []string) ([]netip.Prefix, error) {
	return parseCIDRs(values)
}

func ParseAdminTrustedProxyCIDRs(values []string) ([]netip.Prefix, error) {
	return parseCIDRs(values)
}

func parseCIDRs(values []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(values))
	seen := make(map[netip.Prefix]struct{}, len(values))
	for _, raw := range values {
		text := strings.TrimSpace(raw)
		if text == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(text)
		if err != nil {
			return nil, fmt.Errorf("parse %q: %w", text, err)
		}
		prefix = prefix.Masked()
		if _, ok := seen[prefix]; ok {
			continue
		}
		seen[prefix] = struct{}{}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func RemoteAddrAllowed(remoteAddr string, prefixes []netip.Prefix) bool {
	addr, err := ParseRemoteAddr(remoteAddr)
	if err != nil {
		return false
	}
	return AddrAllowed(addr, prefixes)
}

func AddrAllowed(addr netip.Addr, prefixes []netip.Prefix) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func ResolveRequestAddr(remoteAddr string, headers http.Header, trustedProxies []netip.Prefix) (netip.Addr, error) {
	peer, err := ParseRemoteAddr(remoteAddr)
	if err != nil {
		return netip.Addr{}, err
	}
	if len(trustedProxies) == 0 || !AddrAllowed(peer, trustedProxies) {
		return peer, nil
	}

	forwarded := parseForwardedAddrs(headers)
	if len(forwarded) == 0 {
		return peer, nil
	}
	for i := len(forwarded) - 1; i >= 0; i-- {
		if !AddrAllowed(forwarded[i], trustedProxies) {
			return forwarded[i], nil
		}
	}
	return forwarded[0], nil
}

func RequestAddrAllowed(remoteAddr string, headers http.Header, allowed, trustedProxies []netip.Prefix) bool {
	addr, err := ResolveRequestAddr(remoteAddr, headers, trustedProxies)
	if err != nil {
		return false
	}
	return AddrAllowed(addr, allowed)
}

func parseForwardedAddrs(headers http.Header) []netip.Addr {
	if headers == nil {
		return nil
	}

	values := headers.Values("X-Forwarded-For")
	if len(values) == 0 {
		if raw := strings.TrimSpace(headers.Get("X-Real-IP")); raw != "" {
			values = []string{raw}
		}
	}

	out := make([]netip.Addr, 0)
	for _, raw := range values {
		for _, part := range strings.Split(raw, ",") {
			addr, err := ParseRemoteAddr(strings.TrimSpace(part))
			if err != nil {
				continue
			}
			out = append(out, addr)
		}
	}
	return out
}

func ParseRemoteAddr(remoteAddr string) (netip.Addr, error) {
	raw := strings.TrimSpace(remoteAddr)
	if raw == "" {
		return netip.Addr{}, errors.New("empty remote addr")
	}
	if addrPort, err := netip.ParseAddrPort(raw); err == nil {
		return addrPort.Addr().Unmap(), nil
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	raw = strings.Trim(raw, "[]")
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Addr{}, err
	}
	return addr.Unmap(), nil
}
