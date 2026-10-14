package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSQLPreviewExamples(t *testing.T) {
	examples, err := sqlExamples()
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) != 5 {
		t.Fatalf("got %d examples", len(examples))
	}
	for _, example := range examples {
		t.Run(example.ID, func(t *testing.T) {
			request, err := json.Marshal(map[string]any{"mode": "sql", "query": example.Query,
				"configuration": json.RawMessage(example.Configuration)})
			if err != nil {
				t.Fatal(err)
			}
			result := run(context.Background(), request)
			if !result.OK || result.Error != nil || result.SQL == nil || result.SQL.Where == "" {
				t.Fatalf("SQL preview failed: %+v", result)
			}
			if result.SQL.Timings.Compile < 0 || result.SQL.Timings.Build < 0 {
				t.Fatal("negative timing")
			}
		})
	}
}

func TestSQLPreviewRejectsInvalidConfiguration(t *testing.T) {
	for _, request := range []string{
		`{"mode":"sql","query":"age>18"}`,
		`{"mode":"sql","query":"age>18","configuration":{},"records":[]}`,
		`{"mode":"sql","query":"age>18","configuration":{"version":1}}`,
	} {
		result := run(context.Background(), []byte(request))
		if result.OK || result.SQL != nil || result.Error == nil {
			t.Fatalf("accepted invalid SQL request: %s", request)
		}
	}
}

func TestSQLPreviewPreservesLargeIntArgument(t *testing.T) {
	examples, err := sqlExamples()
	if err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(map[string]any{"mode": "sql", "query": "age=9007199254740993",
		"configuration": json.RawMessage(examples[0].Configuration)})
	if err != nil {
		t.Fatal(err)
	}
	result := run(context.Background(), request)
	if !result.OK || result.SQL == nil || len(result.SQL.Args) != 1 ||
		result.SQL.Args[0].Type != "int64" || result.SQL.Args[0].Value != "9007199254740993" ||
		!strings.Contains(result.SQL.Where, "$1") {
		t.Fatalf("large integer argument changed: %+v", result)
	}
}
