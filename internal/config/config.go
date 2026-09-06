package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type AccessLevel string
type Transport string

const (
	ReadOnly AccessLevel = "READONLY"
	DMLRW    AccessLevel = "DML-RW"
	DDLRW    AccessLevel = "DDL-RW"

	StdioTransport Transport = "stdio"
	HTTPTransport  Transport = "http"
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
	Transport           Transport
	HTTPAddr            string
	HTTPPath            string
	HTTPBearerToken     string
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

func ParseTransport(s string) (Transport, error) {
	switch Transport(strings.ToLower(strings.TrimSpace(s))) {
	case "":
		return StdioTransport, nil
	case StdioTransport:
		return StdioTransport, nil
	case HTTPTransport:
		return HTTPTransport, nil
	default:
		return "", fmt.Errorf("invalid POSTGRESQL_TRANSPORT %q, expected stdio or http", s)
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
	transport, err := ParseTransport(os.Getenv("POSTGRESQL_TRANSPORT"))
	if err != nil {
		return Config{}, err
	}
	port, err := intEnv("POSTGRESQL_PORT", 5432)
	if err != nil {
		return Config{}, err
	}
	connectionTimeout, err := durationSecondsEnv("POSTGRESQL_CONNECTION_TIMEOUT", 30*time.Second)
	if err != nil {
		return Config{}, err
	}
	queryTimeout, err := durationSecondsEnv("POSTGRESQL_QUERY_TIMEOUT", 120*time.Second)
	if err != nil {
		return Config{}, err
	}
	maxRows, err := intEnv("POSTGRESQL_MAX_ROWS_DEFAULT", 1000)
	if err != nil {
		return Config{}, err
	}
	requireConfirmation, err := boolEnv("POSTGRESQL_REQUIRE_CONFIRMATION", true)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		AccessLevel:         level,
		Host:                os.Getenv("POSTGRESQL_HOST"),
		Database:            os.Getenv("POSTGRESQL_DATABASE"),
		User:                os.Getenv("POSTGRESQL_USER"),
		Password:            os.Getenv("POSTGRESQL_PASSWORD"),
		Port:                port,
		SSLMode:             strings.ToLower(stringEnv("POSTGRESQL_SSLMODE", "prefer")),
		ConnectionTimeout:   connectionTimeout,
		QueryTimeout:        queryTimeout,
		MaxRowsDefault:      maxRows,
		RequireConfirmation: requireConfirmation,
		Transport:           transport,
		HTTPAddr:            stringEnv("POSTGRESQL_HTTP_ADDR", "127.0.0.1:8080"),
		HTTPPath:            stringEnv("POSTGRESQL_HTTP_PATH", "/mcp"),
		HTTPBearerToken:     os.Getenv("POSTGRESQL_HTTP_BEARER_TOKEN"),
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
	if c.Transport != StdioTransport && c.Transport != HTTPTransport {
		return fmt.Errorf("POSTGRESQL_TRANSPORT must be stdio or http")
	}
	if c.Transport == HTTPTransport {
		if c.HTTPAddr == "" {
			return fmt.Errorf("POSTGRESQL_HTTP_ADDR is required")
		}
		if _, _, err := net.SplitHostPort(c.HTTPAddr); err != nil {
			return fmt.Errorf("invalid POSTGRESQL_HTTP_ADDR %q: %w", c.HTTPAddr, err)
		}
		if c.HTTPPath == "" || !strings.HasPrefix(c.HTTPPath, "/") ||
			strings.TrimSpace(c.HTTPPath) != c.HTTPPath || strings.ContainsAny(c.HTTPPath, "?#{}") {
			return fmt.Errorf("POSTGRESQL_HTTP_PATH must be an absolute path without whitespace, query, fragment, or wildcard syntax")
		}
	}
	return nil
}

func (c Config) ConnectionString() string {
	host := c.Host
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	u := &url.URL{
		Scheme:  "postgres",
		User:    url.UserPassword(c.User, c.Password),
		Host:    net.JoinHostPort(host, strconv.Itoa(c.Port)),
		Path:    "/" + c.Database,
		RawPath: "/" + url.PathEscape(c.Database),
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
		"transport":            c.Transport,
		"httpAddr":             c.HTTPAddr,
		"httpPath":             c.HTTPPath,
		"httpBearerConfigured": c.HTTPBearerToken != "",
	}
}

func intEnv(name string, fallback int) (int, error) {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer, got %q", name, v)
		}
		return n, nil
	}
	return fallback, nil
}

func stringEnv(name string, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func boolEnv(name string, fallback bool) (bool, error) {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return false, fmt.Errorf("%s must be a boolean, got %q", name, v)
		}
		return b, nil
	}
	return fallback, nil
}

func durationSecondsEnv(name string, fallback time.Duration) (time.Duration, error) {
	seconds, err := intEnv(name, int(fallback.Seconds()))
	if err != nil {
		return 0, err
	}
	maxSeconds := int((time.Duration(1<<63 - 1)) / time.Second)
	if seconds > maxSeconds || seconds < -maxSeconds {
		return 0, fmt.Errorf("%s is too large", name)
	}
	return time.Duration(seconds) * time.Second, nil
}
