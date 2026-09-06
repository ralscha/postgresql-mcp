package db

import (
	"reflect"
	"testing"
	"time"
)

func TestUniqueColumnNames(t *testing.T) {
	got := uniqueColumnNames([]string{"id", "name", "id", "id", "id_2", "id_2"})
	want := []string{"id", "name", "id_3", "id_4", "id_2", "id_2_2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("uniqueColumnNames() = %#v, want %#v", got, want)
	}
}

func TestNormalize(t *testing.T) {
	timestamp := time.Date(2026, time.September, 6, 12, 34, 56, 123, time.UTC)
	tests := []struct {
		name         string
		value        any
		databaseType string
		want         any
	}{
		{name: "text", value: []byte("hello"), databaseType: "TEXT", want: "hello"},
		{name: "binary", value: []byte{0xff, 0x00}, databaseType: "BYTEA", want: "/wA="},
		{name: "json", value: []byte(`{"ok":true,"count":2}`), databaseType: "JSONB", want: map[string]any{"ok": true, "count": float64(2)}},
		{name: "invalid json", value: []byte("{"), databaseType: "JSON", want: "{"},
		{name: "timestamp", value: timestamp, databaseType: "TIMESTAMPTZ", want: timestamp.Format(time.RFC3339Nano)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalize(test.value, test.databaseType); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("normalize() = %#v, want %#v", got, test.want)
			}
		})
	}
}
