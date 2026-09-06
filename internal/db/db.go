package db

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"postgresql-mcp/internal/config"
	"postgresql-mcp/internal/sqlsafe"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type Client struct {
	DB     *sql.DB
	Config config.Config
}

func Open(cfg config.Config) (*Client, error) {
	db, err := sql.Open("pgx", cfg.ConnectionString())
	if err != nil {
		return nil, err
	}
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetMaxIdleConns(2)
	db.SetMaxOpenConns(10)
	return &Client{DB: db, Config: cfg}, nil
}

func (c *Client) Close() error {
	if c == nil || c.DB == nil {
		return nil
	}
	return c.DB.Close()
}

func (c *Client) TimeoutContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.Config.QueryTimeout)
}

func (c *Client) Query(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	rows, err := c.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	return ScanRows(rows)
}

func (c *Client) QueryReadOnly(ctx context.Context, query string, maxRows int) ([]map[string]any, error) {
	if !sqlsafe.IsReadOnlyQuery(query) {
		return nil, fmt.Errorf("only read-only SELECT queries are allowed")
	}
	if maxRows <= 0 || maxRows > c.Config.MaxRowsDefault {
		maxRows = c.Config.MaxRowsDefault
	}
	return c.queryInReadOnlyTransaction(ctx, sqlsafe.AppendLimit(query, maxRows))
}

func (c *Client) ExplainReadOnly(ctx context.Context, query string) ([]map[string]any, error) {
	if !sqlsafe.IsReadOnlyQuery(query) {
		return nil, fmt.Errorf("only read-only SELECT queries can be explained")
	}
	return c.queryInReadOnlyTransaction(ctx, "EXPLAIN (FORMAT JSON) "+query)
}

func (c *Client) queryInReadOnlyTransaction(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	tx, err := c.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin read-only transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	result, err := ScanRows(rows)
	closeErr := rows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit read-only transaction: %w", err)
	}
	return result, nil
}

func (c *Client) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	res, err := c.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, nil
}

func ScanRows(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	cols = uniqueColumnNames(cols)
	out := []map[string]any{}
	for rows.Next() {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = normalize(raw[i], columnTypes[i].DatabaseTypeName())
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func normalize(v any, databaseType string) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		switch strings.ToUpper(databaseType) {
		case "BYTEA":
			return base64.StdEncoding.EncodeToString(x)
		case "JSON", "JSONB":
			var decoded any
			if json.Unmarshal(x, &decoded) == nil {
				return decoded
			}
		}
		return string(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case [16]uint8: // UUID from pgx
		return fmtUUID(x[:])
	default:
		return x
	}
}

func uniqueColumnNames(columns []string) []string {
	names := make([]string, len(columns))
	used := make(map[string]bool, len(columns))
	original := make(map[string]bool, len(columns))
	next := make(map[string]int, len(columns))
	for _, column := range columns {
		original[column] = true
	}
	for i, column := range columns {
		name := column
		if used[name] {
			if next[column] < 2 {
				next[column] = 2
			}
			name = fmt.Sprintf("%s_%d", column, next[column])
			for used[name] || original[name] {
				next[column]++
				name = fmt.Sprintf("%s_%d", column, next[column])
			}
			next[column]++
		}
		names[i] = name
		used[name] = true
	}
	return names
}

func fmtUUID(b []byte) string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
