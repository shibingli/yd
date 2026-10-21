package filestore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/shibingli/yd/cms/filestore"
)

func TestStoreRoundTripAndTraversal(t *testing.T) {
	store, err := filestore.New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	if err := store.Put(ctx, "tn_a/med_1", []byte("payload")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	data, err := store.Get(ctx, "tn_a/med_1")
	if err != nil || string(data) != "payload" {
		t.Fatalf("Get = %q, %v", data, err)
	}
	if err := store.Delete(ctx, "tn_a/med_1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := store.Delete(ctx, "tn_a/med_1"); err != nil {
		t.Fatalf("idempotent Delete: %v", err)
	}
	for _, key := range []string{"../escape", "tn/../../escape", "/absolute", "tn//empty", ""} {
		if err := store.Put(ctx, key, []byte("x")); !errors.Is(err, filestore.ErrKeyInvalid) {
			t.Errorf("Put(%q) error = %v, want ErrKeyInvalid", key, err)
		}
	}
}

func TestNewRejectsEmptyRoot(t *testing.T) {
	if _, err := filestore.New("  "); !errors.Is(err, filestore.ErrRootInvalid) {
		t.Fatalf("New(empty) error = %v, want ErrRootInvalid", err)
	}
}
