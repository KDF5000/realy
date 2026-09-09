package controlplane_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/KDF5000/relay/controlplane"
)

func TestFileBlobStoreRoundTripAndKeyValidation(t *testing.T) {
	store, err := controlplane.NewFileBlobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if size, err := store.Put(context.Background(), "artifact-1", bytes.NewBufferString("hello")); err != nil || size != 5 {
		t.Fatalf("put = %d, %v", size, err)
	}
	reader, err := store.Open(context.Background(), "artifact-1")
	if err != nil {
		t.Fatal(err)
	}
	value, _ := io.ReadAll(reader)
	reader.Close()
	if string(value) != "hello" {
		t.Fatalf("value = %q", value)
	}
	if _, err := store.Put(context.Background(), "../escape", bytes.NewReader(nil)); err == nil {
		t.Fatal("expected unsafe key rejection")
	}
}
