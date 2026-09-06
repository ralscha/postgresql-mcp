package tools

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"postgresql-mcp/internal/config"
	pgdb "postgresql-mcp/internal/db"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolNamesForLevel(t *testing.T) {
	tests := []struct {
		level config.AccessLevel
		want  []string
	}{
		{config.ReadOnly, []string{
			"search_schema", "describe_table", "list_table", "list_databases", "list_environments",
			"profile_table", "inspect_relationships", "inspect_dependencies", "explain_query",
			"read_data", "test_connection", "validate_environment_config",
			"list_schemas", "list_extensions", "list_views", "list_triggers",
			"show_create_table", "table_size",
		}},
		{config.DMLRW, []string{
			"search_schema", "describe_table", "list_table", "list_databases", "list_environments",
			"profile_table", "inspect_relationships", "inspect_dependencies", "explain_query",
			"read_data", "test_connection", "validate_environment_config",
			"list_schemas", "list_extensions", "list_views", "list_triggers",
			"show_create_table", "table_size",
			"insert_data", "update_data", "delete_data",
		}},
		{config.DDLRW, []string{
			"search_schema", "describe_table", "list_table", "list_databases", "list_environments",
			"profile_table", "inspect_relationships", "inspect_dependencies", "explain_query",
			"read_data", "test_connection", "validate_environment_config",
			"list_schemas", "list_extensions", "list_views", "list_triggers",
			"show_create_table", "table_size",
			"insert_data", "update_data", "delete_data",
			"create_table", "create_index", "drop_table",
		}},
	}
	for _, tt := range tests {
		got := ToolNamesForLevel(tt.level)
		if !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("ToolNamesForLevel(%s) = %#v, want %#v", tt.level, got, tt.want)
		}
	}
}

func TestToolDescriptionsForAllTools(t *testing.T) {
	for _, name := range ToolNamesForLevel(config.DDLRW) {
		tool := (&Registry{}).tool(name)
		if strings.TrimSpace(tool.Description) == "" {
			t.Fatalf("tool %q has no description", name)
		}
	}
}

func TestToolAnnotations(t *testing.T) {
	tests := []struct {
		name        string
		readOnly    bool
		destructive bool
	}{
		{name: "read_data", readOnly: true},
		{name: "insert_data"},
		{name: "create_table"},
		{name: "update_data", destructive: true},
		{name: "delete_data", destructive: true},
		{name: "drop_table", destructive: true},
	}
	for _, test := range tests {
		tool := (&Registry{}).tool(test.name)
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint != test.readOnly {
			t.Fatalf("tool %q readOnly annotation = %#v", test.name, tool.Annotations)
		}
		if tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint != test.destructive {
			t.Fatalf("tool %q destructive annotation = %#v", test.name, tool.Annotations)
		}
		if tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Fatalf("tool %q should use the closed-world hint", test.name)
		}
	}
}

func TestGeneratedInputSchemasMarkOptionalFieldsOptional(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "schema-test", Version: "test"}, nil)
	Register(server, &pgdb.Client{Config: config.Config{AccessLevel: config.DDLRW}})
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serverSession.Close() }()
	clientSession, err := mcp.NewClient(&mcp.Implementation{Name: "schema-test-client", Version: "test"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = clientSession.Close() }()

	listed, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantRequired := map[string][]string{
		"list_table":    {},
		"read_data":     {"query"},
		"explain_query": {"query"},
		"update_data":   {"table", "values", "where"},
		"drop_table":    {"table"},
	}
	for name, want := range wantRequired {
		var schema map[string]any
		for _, tool := range listed.Tools {
			if tool.Name == name {
				schema, _ = tool.InputSchema.(map[string]any)
				break
			}
		}
		if schema == nil {
			t.Fatalf("tool %q was not listed", name)
		}
		gotValues, _ := schema["required"].([]any)
		got := make([]string, 0, len(gotValues))
		for _, value := range gotValues {
			got = append(got, value.(string))
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("tool %q required fields = %#v, want %#v", name, got, want)
		}
		if name == "explain_query" {
			properties, _ := schema["properties"].(map[string]any)
			if _, exists := properties["maxRows"]; exists {
				t.Error("explain_query should not advertise the read_data-only maxRows input")
			}
		}
	}
}

func TestMutationTarget(t *testing.T) {
	table, where, args, err := mutationTarget("public.Users", "id = $id AND tenant = $tenant", map[string]any{"tenant": "a", "id": 42}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if table != `"public"."Users"` {
		t.Fatalf("table = %q", table)
	}
	if where != "(\nid = $4 AND tenant = $5\n)" {
		t.Fatalf("where = %q", where)
	}
	if len(args) != 2 || args[0] != 42 || args[1] != "a" {
		t.Fatalf("args = %#v", args)
	}
}

func TestMutationTargetRejectsUnsafeWhere(t *testing.T) {
	bad := []string{"", "1=1; DROP TABLE Users", "id IN (SELECT id FROM x); DELETE FROM x", "id = $missing", "id = $1"}
	for _, where := range bad {
		if _, _, _, err := mutationTarget("public.Users", where, nil, 0); err == nil {
			t.Fatalf("expected error for where %q", where)
		}
	}
}

func TestMutationTargetAllowsSQLTextInAString(t *testing.T) {
	_, where, _, err := mutationTarget("events", `message = 'DELETE; UPDATE'`, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if where != "(\nmessage = 'DELETE; UPDATE'\n)" {
		t.Fatalf("where = %q", where)
	}
}

func TestValidSQLType(t *testing.T) {
	valid := []string{
		"INTEGER", "text", "VARCHAR(255)", "numeric(10,2)", "BOOLEAN",
		"TIMESTAMP WITH TIME ZONE", "time without time zone",
		"JSONB", "UUID", "BIGINT", "SMALLINT", "REAL", "DOUBLE PRECISION",
		"text[]", "integer[]", "numeric(10,2)[]",
		"public.my_enum",
	}
	for _, typ := range valid {
		if !validSQLType(typ) {
			t.Fatalf("expected valid: %q", typ)
		}
	}
	invalid := []string{"", "1bad", "type; DROP", "bad type!"}
	for _, typ := range invalid {
		if validSQLType(typ) {
			t.Fatalf("expected invalid: %q", typ)
		}
	}
}

func TestCreateTableRejectsDuplicateColumnsBeforeExecution(t *testing.T) {
	registry := &Registry{client: &pgdb.Client{Config: config.Config{AccessLevel: config.DDLRW}}}
	_, _, err := registry.createTable(context.Background(), nil, CreateTableInput{
		Table: "duplicate_columns",
		Columns: []ColumnDef{
			{Name: "id", Type: "integer"},
			{Name: "id", Type: "text"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate column") {
		t.Fatalf("createTable() error = %v", err)
	}
}
