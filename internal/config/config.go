package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type AccessLevel string

const (
	ReadOnly AccessLevel = "READONLY"
	DMLRW    AccessLevel = "DML-RW"
	DDLRW    AccessLevel = "DDL-RW"
)

type Config struct {
	AccessLevel         AccessLevel
	Host                string
	Port                int
	Database            string
	User                string
	Password            string
	SSLMode             string
	ConnectionTimeout   time.Duration
	QueryTimeout        time.Duration
	MaxRowsDefault      int
	RequireConfirmation bool
}

var validSSLModes = map[string]bool{
	"disable":     true,
	"allow":       true,
	"prefer":      true,
	"require":     true,
	"verify-ca":   true,
	"verify-full": true,
}

func ParseAccessLevel(s string) (AccessLevel, error) {
	switch AccessLevel(strings.ToUpper(strings.TrimSpace(s))) {
	case "":
		return ReadOnly, nil
	case ReadOnly:
		return ReadOnly, nil
	case DMLRW:
		return DMLRW, nil
	case DDLRW:
		return DDLRW, nil
	default:
		return "", fmt.Errorf("invalid POSTGRESQL_ACCESS_LEVEL %q, expected READONLY, DML-RW, or DDL-RW", s)
	}
}

func (l AccessLevel) AllowsDML() bool {
	return l == DMLRW || l == DDLRW
}

func (l AccessLevel) AllowsDDL() bool {
	return l == DDLRW
}

func Load() (Config, error) {
	level, err := ParseAccessLevel(os.Getenv("POSTGRESQL_ACCESS_LEVEL"))
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		AccessLevel:         level,
		Host:                os.Getenv("POSTGRESQL_HOST"),
		Database:            os.Getenv("POSTGRESQL_DATABASE"),
		User:                os.Getenv("POSTGRESQL_USER"),
		Password:            os.Getenv("POSTGRESQL_PASSWORD"),
		Port:                intEnv("POSTGRESQL_PORT", 5432),
		SSLMode:             stringEnv("POSTGRESQL_SSLMODE", "prefer"),
		ConnectionTimeout:   durationSecondsEnv("POSTGRESQL_CONNECTION_TIMEOUT", 30*time.Second),
		QueryTimeout:        durationSecondsEnv("POSTGRESQL_QUERY_TIMEOUT", 120*time.Second),
		MaxRowsDefault:      intEnv("POSTGRESQL_MAX_ROWS_DEFAULT", 1000),
		RequireConfirmation: boolEnv("POSTGRESQL_REQUIRE_CONFIRMATION", true),
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("POSTGRESQL_HOST is required")
	}
	if c.Database == "" {
		return fmt.Errorf("POSTGRESQL_DATABASE is required")
	}
	if c.User == "" {
		return fmt.Errorf("POSTGRESQL_USER is required")
	}
	if c.Password == "" {
		return fmt.Errorf("POSTGRESQL_PASSWORD is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("POSTGRESQL_PORT must be between 1 and 65535")
	}
	if !validSSLModes[c.SSLMode] {
		return fmt.Errorf("POSTGRESQL_SSLMODE must be one of: disable, allow, prefer, require, verify-ca, verify-full")
	}
	if c.ConnectionTimeout <= 0 {
		return fmt.Errorf("POSTGRESQL_CONNECTION_TIMEOUT must be positive")
	}
	if c.QueryTimeout <= 0 {
		return fmt.Errorf("POSTGRESQL_QUERY_TIMEOUT must be positive")
	}
	if c.MaxRowsDefault <= 0 || c.MaxRowsDefault > 100000 {
		return fmt.Errorf("POSTGRESQL_MAX_ROWS_DEFAULT must be between 1 and 100000")
	}
	return nil
}

func (c Config) ConnectionString() string {
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.User, c.Password),
		Host:   fmt.Sprintf("%s:%d", c.Host, c.Port),
		Path:   c.Database,
	}
	q := u.Query()
	q.Set("sslmode", c.SSLMode)
	q.Set("connect_timeout", strconv.Itoa(int(c.ConnectionTimeout.Seconds())))
	u.RawQuery = q.Encode()
	return u.String()
}

func (c Config) PublicSummary() map[string]any {
	return map[string]any{
		"accessLevel":          c.AccessLevel,
		"host":                 c.Host,
		"port":                 c.Port,
		"database":             c.Database,
		"userConfigured":       c.User != "",
		"passwordConfigured":   c.Password != "",
		"sslMode":              c.SSLMode,
		"connectionTimeoutSec": int(c.ConnectionTimeout.Seconds()),
		"queryTimeoutSec":      int(c.QueryTimeout.Seconds()),
		"maxRowsDefault":       c.MaxRowsDefault,
		"requireConfirmation":  c.RequireConfirmation,
	}
}

func intEnv(name string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func stringEnv(name string, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func boolEnv(name string, fallback bool) bool {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func durationSecondsEnv(name string, fallback time.Duration) time.Duration {
	return time.Duration(intEnv(name, int(fallback.Seconds()))) * time.Second
}
