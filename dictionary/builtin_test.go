package dictionary

import (
	"testing"
	"time"
)

// TestBuiltinRowsAreStable checks the predefined catalogue. | TestBuiltinRowsAreStable 校验预置目录。
func TestBuiltinRowsAreStable(t *testing.T) {
	types, items := BuiltinRows("tn_a", time.Unix(0, 0).UTC())
	if len(types) != len(BuiltinCatalog()) || len(items) != 13 {
		t.Fatalf("types=%d items=%d, want %d and 13", len(types), len(items), len(BuiltinCatalog()))
	}
	secondTypes, secondItems := BuiltinRows("tn_a", time.Unix(0, 0).UTC())
	for i := range types {
		if types[i].ID != secondTypes[i].ID {
			t.Fatalf("builtin type id is not stable: %q vs %q", types[i].ID, secondTypes[i].ID)
		}
	}
	for i := range items {
		if items[i].ID != secondItems[i].ID {
			t.Fatalf("builtin item id is not stable: %q vs %q", items[i].ID, secondItems[i].ID)
		}
	}
	for _, dictionaryType := range types {
		if !dictionaryType.Builtin || dictionaryType.TenantID != "tn_a" {
			t.Fatalf("bad builtin type %+v", dictionaryType)
		}
		if !codeRe.MatchString(dictionaryType.Code) {
			t.Fatalf("bad builtin code %q", dictionaryType.Code)
		}
	}
	for _, item := range items {
		if item.TenantID != "tn_a" || !valueRe.MatchString(item.Value) {
			t.Fatalf("bad builtin item %+v", item)
		}
	}
}
