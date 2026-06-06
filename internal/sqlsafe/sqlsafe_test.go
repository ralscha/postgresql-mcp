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
	// Single part
	got, err = QuoteMultipart("Users")
	if err != nil {
		t.Fatal(err)
	}
	if got != `"Users"` {
		t.Fatalf("got %q", got)
	}
	bad := []string{"", "public.", "public.Users;DROP", "public.\"Users\"", "a.b.c", "with space"}
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
	}
	for _, q := range yes {
		if !IsReadOnlyQuery(q) {
			t.Fatalf("expected read-only: %q", q)
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
	}
	for _, q := range no {
		if IsReadOnlyQuery(q) {
			t.Fatalf("expected not read-only: %q", q)
		}
	}
}

func TestRowLimit(t *testing.T) {
	if !NeedsRowLimit("SELECT * FROM t") {
		t.Fatal("plain select should need row limit")
	}
	if NeedsRowLimit("SELECT * FROM t LIMIT 10") {
		t.Fatal("LIMIT select should not need row limit")
	}
	if NeedsRowLimit("SELECT * FROM t FETCH FIRST 10 ROWS ONLY") {
		t.Fatal("FETCH FIRST select should not need row limit")
	}
	if NeedsRowLimit("SELECT * FROM t FETCH NEXT 50 ROW ONLY") {
		t.Fatal("FETCH NEXT select should not need row limit")
	}
}

func TestAppendLimit(t *testing.T) {
	got := AppendLimit("SELECT * FROM t", 25)
	want := "SELECT * FROM t LIMIT 25"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// Strips trailing semicolon
	got = AppendLimit("SELECT * FROM t;", 10)
	want = "SELECT * FROM t LIMIT 10"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLikePattern(t *testing.T) {
	got := LikePattern("acme")
	if len(got) != 6 || got[0] != '%' || got[5] != '%' {
		t.Fatalf("got %q (len=%d, bytes=%v)", got, len(got), []byte(got))
	}
	// * is a wildcard that maps to % — no additional wrapping
	got = LikePattern("*acme")
	if got != "%acme" {
		t.Fatalf("got %q", got)
	}
	// Already has % so no wrapping
	got = LikePattern("acme%")
	if len(got) < 5 {
		t.Fatalf("got %q (len=%d, bytes=%v)", got, len(got), []byte(got))
	}
	// "acme%" should become "acme\%" — has wildcard, so no wrapping
	if got[len(got)-1] != '%' {
		t.Fatalf("expected trailing %%, got %q (bytes=%v)", got, []byte(got))
	}
}
