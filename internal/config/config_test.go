package config

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseAccessLevel(t *testing.T) {
	tests := []struct {
		in   string
		want AccessLevel
		ok   bool
	}{
		{"", ReadOnly, true},
		{"readonly", ReadOnly, true},
		{"DML-RW", DMLRW, true},
		{"ddl-rw", DDLRW, true},
		{"admin", "", false},
	}
	for _, tt := range tests {
		got, err := ParseAccessLevel(tt.in)
		if tt.ok && err != nil {
			t.Fatalf("ParseAccessLevel(%q) unexpected error: %v", tt.in, err)
		}
		if !tt.ok && err == nil {
			t.Fatalf("ParseAccessLevel(%q) expected error", tt.in)
		}
		if got != tt.want {
			t.Fatalf("ParseAccessLevel(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseTransport(t *testing.T) {
	tests := []struct {
		in   string
		want Transport
		ok   bool
	}{
		{"", StdioTransport, true},
		{"stdio", StdioTransport, true},
		{"HTTP", HTTPTransport, true},
		{"sse", "", false},
		{"websocket", "", false},
	}
	for _, tt := range tests {
		got, err := ParseTransport(tt.in)
		if tt.ok && err != nil {
			t.Fatalf("ParseTransport(%q) unexpected error: %v", tt.in, err)
		}
		if !tt.ok && err == nil {
			t.Fatalf("ParseTransport(%q) expected error", tt.in)
		}
		if got != tt.want {
			t.Fatalf("ParseTransport(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAccessLevelPermissions(t *testing.T) {
	if ReadOnly.AllowsDML() || ReadOnly.AllowsDDL() {
		t.Fatal("READONLY should not allow writes")
	}
	if !DMLRW.AllowsDML() || DMLRW.AllowsDDL() {
		t.Fatal("DML-RW should allow only DML writes")
	}
	if !DDLRW.AllowsDML() || !DDLRW.AllowsDDL() {
		t.Fatal("DDL-RW should allow DML and DDL writes")
	}
}

func TestSSLModeValidation(t *testing.T) {
	valid := []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}
	for _, mode := range valid {
		if !validSSLModes[mode] {
			t.Fatalf("%q should be a valid sslmode", mode)
		}
	}
	invalid := []string{"", "yes", "true", "false", "enabled", "bad"}
	for _, mode := range invalid {
		if validSSLModes[mode] {
			t.Fatalf("%q should not be a valid sslmode", mode)
		}
	}
}

func TestValidateTransportConfig(t *testing.T) {
	cfg := Config{
		AccessLevel:         ReadOnly,
		Host:                "localhost",
		Port:                5432,
		Database:            "db",
		User:                "postgres",
		Password:            "password",
		SSLMode:             "disable",
		ConnectionTimeout:   1,
		QueryTimeout:        1,
		MaxRowsDefault:      1,
		Transport:           HTTPTransport,
		HTTPAddr:            ":8080",
		HTTPPath:            "/mcp",
		RequireConfirmation: true,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() unexpected error: %v", err)
	}

	cfg.HTTPPath = "mcp"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() expected error for HTTP path without leading slash")
	}

	cfg.HTTPPath = "/mcp?debug=true"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() expected error for HTTP path containing a query")
	}

	cfg.Transport = StdioTransport
	cfg.HTTPAddr = ""
	cfg.HTTPPath = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("stdio config should not require HTTP settings: %v", err)
	}
}

func TestLoadRejectsMalformedValues(t *testing.T) {
	setValidEnvironment(t)
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"POSTGRESQL_PORT", "not-a-port", "must be an integer"},
		{"POSTGRESQL_CONNECTION_TIMEOUT", "soon", "must be an integer"},
		{"POSTGRESQL_QUERY_TIMEOUT", "later", "must be an integer"},
		{"POSTGRESQL_MAX_ROWS_DEFAULT", "many", "must be an integer"},
		{"POSTGRESQL_REQUIRE_CONFIRMATION", "sometimes", "must be a boolean"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(test.name, test.value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load() error = %v, want text %q", err, test.want)
			}
		})
	}
}

func TestLoadDefaultsAndNormalization(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("POSTGRESQL_SSLMODE", " REQUIRE ")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 5432 || cfg.ConnectionTimeout != 30*time.Second || cfg.QueryTimeout != 120*time.Second {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
	if cfg.HTTPAddr != "127.0.0.1:8080" || cfg.SSLMode != "require" {
		t.Fatalf("unexpected normalized config: %#v", cfg)
	}
}

func TestLoadRejectsDurationOverflow(t *testing.T) {
	setValidEnvironment(t)
	for _, value := range []string{"9223372037", "-9223372037"} {
		t.Setenv("POSTGRESQL_QUERY_TIMEOUT", value)
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "too large") {
			t.Fatalf("Load() error = %v for %s", err, value)
		}
	}
}

func TestConnectionStringEscapesCredentialsDatabaseAndIPv6(t *testing.T) {
	cfg := Config{
		Host:              "2001:db8::1",
		Port:              5432,
		Database:          "sales/2026",
		User:              "user@example.com",
		Password:          "p@ss:/word",
		SSLMode:           "verify-full",
		ConnectionTimeout: 45 * time.Second,
	}
	u, err := url.Parse(cfg.ConnectionString())
	if err != nil {
		t.Fatal(err)
	}
	password, _ := u.User.Password()
	if u.Host != "[2001:db8::1]:5432" || u.User.Username() != cfg.User || password != cfg.Password {
		t.Fatalf("connection authority was not preserved: %s", u)
	}
	if u.EscapedPath() != "/sales%2F2026" {
		t.Fatalf("escaped database path = %q", u.EscapedPath())
	}
	if u.Query().Get("sslmode") != "verify-full" || u.Query().Get("connect_timeout") != "45" {
		t.Fatalf("unexpected connection options: %s", u.RawQuery)
	}
}

func setValidEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("POSTGRESQL_HOST", "localhost")
	t.Setenv("POSTGRESQL_DATABASE", "db")
	t.Setenv("POSTGRESQL_USER", "postgres")
	t.Setenv("POSTGRESQL_PASSWORD", "password")
	for _, name := range []string{
		"POSTGRESQL_PORT", "POSTGRESQL_SSLMODE", "POSTGRESQL_ACCESS_LEVEL",
		"POSTGRESQL_CONNECTION_TIMEOUT", "POSTGRESQL_QUERY_TIMEOUT",
		"POSTGRESQL_MAX_ROWS_DEFAULT", "POSTGRESQL_REQUIRE_CONFIRMATION",
		"POSTGRESQL_TRANSPORT", "POSTGRESQL_HTTP_ADDR", "POSTGRESQL_HTTP_PATH",
		"POSTGRESQL_HTTP_BEARER_TOKEN",
	} {
		t.Setenv(name, "")
	}
}
