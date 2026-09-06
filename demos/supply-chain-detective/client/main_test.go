package main

import (
	"strings"
	"testing"
)

func TestEnsureEnvForHTTPDoesNotRequireDatabaseCredentials(t *testing.T) {
	clearDemoEnvironment(t)
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("POSTGRESQL_TRANSPORT", "http")
	if err := ensureEnv(); err != nil {
		t.Fatalf("ensureEnv() = %v", err)
	}
}

func TestEnsureEnvForStdioRequiresDatabaseCredentials(t *testing.T) {
	clearDemoEnvironment(t)
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("POSTGRESQL_TRANSPORT", "stdio")
	err := ensureEnv()
	if err == nil || !strings.Contains(err.Error(), "POSTGRESQL_HOST") {
		t.Fatalf("ensureEnv() = %v", err)
	}
}

func TestHTTPEndpointConvertsWildcardAddressToLocalhost(t *testing.T) {
	t.Setenv("POSTGRESQL_HTTP_URL", "")
	t.Setenv("POSTGRESQL_HTTP_ADDR", "[::]:9090")
	t.Setenv("POSTGRESQL_HTTP_PATH", "mcp")
	if got := httpEndpoint(); got != "http://localhost:9090/mcp" {
		t.Fatalf("httpEndpoint() = %q", got)
	}
}

func clearDemoEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"OPENAI_API_KEY", "OPENAI_MODEL",
		"POSTGRESQL_HOST", "POSTGRESQL_PORT", "POSTGRESQL_DATABASE",
		"POSTGRESQL_USER", "POSTGRESQL_PASSWORD", "POSTGRESQL_SSLMODE",
		"POSTGRESQL_ACCESS_LEVEL", "POSTGRESQL_TRANSPORT",
		"POSTGRESQL_HTTP_ADDR", "POSTGRESQL_HTTP_PATH", "POSTGRESQL_HTTP_URL",
	} {
		t.Setenv(name, "")
	}
}
