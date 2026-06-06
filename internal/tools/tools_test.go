package tools

import (
	"reflect"
	"testing"

	"postgresql-mcp/internal/config"
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

func TestMutationTarget(t *testing.T) {
	table, where, args, err := mutationTarget("public.Users", "id = $id AND tenant = $tenant", map[string]any{"tenant": "a", "id": 42})
	if err != nil {
		t.Fatal(err)
	}
	if table != `"public"."Users"` {
		t.Fatalf("table = %q", table)
	}
	if where != "id = $1 AND tenant = $2" {
		t.Fatalf("where = %q", where)
	}
	if len(args) != 2 || args[0] != 42 || args[1] != "a" {
		t.Fatalf("args = %#v", args)
	}
}

func TestMutationTargetRejectsUnsafeWhere(t *testing.T) {
	bad := []string{"", "1=1; DROP TABLE Users", "id IN (SELECT id FROM x); DELETE FROM x"}
	for _, where := range bad {
		if _, _, _, err := mutationTarget("public.Users", where, nil); err == nil {
			t.Fatalf("expected error for where %q", where)
		}
	}
}

func TestRenumberWhere(t *testing.T) {
	got := renumberWhere("id = $1 AND tenant = $2", 3)
	if got != "id = $4 AND tenant = $5" {
		t.Fatalf("got %q", got)
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
