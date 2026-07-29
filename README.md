# postgresql-mcp

An [MCP (Model Context Protocol)](https://modelcontextprotocol.io/) server that gives AI assistants structured, read-optimized access to PostgreSQL databases. It exposes schema exploration, data profiling, relationship analysis, query explanation, and safe read/write operations through a standardized MCP interface.

## Features

- **24 MCP tools** covering schema search, table description, data profiling, relationship/dependency inspection, query explanation, connection testing, schema/extension/view/trigger listing, CREATE TABLE DDL generation, table sizing, and tiered write access
- **Tiered access model** via `POSTGRESQL_ACCESS_LEVEL`: `READONLY` (default), `DML-RW` (adds insert/update/delete), `DDL-RW` (adds create/drop table/index)
- **SQL-safe design** with double-quote identifier quoting, multipart name validation, and read-only query enforcement
- **Read-only query guard** that rejects mutating statements (`INSERT`, `UPDATE`, `DELETE`, `CREATE`, `ALTER`, `DROP`, `TRUNCATE`, `COPY`, `VACUUM`, etc.) on the `read_data` tool
- **Mutation confirmation** with preview mode that shows affected rows before executing writes when `POSTGRESQL_REQUIRE_CONFIRMATION` is enabled
- **Explain plan** via `EXPLAIN (FORMAT JSON)` for understanding query performance
- **Connection testing** that validates connectivity and reports latency
- **Environment listing** showing current host, database, and access-level configuration

## Installation

Download the latest release for your platform from the [Releases](https://github.com/ralscha/postgresql-mcp/releases) page.

## Usage

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `POSTGRESQL_HOST` | Yes | — | PostgreSQL hostname or IP |
| `POSTGRESQL_DATABASE` | Yes | — | Database name |
| `POSTGRESQL_USER` | Yes | — | Login username |
| `POSTGRESQL_PASSWORD` | Yes | — | Login password |
| `POSTGRESQL_PORT` | No | `5432` | PostgreSQL port |
| `POSTGRESQL_SSLMODE` | No | `prefer` | SSL mode: `disable`, `allow`, `prefer`, `require`, `verify-ca`, `verify-full` |
| `POSTGRESQL_ACCESS_LEVEL` | No | `READONLY` | `READONLY`, `DML-RW`, or `DDL-RW` |
| `POSTGRESQL_CONNECTION_TIMEOUT` | No | `30` | Connection timeout in seconds |
| `POSTGRESQL_QUERY_TIMEOUT` | No | `120` | Query timeout in seconds |
| `POSTGRESQL_MAX_ROWS_DEFAULT` | No | `1000` | Default row limit for queries |
| `POSTGRESQL_REQUIRE_CONFIRMATION` | No | `true` | Require confirm flag for writes |
| `POSTGRESQL_TRANSPORT` | No | `stdio` | MCP transport: `stdio` or `http` |
| `POSTGRESQL_HTTP_ADDR` | No | `:8080` | HTTP listen address when `POSTGRESQL_TRANSPORT=http` |
| `POSTGRESQL_HTTP_PATH` | No | `/mcp` | Stateless Streamable HTTP endpoint path when `POSTGRESQL_TRANSPORT=http` |

### Running as an MCP Server

By default, the server communicates over stdio. Configure your MCP client to launch it:

```json
{
  "mcpServers": {
    "postgresql": {
      "command": "/path/to/bin/postgresql-mcp",
      "env": {
        "POSTGRESQL_HOST": "localhost",
        "POSTGRESQL_DATABASE": "YourDatabase",
        "POSTGRESQL_USER": "postgres",
        "POSTGRESQL_PASSWORD": "YourPassword",
        "POSTGRESQL_SSLMODE": "disable",
        "POSTGRESQL_ACCESS_LEVEL": "READONLY"
      }
    }
  }
}
```

To serve MCP using the stateless Streamable HTTP transport, set `POSTGRESQL_TRANSPORT=http`:

```bash
POSTGRESQL_TRANSPORT=http \
POSTGRESQL_HTTP_ADDR=:8080 \
POSTGRESQL_HTTP_PATH=/mcp \
/path/to/bin/postgresql-mcp
```

Then configure a Streamable HTTP MCP client to connect to:

```text
http://localhost:8080/mcp
```

### Access Levels

- **`READONLY`** (default) — Schema exploration, data reading, profiling, relationship inspection, query explanation, connection testing, schema listing, extensions, views, triggers, CREATE TABLE DDL generation, table sizing. 18 tools.
- **`DML-RW`** — All read-only tools plus `insert_data`, `update_data`, `delete_data`. 21 tools.
- **`DDL-RW`** — All DML tools plus `create_table`, `create_index`, `drop_table`. 24 tools.

Mutations (`update_data`, `delete_data`, `drop_table`) require a `"confirm": true` flag when `POSTGRESQL_REQUIRE_CONFIRMATION` is enabled (the default). Without confirmation, the server returns a preview of the affected rows instead.

## MCP Tools

### Read-Only (READONLY)

| Tool | Description |
|------|-------------|
| `search_schema` | Search tables and columns by name pattern with pagination |
| `describe_table` | Get columns, primary keys, foreign keys, and indexes for a table |
| `list_table` | List tables, optionally filtered by schema and name |
| `list_databases` | List all databases on the server |
| `list_environments` | Show current connection and access-level configuration |
| `profile_table` | Row count, null counts, distinct counts, min/max per column, with optional data samples |
| `inspect_relationships` | List foreign keys going out of and into a table |
| `inspect_dependencies` | Find objects (views, functions) that depend on a table |
| `explain_query` | Get the JSON execution plan for a read-only query |
| `read_data` | Execute a read-only SELECT query with row limits |
| `test_connection` | Ping the server and return latency and server version info |
| `validate_environment_config` | Validate that all environment variables are correctly configured |
| `list_schemas` | List all user schemas with owner and description |
| `list_extensions` | List installed PostgreSQL extensions with version |
| `list_views` | List views with their SQL definitions |
| `list_triggers` | List triggers with event, timing, and definition |
| `show_create_table` | Generate the CREATE TABLE DDL for an existing table |
| `table_size` | Table and index sizes, toast size, estimated row counts |

### DML (DML-RW)

| Tool | Description |
|------|-------------|
| `insert_data` | Insert one or more rows into a table |
| `update_data` | Update rows matching a WHERE clause (with optional preview) |
| `delete_data` | Delete rows matching a WHERE clause (with optional preview) |

### DDL (DDL-RW)

| Tool | Description |
|------|-------------|
| `create_table` | Create a table with column definitions, primary keys, and identity columns |
| `create_index` | Create a standard or unique index on specified columns |
| `drop_table` | Drop a table (with optional preview/confirmation) |

## License

MIT License. See [LICENSE](LICENSE) for details.
