package sqlsafe

import "testing"

func TestQuoteMultipart(t *testing.T) {
	got, err := QuoteMultipart("public.Users")
	if err != nil {
		t.Fatal(err)
	}
	if got != `"public"."Users"` {
		t.Fatalf("got %q", got)
	}

	got, err = QuoteMultipart("Users")
	if err != nil {
		t.Fatal(err)
	}
	if got != `"Users"` {
		t.Fatalf("got %q", got)
	}

	bad := []string{"", "public.", "public.Users;DROP", `public."Users"`, "a.b.c", "with space"}
	for _, name := range bad {
		if _, err := QuoteMultipart(name); err == nil {
			t.Fatalf("QuoteMultipart(%q) expected error", name)
		}
	}
}

func TestIsReadOnlyQuery(t *testing.T) {
	yes := []string{
		"SELECT * FROM public.Users",
		"WITH cte AS (SELECT 1 AS n) SELECT * FROM cte",
		"-- comment\nSELECT 1",
		"/* comment */ SELECT 1",
		"SELECT 'UPDATE t SET value = 1; DROP TABLE t' AS example",
		`SELECT "DELETE", $$ INSERT; UPDATE $$`,
		"SELECT 1; -- a trailing comment is part of the same statement",
		"/* nested /* block */ comment */ SELECT 1",
	}
	for _, query := range yes {
		if !IsReadOnlyQuery(query) {
			t.Errorf("expected read-only: %q", query)
		}
	}

	no := []string{
		"INSERT INTO t VALUES (1)",
		"UPDATE t SET a = 1",
		"DELETE FROM t",
		"MERGE INTO t USING s ON 1=1 WHEN MATCHED THEN UPDATE SET a=1",
		"CREATE TABLE x (id int)",
		"ALTER TABLE x ADD y int",
		"DROP TABLE x",
		"TRUNCATE TABLE x",
		"COPY t FROM '/tmp/data.csv'",
		"VACUUM t",
		"REINDEX TABLE t",
		"DO $$ BEGIN NULL; END $$",
		"SELECT * FROM t; DROP TABLE t",
		"SELECT 1; SELECT 2",
		"WITH changed AS (DELETE FROM t RETURNING *) SELECT * FROM changed",
		"SELECT 'unterminated",
		"/* unterminated SELECT 1",
	}
	for _, query := range no {
		if IsReadOnlyQuery(query) {
			t.Errorf("expected not read-only: %q", query)
		}
	}
}

func TestAppendLimit(t *testing.T) {
	got := AppendLimit("SELECT * FROM t", 25)
	want := "SELECT * FROM (\nSELECT * FROM t\n) AS \"_postgresql_mcp_result\" LIMIT 25"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	// The outer limit is authoritative even if the query already has a larger one.
	got = AppendLimit("SELECT * FROM t LIMIT 10000; -- trailing", 10)
	want = "SELECT * FROM (\nSELECT * FROM t LIMIT 10000\n) AS \"_postgresql_mcp_result\" LIMIT 10"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLikePattern(t *testing.T) {
	tests := map[string]string{
		"acme":  "%acme%",
		"*acme": "%acme",
		"acme*": "acme%",
		"acme%": `%acme\%%`,
		"a_b":   `%a\_b%`,
		`a\b`:   `%a\\b%`,
	}
	for input, want := range tests {
		if got := LikePattern(input); got != want {
			t.Errorf("LikePattern(%q) = %q, want %q", input, got, want)
		}
	}
}
