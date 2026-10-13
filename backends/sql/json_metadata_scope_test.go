package sql

import (
	"testing"

	"github.com/ornew/hff"
)

func TestMetadataReferencesRetainOuterCallbackScope(t *testing.T) {
	outerItem := BindStruct(hff.Path{}, Boolean(true), Boolean(false),
		BindJSON(hff.Path{"payload"}, Column("outer", "data"), JSONB,
			WithTypeMetadata(Column("metadata", "types"))),
	)
	b := builder{physicalParameters: map[hff.BindingID]Binding{1: outerItem}}
	if !b.shadowsOuterBinding("metadata") {
		t.Fatal("inner source would shadow outer callback metadata")
	}
	if !b.shadowsOuterBinding("outer") {
		t.Fatal("inner source would shadow outer callback data")
	}
	if b.shadowsOuterBinding("unrelated") {
		t.Fatal("unrelated alias should retain direct relation lowering")
	}
}

func TestTimestampRemainderRetainsOuterCallbackScope(t *testing.T) {
	outer := BindTimestamp(hff.Path{"created"}, Column("events", "created_at"),
		WithNanosecondRemainder(Column("r", "created_ns_remainder")))
	b := builder{physicalBindings: []Binding{outer}}
	if !b.shadowsOuterBinding("r") {
		t.Fatal("relation alias would shadow the outer Timestamp remainder")
	}
	if b.shadowsOuterBinding("unrelated") {
		t.Fatal("unrelated alias should retain direct relation lowering")
	}
}
