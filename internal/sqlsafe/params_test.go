package sqlsafe

import (
	"reflect"
	"strings"
	"testing"
)

func TestBindNamedParameters(t *testing.T) {
	query := `id = $id AND parent_id = $id2 AND owner_id = $id AND note = '$id2' -- $ignored`
	gotQuery, gotArgs, err := BindNamedParameters(query, map[string]any{"id": 7, "id2": 9}, 3)
	if err != nil {
		t.Fatal(err)
	}
	wantQuery := `id = $4 AND parent_id = $5 AND owner_id = $4 AND note = '$id2' -- $ignored`
	if gotQuery != wantQuery {
		t.Fatalf("query = %q, want %q", gotQuery, wantQuery)
	}
	if !reflect.DeepEqual(gotArgs, []any{7, 9}) {
		t.Fatalf("args = %#v", gotArgs)
	}
}

func TestBindNamedParametersSkipsQuotedAndCommentedText(t *testing.T) {
	query := `payload = $$ $not_a_parameter $$ AND "column$id" = $value /* $ignored */`
	got, args, err := BindNamedParameters(query, map[string]any{"value": "ok"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := `payload = $$ $not_a_parameter $$ AND "column$id" = $1 /* $ignored */`
	if got != want || !reflect.DeepEqual(args, []any{"ok"}) {
		t.Fatalf("got query %q, args %#v", got, args)
	}
}

func TestBindNamedParametersRejectsInvalidBindings(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		params map[string]any
		want   string
	}{
		{name: "missing", query: "id = $id", want: "missing value"},
		{name: "unused", query: "id = 1", params: map[string]any{"id": 1}, want: "unused named parameters"},
		{name: "positional", query: "id = $1", want: "positional placeholder"},
		{name: "unterminated quote", query: "note = 'oops", want: "unterminated"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := BindNamedParameters(test.query, test.params, 0)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want text %q", err, test.want)
			}
		})
	}
}
