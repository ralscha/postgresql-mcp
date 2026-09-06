package tools

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"postgresql-mcp/internal/config"
	pgdb "postgresql-mcp/internal/db"
	"postgresql-mcp/internal/sqlsafe"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Registry struct {
	client *pgdb.Client
	names  []string
}

func Register(server *mcp.Server, client *pgdb.Client) []string {
	r := &Registry{client: client}
	r.addReadOnly(server)
	if client.Config.AccessLevel.AllowsDML() {
		r.addDML(server)
	}
	if client.Config.AccessLevel.AllowsDDL() {
		r.addDDL(server)
	}
	return append([]string(nil), r.names...)
}

func ToolNamesForLevel(level config.AccessLevel) []string {
	cfg := config.Config{AccessLevel: level}
	client := &pgdb.Client{Config: cfg}
	server := mcp.NewServer(&mcp.Implementation{Name: "test"}, nil)
	return Register(server, client)
}

func (r *Registry) tool(name string) *mcp.Tool {
	r.names = append(r.names, name)
	_, mutating := mutatingTools[name]
	_, destructive := destructiveTools[name]
	return &mcp.Tool{
		Name:        name,
		Description: toolDescriptions[name],
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: new(destructive),
			IdempotentHint:  !mutating,
			OpenWorldHint:   new(false),
			ReadOnlyHint:    !mutating,
		},
	}
}

var mutatingTools = map[string]struct{}{
	"insert_data": {}, "update_data": {}, "delete_data": {},
	"create_table": {}, "create_index": {}, "drop_table": {},
}

var destructiveTools = map[string]struct{}{
	"update_data": {}, "delete_data": {}, "drop_table": {},
}

var toolDescriptions = map[string]string{
	"search_schema":               "Find tables and columns by name.",
	"describe_table":              "Show columns, keys, and indexes.",
	"list_table":                  "List database tables.",
	"list_databases":              "List PostgreSQL databases.",
	"list_environments":           "Show active connection settings.",
	"profile_table":               "Summarize table rows and columns.",
	"inspect_relationships":       "Show foreign-key relationships.",
	"inspect_dependencies":        "Find objects depending on a table.",
	"explain_query":               "Return the execution plan for SELECT.",
	"read_data":                   "Run a read-only SELECT query.",
	"test_connection":             "Verify PostgreSQL connectivity.",
	"validate_environment_config": "Validate connection configuration.",
	"list_schemas":                "List database schemas.",
	"list_extensions":             "List PostgreSQL extensions.",
	"list_views":                  "List database views.",
	"list_triggers":               "List database triggers.",
	"show_create_table":           "Generate CREATE TABLE DDL.",
	"table_size":                  "Show table size estimates.",
	"insert_data":                 "Insert rows into a table.",
	"update_data":                 "Update rows matching a filter.",
	"delete_data":                 "Delete rows matching a filter.",
	"create_table":                "Create a database table.",
	"create_index":                "Create an index on a table.",
	"drop_table":                  "Drop a database table.",
}

func (r *Registry) addReadOnly(server *mcp.Server) {
	mcp.AddTool(server, r.tool("search_schema"), r.searchSchema)
	mcp.AddTool(server, r.tool("describe_table"), r.describeTable)
	mcp.AddTool(server, r.tool("list_table"), r.listTable)
	mcp.AddTool(server, r.tool("list_databases"), r.listDatabases)
	mcp.AddTool(server, r.tool("list_environments"), r.listEnvironments)
	mcp.AddTool(server, r.tool("profile_table"), r.profileTable)
	mcp.AddTool(server, r.tool("inspect_relationships"), r.inspectRelationships)
	mcp.AddTool(server, r.tool("inspect_dependencies"), r.inspectDependencies)
	mcp.AddTool(server, r.tool("explain_query"), r.explainQuery)
	mcp.AddTool(server, r.tool("read_data"), r.readData)
	mcp.AddTool(server, r.tool("test_connection"), r.testConnection)
	mcp.AddTool(server, r.tool("validate_environment_config"), r.validateEnvironmentConfig)
	mcp.AddTool(server, r.tool("list_schemas"), r.listSchemas)
	mcp.AddTool(server, r.tool("list_extensions"), r.listExtensions)
	mcp.AddTool(server, r.tool("list_views"), r.listViews)
	mcp.AddTool(server, r.tool("list_triggers"), r.listTriggers)
	mcp.AddTool(server, r.tool("show_create_table"), r.showCreateTable)
	mcp.AddTool(server, r.tool("table_size"), r.tableSize)
}

func (r *Registry) addDML(server *mcp.Server) {
	mcp.AddTool(server, r.tool("insert_data"), r.insertData)
	mcp.AddTool(server, r.tool("update_data"), r.updateData)
	mcp.AddTool(server, r.tool("delete_data"), r.deleteData)
}

func (r *Registry) addDDL(server *mcp.Server) {
	mcp.AddTool(server, r.tool("create_table"), r.createTable)
	mcp.AddTool(server, r.tool("create_index"), r.createIndex)
	mcp.AddTool(server, r.tool("drop_table"), r.dropTable)
}

type RowsOutput struct {
	Rows []map[string]any `json:"rows"`
}

// ---- search_schema ----

type SearchSchemaInput struct {
	Query        string `json:"query" jsonschema:"Name to find; use * as a wildcard. An empty string lists everything."`
	TableOffset  int    `json:"tableOffset,omitempty" jsonschema:"Zero-based offset for table results."`
	ColumnOffset int    `json:"columnOffset,omitempty" jsonschema:"Zero-based offset for column results."`
	Limit        int    `json:"limit,omitempty" jsonschema:"Maximum results in each group; defaults to 50 and is capped at 200."`
}

type SearchSchemaOutput struct {
	Tables  []map[string]any `json:"tables"`
	Columns []map[string]any `json:"columns"`
}

func (r *Registry) searchSchema(ctx context.Context, _ *mcp.CallToolRequest, in SearchSchemaInput) (*mcp.CallToolResult, SearchSchemaOutput, error) {
	ctx, cancel := r.client.TimeoutContext(ctx)
	defer cancel()
	limit := bounded(in.Limit, 50, 200)
	pattern := sqlsafe.LikePattern(in.Query)
	tables, err := r.client.Query(ctx, `
SELECT table_schema AS schema, table_name AS table, table_type AS type
FROM information_schema.tables
WHERE table_type = 'BASE TABLE'
  AND table_schema NOT IN ('information_schema', 'pg_catalog')
  AND table_schema NOT LIKE 'pg_toast%'
  AND (table_schema LIKE $1 ESCAPE '\' OR table_name LIKE $1 ESCAPE '\')
ORDER BY table_schema, table_name
LIMIT $3 OFFSET $2`, pattern, max(in.TableOffset, 0), limit)
	if err != nil {
		return nil, SearchSchemaOutput{}, err
	}
	columns, err := r.client.Query(ctx, `
SELECT table_schema AS schema, table_name AS table, column_name AS column, data_type AS "dataType"
FROM information_schema.columns
WHERE table_schema NOT IN ('information_schema', 'pg_catalog')
  AND table_schema NOT LIKE 'pg_toast%'
  AND (table_schema LIKE $1 ESCAPE '\' OR table_name LIKE $1 ESCAPE '\' OR column_name LIKE $1 ESCAPE '\')
ORDER BY table_schema, table_name, ordinal_position
LIMIT $3 OFFSET $2`, pattern, max(in.ColumnOffset, 0), limit)
	return nil, SearchSchemaOutput{Tables: tables, Columns: columns}, err
}

// ---- describe_table ----

type TableInput struct {
	Table string `json:"table" jsonschema:"Table name as table or schema.table; unqualified names use the public schema."`
}

type DescribeTableOutput struct {
	Columns     []map[string]any `json:"columns"`
	PrimaryKeys []map[string]any `json:"primaryKeys"`
	ForeignKeys []map[string]any `json:"foreignKeys"`
	Indexes     []map[string]any `json:"indexes"`
}

func (r *Registry) describeTable(ctx context.Context, _ *mcp.CallToolRequest, in TableInput) (*mcp.CallToolResult, DescribeTableOutput, error) {
	ctx, cancel := r.client.TimeoutContext(ctx)
	defer cancel()
	schema, table, err := splitTable(in.Table)
	if err != nil {
		return nil, DescribeTableOutput{}, err
	}
	columns, err := r.client.Query(ctx, `
SELECT c.column_name AS name,
       c.data_type AS "dataType",
       c.character_maximum_length AS "maxLength",
       c.numeric_precision AS precision,
       c.numeric_scale AS scale,
       c.is_nullable AS nullable,
       c.is_identity AS identity,
       c.column_default AS "default",
       c.udt_name AS "udtName"
FROM information_schema.columns c
WHERE c.table_schema = $1 AND c.table_name = $2
ORDER BY c.ordinal_position`, schema, table)
	if err != nil {
		return nil, DescribeTableOutput{}, err
	}
	pks, err := r.client.Query(ctx, `
SELECT column_info.attname AS column, key_info.position AS ordinal
FROM pg_catalog.pg_constraint constraint_info
JOIN pg_catalog.pg_class table_info ON table_info.oid = constraint_info.conrelid
JOIN pg_catalog.pg_namespace schema_info ON schema_info.oid = table_info.relnamespace
JOIN LATERAL unnest(constraint_info.conkey) WITH ORDINALITY AS key_info(attnum, position) ON true
JOIN pg_catalog.pg_attribute column_info
	  ON column_info.attrelid = table_info.oid AND column_info.attnum = key_info.attnum
WHERE constraint_info.contype = 'p'
	  AND schema_info.nspname = $1 AND table_info.relname = $2
ORDER BY key_info.position`, schema, table)
	if err != nil {
		return nil, DescribeTableOutput{}, err
	}
	fks, err := r.foreignKeys(ctx, schema, table, "outbound")
	if err != nil {
		return nil, DescribeTableOutput{}, err
	}
	indexes, err := r.client.Query(ctx, `
SELECT index_info.relname AS name,
	   access_method.amname AS type,
	   index_metadata.indisunique AS unique,
	   index_metadata.indisvalid AS valid,
	   pg_catalog.pg_get_indexdef(index_metadata.indexrelid) AS definition,
	   pg_catalog.to_jsonb(ARRAY(
	     SELECT pg_catalog.pg_get_indexdef(index_metadata.indexrelid, position, true)
	     FROM generate_series(1, index_metadata.indnkeyatts) AS position
	     ORDER BY position
	   )) AS columns,
	   pg_catalog.to_jsonb(ARRAY(
	     SELECT pg_catalog.pg_get_indexdef(index_metadata.indexrelid, position, true)
	     FROM generate_series(index_metadata.indnkeyatts + 1, index_metadata.indnatts) AS position
	     ORDER BY position
	   )) AS "includedColumns"
FROM pg_catalog.pg_index index_metadata
JOIN pg_catalog.pg_class index_info ON index_metadata.indexrelid = index_info.oid
JOIN pg_catalog.pg_class table_info ON index_metadata.indrelid = table_info.oid
JOIN pg_catalog.pg_namespace schema_info ON table_info.relnamespace = schema_info.oid
JOIN pg_catalog.pg_am access_method ON index_info.relam = access_method.oid
WHERE schema_info.nspname = $1 AND table_info.relname = $2
	  AND index_metadata.indisprimary = false
ORDER BY index_info.relname`, schema, table)
	return nil, DescribeTableOutput{Columns: columns, PrimaryKeys: pks, ForeignKeys: fks, Indexes: indexes}, err
}

// ---- list_table ----

type ListTableInput struct {
	Schema string `json:"schema,omitempty" jsonschema:"Exact schema name; omit to include every schema."`
	Filter string `json:"filter,omitempty" jsonschema:"Table-name filter; use * as a wildcard."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum rows; defaults to 200 and is capped at 1000."`
}

func (r *Registry) listTable(ctx context.Context, _ *mcp.CallToolRequest, in ListTableInput) (*mcp.CallToolResult, RowsOutput, error) {
	ctx, cancel := r.client.TimeoutContext(ctx)
	defer cancel()
	limit := bounded(in.Limit, 200, 1000)
	rows, err := r.client.Query(ctx, `
SELECT table_schema AS schema, table_name AS table
FROM information_schema.tables
WHERE table_type = 'BASE TABLE'
  AND ($1 = '' OR table_schema = $1)
	AND ($1 <> '' OR (table_schema NOT IN ('information_schema', 'pg_catalog') AND table_schema NOT LIKE 'pg_toast%'))
  AND ($2 = '' OR table_name LIKE $2 ESCAPE '\')
ORDER BY table_schema, table_name
LIMIT $3`, in.Schema, patternOrEmpty(in.Filter), limit)
	return nil, RowsOutput{Rows: rows}, err
}

// ---- list_databases ----

func (r *Registry) listDatabases(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, RowsOutput, error) {
	ctx, cancel := r.client.TimeoutContext(ctx)
	defer cancel()
	rows, err := r.client.Query(ctx, `
SELECT datname AS name, datallowconn AS "allowConn",
       pg_catalog.pg_encoding_to_char(encoding) AS encoding
FROM pg_catalog.pg_database
WHERE datistemplate = false
ORDER BY datname`)
	return nil, RowsOutput{Rows: rows}, err
}

// ---- list_environments ----

type EnvironmentsOutput struct {
	Environments []map[string]any `json:"environments"`
}

func (r *Registry) listEnvironments(context.Context, *mcp.CallToolRequest, any) (*mcp.CallToolResult, EnvironmentsOutput, error) {
	env := r.client.Config.PublicSummary()
	env["name"] = "default"
	return nil, EnvironmentsOutput{Environments: []map[string]any{env}}, nil
}

// ---- profile_table ----

type ProfileTableInput struct {
	Table          string `json:"table" jsonschema:"Table name as table or schema.table; unqualified names use the public schema."`
	IncludeSamples bool   `json:"includeSamples,omitempty" jsonschema:"Include sample rows when true."`
	SampleSize     int    `json:"sampleSize,omitempty" jsonschema:"Sample row count; defaults to 10 and is capped at 100."`
}

type ProfileTableOutput struct {
	RowCount []map[string]any `json:"rowCount"`
	Columns  []map[string]any `json:"columns"`
	Samples  []map[string]any `json:"samples,omitempty"`
}

func (r *Registry) profileTable(ctx context.Context, _ *mcp.CallToolRequest, in ProfileTableInput) (*mcp.CallToolResult, ProfileTableOutput, error) {
	ctx, cancel := r.client.TimeoutContext(ctx)
	defer cancel()
	schema, table, err := splitTable(in.Table)
	if err != nil {
		return nil, ProfileTableOutput{}, err
	}
	qname, err := sqlsafe.QuoteMultipart(schema + "." + table)
	if err != nil {
		return nil, ProfileTableOutput{}, err
	}
	rowCount, err := r.client.Query(ctx, "SELECT COUNT(*) AS count FROM "+qname)
	if err != nil {
		return nil, ProfileTableOutput{}, err
	}
	cols, err := r.client.Query(ctx, `
SELECT column_name AS name, data_type AS "dataType"
FROM information_schema.columns
WHERE table_schema = $1 AND table_name = $2
ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return nil, ProfileTableOutput{}, err
	}
	summaries := make([]map[string]any, 0, len(cols))
	for _, col := range cols {
		name, _ := col["name"].(string)
		qcol, err := sqlsafe.QuoteIdentifier(name)
		if err != nil {
			return nil, ProfileTableOutput{}, err
		}
		stats, err := r.client.Query(ctx, fmt.Sprintf(
			"SELECT COUNT(*) FILTER (WHERE %s IS NULL) AS \"nullCount\", COUNT(DISTINCT pg_catalog.to_jsonb(%s)) AS \"distinctCount\" FROM %s",
			qcol, qcol, qname))
		if err != nil {
			return nil, ProfileTableOutput{}, err
		}
		if len(stats) == 0 {
			return nil, ProfileTableOutput{}, fmt.Errorf("profiling column %q returned no result", name)
		}
		stats[0]["name"] = name
		stats[0]["dataType"] = col["dataType"]
		minMax, minMaxErr := r.client.Query(ctx, fmt.Sprintf("SELECT MIN(%s) AS min, MAX(%s) AS max FROM %s", qcol, qcol, qname))
		if minMaxErr == nil && len(minMax) > 0 {
			stats[0]["min"] = minMax[0]["min"]
			stats[0]["max"] = minMax[0]["max"]
			stats[0]["minMaxAvailable"] = true
		} else {
			if ctx.Err() != nil {
				return nil, ProfileTableOutput{}, ctx.Err()
			}
			stats[0]["minMaxAvailable"] = false
		}
		summaries = append(summaries, stats[0])
	}
	var samples []map[string]any
	if in.IncludeSamples {
		sampleSize := bounded(in.SampleSize, 10, 100)
		samples, err = r.client.Query(ctx, fmt.Sprintf("SELECT * FROM %s LIMIT %d", qname, sampleSize))
		if err != nil {
			return nil, ProfileTableOutput{}, err
		}
	}
	return nil, ProfileTableOutput{RowCount: rowCount, Columns: summaries, Samples: samples}, nil
}

// ---- inspect_relationships ----

type RelationshipsOutput struct {
	Outbound []map[string]any `json:"outbound"`
	Inbound  []map[string]any `json:"inbound"`
}

func (r *Registry) inspectRelationships(ctx context.Context, _ *mcp.CallToolRequest, in TableInput) (*mcp.CallToolResult, RelationshipsOutput, error) {
	ctx, cancel := r.client.TimeoutContext(ctx)
	defer cancel()
	schema, table, err := splitTable(in.Table)
	if err != nil {
		return nil, RelationshipsOutput{}, err
	}
	out, err := r.foreignKeys(ctx, schema, table, "outbound")
	if err != nil {
		return nil, RelationshipsOutput{}, err
	}
	inb, err := r.foreignKeys(ctx, schema, table, "inbound")
	return nil, RelationshipsOutput{Outbound: out, Inbound: inb}, err
}

func (r *Registry) foreignKeys(ctx context.Context, schema, table, direction string) ([]map[string]any, error) {
	var filter string
	switch direction {
	case "outbound":
		filter = "source_namespace.nspname = $1 AND source_table.relname = $2"
	case "inbound":
		filter = "target_namespace.nspname = $1 AND target_table.relname = $2"
	default:
		return nil, fmt.Errorf("invalid relationship direction %q", direction)
	}
	return r.client.Query(ctx, `
SELECT
	    constraint_info.conname AS "foreignKey",
	    source_namespace.nspname AS schema,
	    source_table.relname AS table,
	    source_column.attname AS column,
	    target_namespace.nspname AS "referencedSchema",
	    target_table.relname AS "referencedTable",
	    target_column.attname AS "referencedColumn",
	    pg_catalog.pg_get_constraintdef(constraint_info.oid, true) AS definition
FROM pg_catalog.pg_constraint constraint_info
JOIN pg_catalog.pg_class source_table ON source_table.oid = constraint_info.conrelid
JOIN pg_catalog.pg_namespace source_namespace ON source_namespace.oid = source_table.relnamespace
JOIN pg_catalog.pg_class target_table ON target_table.oid = constraint_info.confrelid
JOIN pg_catalog.pg_namespace target_namespace ON target_namespace.oid = target_table.relnamespace
JOIN LATERAL unnest(constraint_info.conkey) WITH ORDINALITY AS source_key(attnum, position) ON true
JOIN LATERAL unnest(constraint_info.confkey) WITH ORDINALITY AS target_key(attnum, position)
	  ON target_key.position = source_key.position
JOIN pg_catalog.pg_attribute source_column
	  ON source_column.attrelid = source_table.oid AND source_column.attnum = source_key.attnum
JOIN pg_catalog.pg_attribute target_column
	  ON target_column.attrelid = target_table.oid AND target_column.attnum = target_key.attnum
WHERE constraint_info.contype = 'f' AND (`+filter+`)
ORDER BY source_namespace.nspname, source_table.relname, constraint_info.conname, source_key.position`, schema, table)
}

// ---- inspect_dependencies ----

type DependenciesOutput struct {
	Dependencies []map[string]any `json:"dependencies"`
}

func (r *Registry) inspectDependencies(ctx context.Context, _ *mcp.CallToolRequest, in TableInput) (*mcp.CallToolResult, DependenciesOutput, error) {
	ctx, cancel := r.client.TimeoutContext(ctx)
	defer cancel()
	schema, table, err := splitTable(in.Table)
	if err != nil {
		return nil, DependenciesOutput{}, err
	}
	rows, err := r.client.Query(ctx, `
SELECT vtu.view_schema AS schema, vtu.view_name AS object, 'VIEW' AS type
FROM information_schema.view_table_usage vtu
WHERE vtu.table_schema = $1 AND vtu.table_name = $2
UNION ALL
SELECT r.routine_schema AS schema, r.routine_name AS object, 'FUNCTION' AS type
FROM information_schema.routine_table_usage rtu
JOIN information_schema.routines r
  ON rtu.specific_schema = r.specific_schema AND rtu.specific_name = r.specific_name
WHERE rtu.table_schema = $1 AND rtu.table_name = $2
ORDER BY schema, object`, schema, table)
	return nil, DependenciesOutput{Dependencies: rows}, err
}

// ---- read_data ----

type QueryInput struct {
	Query   string `json:"query" jsonschema:"One read-only SELECT statement. Multiple or mutating statements are rejected."`
	MaxRows int    `json:"maxRows,omitempty" jsonschema:"Maximum returned rows, capped by POSTGRESQL_MAX_ROWS_DEFAULT."`
}

func (r *Registry) readData(ctx context.Context, _ *mcp.CallToolRequest, in QueryInput) (*mcp.CallToolResult, RowsOutput, error) {
	ctx, cancel := r.client.TimeoutContext(ctx)
	defer cancel()
	maxRows := bounded(in.MaxRows, r.client.Config.MaxRowsDefault, r.client.Config.MaxRowsDefault)
	rows, err := r.client.QueryReadOnly(ctx, in.Query, maxRows)
	return nil, RowsOutput{Rows: rows}, err
}

// ---- explain_query ----

type ExplainOutput struct {
	Plan []map[string]any `json:"plan"`
}

type ExplainInput struct {
	Query string `json:"query" jsonschema:"One read-only SELECT statement to plan. Multiple or mutating statements are rejected."`
}

func (r *Registry) explainQuery(ctx context.Context, _ *mcp.CallToolRequest, in ExplainInput) (*mcp.CallToolResult, ExplainOutput, error) {
	ctx, cancel := r.client.TimeoutContext(ctx)
	defer cancel()
	plan, err := r.client.ExplainReadOnly(ctx, in.Query)
	return nil, ExplainOutput{Plan: plan}, err
}

// ---- test_connection ----

type ConnectionOutput struct {
	OK        bool             `json:"ok"`
	LatencyMS int64            `json:"latencyMs"`
	Config    map[string]any   `json:"config"`
	Rows      []map[string]any `json:"rows,omitempty"`
}

func (r *Registry) testConnection(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, ConnectionOutput, error) {
	ctx, cancel := context.WithTimeout(ctx, r.client.Config.ConnectionTimeout)
	defer cancel()
	start := time.Now()
	if err := r.client.DB.PingContext(ctx); err != nil {
		return nil, ConnectionOutput{}, err
	}
	rows, err := r.client.Query(ctx, `SELECT current_database() AS database, version() AS version`)
	if err != nil {
		return nil, ConnectionOutput{}, err
	}
	return nil, ConnectionOutput{OK: true, LatencyMS: time.Since(start).Milliseconds(), Config: r.client.Config.PublicSummary(), Rows: rows}, nil
}

// ---- validate_environment_config ----

type ValidationOutput struct {
	Valid  bool           `json:"valid"`
	Config map[string]any `json:"config"`
}

func (r *Registry) validateEnvironmentConfig(context.Context, *mcp.CallToolRequest, any) (*mcp.CallToolResult, ValidationOutput, error) {
	err := r.client.Config.Validate()
	return nil, ValidationOutput{Valid: err == nil, Config: r.client.Config.PublicSummary()}, err
}

// ---- list_schemas ----

func (r *Registry) listSchemas(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, RowsOutput, error) {
	ctx, cancel := r.client.TimeoutContext(ctx)
	defer cancel()
	rows, err := r.client.Query(ctx, `
SELECT nspname AS schema,
       pg_catalog.pg_get_userbyid(nspowner) AS owner,
       obj_description(oid, 'pg_namespace') AS description
FROM pg_catalog.pg_namespace
WHERE nspname NOT LIKE 'pg_%' AND nspname != 'information_schema'
ORDER BY nspname`)
	return nil, RowsOutput{Rows: rows}, err
}

// ---- list_extensions ----

func (r *Registry) listExtensions(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, RowsOutput, error) {
	ctx, cancel := r.client.TimeoutContext(ctx)
	defer cancel()
	rows, err := r.client.Query(ctx, `
SELECT extname AS name,
       extversion AS version,
       obj_description(oid, 'pg_extension') AS description
FROM pg_catalog.pg_extension
ORDER BY extname`)
	return nil, RowsOutput{Rows: rows}, err
}

// ---- list_views ----

func (r *Registry) listViews(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, RowsOutput, error) {
	ctx, cancel := r.client.TimeoutContext(ctx)
	defer cancel()
	rows, err := r.client.Query(ctx, `
SELECT schemaname AS schema,
       viewname AS view,
       pg_get_viewdef(format('%I.%I', schemaname, viewname), true) AS definition
FROM pg_catalog.pg_views
WHERE schemaname NOT IN ('information_schema', 'pg_catalog')
ORDER BY schemaname, viewname`)
	return nil, RowsOutput{Rows: rows}, err
}

// ---- list_triggers ----

func (r *Registry) listTriggers(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, RowsOutput, error) {
	ctx, cancel := r.client.TimeoutContext(ctx)
	defer cancel()
	rows, err := r.client.Query(ctx, `
SELECT trigger_schema AS schema,
       trigger_name AS name,
       event_object_schema AS "tableSchema",
       event_object_table AS "table",
       event_manipulation AS event,
       action_timing AS timing,
       action_orientation AS orientation,
       action_statement AS definition
FROM information_schema.triggers
WHERE trigger_schema NOT IN ('information_schema', 'pg_catalog')
ORDER BY trigger_schema, event_object_table, trigger_name`)
	return nil, RowsOutput{Rows: rows}, err
}

// ---- show_create_table ----

type DDLOutput struct {
	DDL string `json:"ddl"`
}

func (r *Registry) showCreateTable(ctx context.Context, _ *mcp.CallToolRequest, in TableInput) (*mcp.CallToolResult, DDLOutput, error) {
	ctx, cancel := r.client.TimeoutContext(ctx)
	defer cancel()
	schema, table, err := splitTable(in.Table)
	if err != nil {
		return nil, DDLOutput{}, err
	}
	qname, err := sqlsafe.QuoteMultipart(schema + "." + table)
	if err != nil {
		return nil, DDLOutput{}, err
	}

	cols, err := r.client.Query(ctx, `
SELECT a.attname AS name,
       pg_catalog.format_type(a.atttypid, a.atttypmod) AS type,
       a.attnotnull AS "notNull",
       a.atthasdef AS "hasDefault",
       pg_catalog.pg_get_expr(ad.adbin, ad.adrelid) AS "defaultVal",
       a.attidentity != '' AS identity,
	   CASE a.attidentity WHEN 'a' THEN 'ALWAYS' WHEN 'd' THEN 'BY DEFAULT' ELSE '' END AS "identityType",
	   a.attgenerated AS "generatedType"
FROM pg_catalog.pg_attribute a
JOIN pg_catalog.pg_class c ON a.attrelid = c.oid
JOIN pg_catalog.pg_namespace n ON c.relnamespace = n.oid
LEFT JOIN pg_catalog.pg_attrdef ad ON a.attrelid = ad.adrelid AND a.attnum = ad.adnum
WHERE n.nspname = $1 AND c.relname = $2
	  AND c.relkind IN ('r', 'p')
	  AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum`, schema, table)
	if err != nil {
		return nil, DDLOutput{}, err
	}

	if len(cols) == 0 {
		return nil, DDLOutput{}, fmt.Errorf("table %s.%s does not exist or is not a table", schema, table)
	}

	colDefs := make([]string, 0, len(cols))
	for _, col := range cols {
		name, _ := col["name"].(string)
		typ, _ := col["type"].(string)
		qcol, err := sqlsafe.QuoteIdentifier(name)
		if err != nil {
			return nil, DDLOutput{}, err
		}
		def := qcol + " " + typ
		identity, _ := col["identity"].(bool)
		generatedType, _ := col["generatedType"].(string)
		defaultValue, _ := col["defaultVal"].(string)
		if identity {
			idt, _ := col["identityType"].(string)
			if idt == "ALWAYS" {
				def += " GENERATED ALWAYS AS IDENTITY"
			} else {
				def += " GENERATED BY DEFAULT AS IDENTITY"
			}
		} else if generatedType != "" {
			def += " GENERATED ALWAYS AS (" + defaultValue + ")"
			if generatedType == "v" {
				def += " VIRTUAL"
			} else {
				def += " STORED"
			}
		} else if hasDefault, _ := col["hasDefault"].(bool); hasDefault && defaultValue != "" {
			def += " DEFAULT " + defaultValue
		}
		if notNull, _ := col["notNull"].(bool); notNull {
			def += " NOT NULL"
		}
		colDefs = append(colDefs, def)
	}

	constraints, err := r.client.Query(ctx, `
SELECT constraint_info.conname AS name,
	   pg_catalog.pg_get_constraintdef(constraint_info.oid, true) AS definition
FROM pg_catalog.pg_constraint constraint_info
JOIN pg_catalog.pg_class table_info ON table_info.oid = constraint_info.conrelid
JOIN pg_catalog.pg_namespace schema_info ON schema_info.oid = table_info.relnamespace
WHERE schema_info.nspname = $1 AND table_info.relname = $2
	  AND constraint_info.contype IN ('p', 'u', 'f', 'c', 'x')
ORDER BY CASE constraint_info.contype WHEN 'p' THEN 0 ELSE 1 END, constraint_info.conname`, schema, table)
	if err != nil {
		return nil, DDLOutput{}, err
	}
	for _, constraint := range constraints {
		name, _ := constraint["name"].(string)
		definition, _ := constraint["definition"].(string)
		quotedName, err := sqlsafe.QuoteIdentifier(name)
		if err != nil {
			return nil, DDLOutput{}, err
		}
		colDefs = append(colDefs, "CONSTRAINT "+quotedName+" "+definition)
	}

	ddl := "CREATE TABLE " + qname + " (\n    " + strings.Join(colDefs, ",\n    ") + "\n);"
	return nil, DDLOutput{DDL: ddl}, nil
}

// ---- table_size ----

type TableSizeInput struct {
	Table string `json:"table,omitempty" jsonschema:"Exact table as table or schema.table; omit to list the largest user tables."`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum rows; defaults to 200 and is capped at 1000."`
}

func (r *Registry) tableSize(ctx context.Context, _ *mcp.CallToolRequest, in TableSizeInput) (*mcp.CallToolResult, RowsOutput, error) {
	ctx, cancel := r.client.TimeoutContext(ctx)
	defer cancel()
	var schema, table string
	var err error
	if strings.TrimSpace(in.Table) != "" {
		schema, table, err = splitTable(in.Table)
		if err != nil {
			return nil, RowsOutput{}, err
		}
	}
	rows, err := r.client.Query(ctx, `
SELECT n.nspname AS schema,
       c.relname AS table,
       pg_catalog.pg_size_pretty(pg_catalog.pg_total_relation_size(c.oid)) AS "totalSize",
       pg_catalog.pg_size_pretty(pg_catalog.pg_relation_size(c.oid)) AS "tableSize",
       pg_catalog.pg_size_pretty(pg_catalog.pg_indexes_size(c.oid)) AS "indexSize",
       pg_catalog.pg_size_pretty(
         pg_catalog.pg_total_relation_size(c.oid)
         - pg_catalog.pg_relation_size(c.oid)
         - pg_catalog.pg_indexes_size(c.oid)
       ) AS "toastSize",
       c.reltuples::bigint AS "estimatedRows"
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON c.relnamespace = n.oid
WHERE c.relkind IN ('r', 'm')
	  AND n.nspname NOT LIKE 'pg_%'
	  AND n.nspname != 'information_schema'
	  AND ($1 = '' OR (n.nspname = $1 AND c.relname = $2))
ORDER BY pg_catalog.pg_total_relation_size(c.oid) DESC
LIMIT $3`, schema, table, bounded(in.Limit, 200, 1000))
	return nil, RowsOutput{Rows: rows}, err
}

// ---- insert_data ----

type InsertInput struct {
	Table string           `json:"table" jsonschema:"Target table as table or schema.table."`
	Rows  []map[string]any `json:"rows" jsonschema:"One or more objects whose keys are column names. All rows are inserted atomically."`
}

type MutationOutput struct {
	Executed     bool             `json:"executed"`
	RowsAffected int64            `json:"rowsAffected"`
	Preview      []map[string]any `json:"preview,omitempty"`
}

func (r *Registry) insertData(ctx context.Context, _ *mcp.CallToolRequest, in InsertInput) (*mcp.CallToolResult, MutationOutput, error) {
	if !r.client.Config.AccessLevel.AllowsDML() {
		return nil, MutationOutput{}, fmt.Errorf("insert_data requires DML-RW or DDL-RW")
	}
	if len(in.Rows) == 0 {
		return nil, MutationOutput{}, fmt.Errorf("rows is required")
	}
	table, err := sqlsafe.QuoteMultipart(in.Table)
	if err != nil {
		return nil, MutationOutput{}, err
	}
	type command struct {
		query string
		args  []any
	}
	commands := make([]command, 0, len(in.Rows))
	for rowIndex, row := range in.Rows {
		cols := sortedKeys(row)
		if len(cols) == 0 {
			return nil, MutationOutput{}, fmt.Errorf("row %d cannot be empty", rowIndex+1)
		}
		qcols := make([]string, len(cols))
		params := make([]string, len(cols))
		args := make([]any, len(cols))
		for i, col := range cols {
			qcol, err := sqlsafe.QuoteIdentifier(col)
			if err != nil {
				return nil, MutationOutput{}, err
			}
			qcols[i] = qcol
			params[i] = fmt.Sprintf("$%d", i+1)
			args[i] = row[col]
		}
		commands = append(commands, command{
			query: fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(qcols, ", "), strings.Join(params, ", ")),
			args:  args,
		})
	}

	ctx, cancel := r.client.TimeoutContext(ctx)
	defer cancel()
	tx, err := r.client.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, MutationOutput{}, fmt.Errorf("begin insert transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()
	var total int64
	for rowIndex, command := range commands {
		result, err := tx.ExecContext(ctx, command.query, command.args...)
		if err != nil {
			return nil, MutationOutput{}, fmt.Errorf("insert row %d: %w", rowIndex+1, err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return nil, MutationOutput{}, fmt.Errorf("insert row %d: %w", rowIndex+1, err)
		}
		total += n
	}
	if err := tx.Commit(); err != nil {
		return nil, MutationOutput{}, fmt.Errorf("commit insert transaction: %w", err)
	}
	return nil, MutationOutput{Executed: true, RowsAffected: total}, nil
}

// ---- update_data / delete_data shared types ----

type WhereMutationInput struct {
	Table   string         `json:"table" jsonschema:"Target table as table or schema.table."`
	Values  map[string]any `json:"values" jsonschema:"Column names and replacement values."`
	Where   string         `json:"where" jsonschema:"Required SQL predicate using named placeholders such as id = $id."`
	Params  map[string]any `json:"params,omitempty" jsonschema:"Values for every named placeholder in where."`
	Confirm bool           `json:"confirm,omitempty" jsonschema:"Execute instead of previewing when confirmation is required."`
}

func (r *Registry) updateData(ctx context.Context, _ *mcp.CallToolRequest, in WhereMutationInput) (*mcp.CallToolResult, MutationOutput, error) {
	if !r.client.Config.AccessLevel.AllowsDML() {
		return nil, MutationOutput{}, fmt.Errorf("update_data requires DML-RW or DDL-RW")
	}
	if len(in.Values) == 0 {
		return nil, MutationOutput{}, fmt.Errorf("values is required")
	}
	cols := sortedKeys(in.Values)
	execute := !r.client.Config.RequireConfirmation || in.Confirm
	offset := 0
	if execute {
		offset = len(cols)
	}
	table, where, args, err := mutationTarget(in.Table, in.Where, in.Params, offset)
	if err != nil {
		return nil, MutationOutput{}, err
	}
	ctx, cancel := r.client.TimeoutContext(ctx)
	defer cancel()
	if !execute {
		preview, err := r.client.Query(ctx, fmt.Sprintf("SELECT * FROM %s WHERE %s\nLIMIT %d", table, where, r.client.Config.MaxRowsDefault), args...)
		return nil, MutationOutput{Executed: false, Preview: preview}, err
	}
	set := make([]string, len(cols))
	allArgs := make([]any, 0, len(cols)+len(args))
	for i, col := range cols {
		qcol, err := sqlsafe.QuoteIdentifier(col)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		set[i] = fmt.Sprintf("%s = $%d", qcol, i+1)
		allArgs = append(allArgs, in.Values[col])
	}
	allArgs = append(allArgs, args...)
	n, err := r.client.Exec(ctx, fmt.Sprintf("UPDATE %s SET %s WHERE %s", table, strings.Join(set, ", "), where), allArgs...)
	if err != nil {
		return nil, MutationOutput{}, err
	}
	return nil, MutationOutput{Executed: true, RowsAffected: n}, nil
}

// ---- delete_data ----

type DeleteInput struct {
	Table   string         `json:"table" jsonschema:"Target table as table or schema.table."`
	Where   string         `json:"where" jsonschema:"Required SQL predicate using named placeholders such as id = $id."`
	Params  map[string]any `json:"params,omitempty" jsonschema:"Values for every named placeholder in where."`
	Confirm bool           `json:"confirm,omitempty" jsonschema:"Execute instead of previewing when confirmation is required."`
}

func (r *Registry) deleteData(ctx context.Context, _ *mcp.CallToolRequest, in DeleteInput) (*mcp.CallToolResult, MutationOutput, error) {
	if !r.client.Config.AccessLevel.AllowsDML() {
		return nil, MutationOutput{}, fmt.Errorf("delete_data requires DML-RW or DDL-RW")
	}
	table, where, args, err := mutationTarget(in.Table, in.Where, in.Params, 0)
	if err != nil {
		return nil, MutationOutput{}, err
	}
	ctx, cancel := r.client.TimeoutContext(ctx)
	defer cancel()
	if r.client.Config.RequireConfirmation && !in.Confirm {
		preview, err := r.client.Query(ctx, fmt.Sprintf("SELECT * FROM %s WHERE %s\nLIMIT %d", table, where, r.client.Config.MaxRowsDefault), args...)
		return nil, MutationOutput{Executed: false, Preview: preview}, err
	}
	n, err := r.client.Exec(ctx, fmt.Sprintf("DELETE FROM %s WHERE %s", table, where), args...)
	if err != nil {
		return nil, MutationOutput{}, err
	}
	return nil, MutationOutput{Executed: true, RowsAffected: n}, nil
}

// ---- create_table ----

type ColumnDef struct {
	Name       string `json:"name" jsonschema:"Column name."`
	Type       string `json:"type" jsonschema:"PostgreSQL data type, optionally parameterized or schema-qualified."`
	Nullable   bool   `json:"nullable" jsonschema:"Whether the column accepts NULL; false emits NOT NULL."`
	PrimaryKey bool   `json:"primaryKey,omitempty" jsonschema:"Include the column in the primary key."`
	Identity   bool   `json:"identity,omitempty" jsonschema:"Generate values by default using a PostgreSQL identity."`
}

type CreateTableInput struct {
	Table   string      `json:"table" jsonschema:"New table name as table or schema.table."`
	Columns []ColumnDef `json:"columns" jsonschema:"One or more column definitions."`
}

func (r *Registry) createTable(ctx context.Context, _ *mcp.CallToolRequest, in CreateTableInput) (*mcp.CallToolResult, MutationOutput, error) {
	if !r.client.Config.AccessLevel.AllowsDDL() {
		return nil, MutationOutput{}, fmt.Errorf("create_table requires DDL-RW")
	}
	if len(in.Columns) == 0 {
		return nil, MutationOutput{}, fmt.Errorf("columns is required")
	}
	table, err := sqlsafe.QuoteMultipart(in.Table)
	if err != nil {
		return nil, MutationOutput{}, err
	}
	defs := make([]string, 0, len(in.Columns)+1)
	var pks []string
	seenColumns := make(map[string]bool, len(in.Columns))
	for _, col := range in.Columns {
		qcol, err := sqlsafe.QuoteIdentifier(col.Name)
		if err != nil {
			return nil, MutationOutput{}, err
		}
		if seenColumns[qcol] {
			return nil, MutationOutput{}, fmt.Errorf("duplicate column %q", col.Name)
		}
		seenColumns[qcol] = true
		if !validSQLType(col.Type) {
			return nil, MutationOutput{}, fmt.Errorf("invalid column type %q", col.Type)
		}
		def := qcol + " " + strings.TrimSpace(col.Type)
		if col.Identity {
			def += " GENERATED BY DEFAULT AS IDENTITY"
		}
		if col.Nullable {
			def += " NULL"
		} else {
			def += " NOT NULL"
		}
		defs = append(defs, def)
		if col.PrimaryKey {
			pks = append(pks, qcol)
		}
	}
	if len(pks) > 0 {
		defs = append(defs, "PRIMARY KEY ("+strings.Join(pks, ", ")+")")
	}
	ctx, cancel := r.client.TimeoutContext(ctx)
	defer cancel()
	n, err := r.client.Exec(ctx, fmt.Sprintf("CREATE TABLE %s (%s)", table, strings.Join(defs, ", ")))
	if err != nil {
		return nil, MutationOutput{}, err
	}
	return nil, MutationOutput{Executed: true, RowsAffected: n}, nil
}

// ---- create_index ----

type CreateIndexInput struct {
	Table   string   `json:"table" jsonschema:"Indexed table as table or schema.table."`
	Name    string   `json:"name" jsonschema:"New unqualified index name."`
	Columns []string `json:"columns" jsonschema:"One or more table column names in index order."`
	Unique  bool     `json:"unique,omitempty" jsonschema:"Create a unique index when true."`
}

func (r *Registry) createIndex(ctx context.Context, _ *mcp.CallToolRequest, in CreateIndexInput) (*mcp.CallToolResult, MutationOutput, error) {
	if !r.client.Config.AccessLevel.AllowsDDL() {
		return nil, MutationOutput{}, fmt.Errorf("create_index requires DDL-RW")
	}
	table, err := sqlsafe.QuoteMultipart(in.Table)
	if err != nil {
		return nil, MutationOutput{}, err
	}
	name, err := sqlsafe.QuoteIdentifier(in.Name)
	if err != nil {
		return nil, MutationOutput{}, err
	}
	if len(in.Columns) == 0 {
		return nil, MutationOutput{}, fmt.Errorf("columns is required")
	}
	cols := make([]string, len(in.Columns))
	for i, col := range in.Columns {
		cols[i], err = sqlsafe.QuoteIdentifier(col)
		if err != nil {
			return nil, MutationOutput{}, err
		}
	}
	prefix := "CREATE INDEX"
	if in.Unique {
		prefix = "CREATE UNIQUE INDEX"
	}
	ctx, cancel := r.client.TimeoutContext(ctx)
	defer cancel()
	n, err := r.client.Exec(ctx, fmt.Sprintf("%s %s ON %s (%s)", prefix, name, table, strings.Join(cols, ", ")))
	if err != nil {
		return nil, MutationOutput{}, err
	}
	return nil, MutationOutput{Executed: true, RowsAffected: n}, nil
}

// ---- drop_table ----

type DropTableInput struct {
	Table   string `json:"table" jsonschema:"Table to drop as table or schema.table."`
	Confirm bool   `json:"confirm,omitempty" jsonschema:"Execute instead of returning a no-op preview when confirmation is required."`
}

func (r *Registry) dropTable(ctx context.Context, _ *mcp.CallToolRequest, in DropTableInput) (*mcp.CallToolResult, MutationOutput, error) {
	if !r.client.Config.AccessLevel.AllowsDDL() {
		return nil, MutationOutput{}, fmt.Errorf("drop_table requires DDL-RW")
	}
	table, err := sqlsafe.QuoteMultipart(in.Table)
	if err != nil {
		return nil, MutationOutput{}, err
	}
	if r.client.Config.RequireConfirmation && !in.Confirm {
		ctx, cancel := r.client.TimeoutContext(ctx)
		defer cancel()
		preview, err := r.client.Query(ctx, `
SELECT schema_info.nspname AS schema,
	   table_info.relname AS table,
	   table_info.reltuples::bigint AS "estimatedRows",
	   pg_catalog.pg_size_pretty(pg_catalog.pg_total_relation_size(table_info.oid)) AS "totalSize"
FROM pg_catalog.pg_class table_info
JOIN pg_catalog.pg_namespace schema_info ON schema_info.oid = table_info.relnamespace
WHERE table_info.oid = pg_catalog.to_regclass($1)`, table)
		return nil, MutationOutput{Executed: false, Preview: preview}, err
	}
	ctx, cancel := r.client.TimeoutContext(ctx)
	defer cancel()
	n, err := r.client.Exec(ctx, "DROP TABLE "+table)
	if err != nil {
		return nil, MutationOutput{}, err
	}
	return nil, MutationOutput{Executed: true, RowsAffected: n}, nil
}

// ---- helpers ----

func bounded(value, fallback, maxValue int) int {
	if value <= 0 {
		return fallback
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func patternOrEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return sqlsafe.LikePattern(s)
}

func splitTable(name string) (string, string, error) {
	parts := strings.Split(strings.TrimSpace(name), ".")
	if len(parts) == 1 {
		if _, err := sqlsafe.QuoteIdentifier(parts[0]); err != nil {
			return "", "", err
		}
		return "public", parts[0], nil
	}
	if len(parts) == 2 {
		if _, err := sqlsafe.QuoteIdentifier(parts[0]); err != nil {
			return "", "", err
		}
		if _, err := sqlsafe.QuoteIdentifier(parts[1]); err != nil {
			return "", "", err
		}
		return parts[0], parts[1], nil
	}
	return "", "", fmt.Errorf("table must be table or schema.table")
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// validSQLType allows PostgreSQL type forms:
// - simple: integer, text, boolean, uuid, jsonb, bigint, etc.
// - parameterized: varchar(255), numeric(10,2), char(3)
// - with time zone: timestamp with time zone, time without time zone
// - arrays: text[], integer[], numeric(10,2)[]
// - schema-qualified: public.my_enum
var sqlTypeRE = regexp.MustCompile(`(?i)^(?:[a-z_][a-z0-9_]*\.)?[a-z][a-z0-9_]*(?:\s+(?:WITH|WITHOUT)\s+TIME\s+ZONE|\s+PRECISION)?(?:\([0-9]+(?:,[0-9]+)?\))?(\[\])?$`)

func validSQLType(s string) bool {
	return sqlTypeRE.MatchString(strings.TrimSpace(s))
}

func mutationTarget(tableName, where string, params map[string]any, parameterOffset int) (string, string, []any, error) {
	table, err := sqlsafe.QuoteMultipart(tableName)
	if err != nil {
		return "", "", nil, err
	}
	where = strings.TrimSpace(where)
	if where == "" {
		return "", "", nil, fmt.Errorf("where is required")
	}
	if !sqlsafe.IsReadOnlyQuery("SELECT * FROM x WHERE (\n" + where + "\n)") {
		return "", "", nil, fmt.Errorf("where clause contains disallowed SQL")
	}
	where, args, err := sqlsafe.BindNamedParameters(where, params, parameterOffset)
	if err != nil {
		return "", "", nil, err
	}
	return table, "(\n" + where + "\n)", args, nil
}

//go:fix inline
func boolPointer(value bool) *bool {
	return new(value)
}
