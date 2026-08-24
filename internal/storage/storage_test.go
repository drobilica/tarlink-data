package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drobilica/tarlink-data/internal/recipe"
)

func testVariant() recipe.Variant {
	return recipe.Variant{Size: 7, SHA256: "239f59ed55e737c77147cf55ad0c1b030e5b7c5b6f5e8f5f5c7b5a6c4d3e2f1a"}
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func TestMaterializeAndDestinationStates(t *testing.T) {
	// The expected digest is generated from the same synthetic payload below.
	payload := []byte("payload")
	variant := testVariant()
	variant.SHA256 = sha256Hex(payload)
	data := t.TempDir()
	if err := Materialize(data, "example-app", "required-data.bin", variant, func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(payload)), nil
	}, false); err != nil {
		t.Fatal(err)
	}
	state, err := Inspect(data, "example-app", "required-data.bin", []recipe.Variant{variant})
	if err != nil || state.State != Satisfied {
		t.Fatalf("satisfied state: %#v %v", state, err)
	}
	original, _ := os.ReadFile(filepath.Join(AppRoot(data, "example-app"), "required-data.bin"))
	wrong := variant
	wrong.SHA256 = strings.Repeat("0", 64)
	if _, err := Inspect(data, "example-app", "required-data.bin", []recipe.Variant{wrong}); err == nil {
		t.Fatal("expected conflict")
	}
	current, _ := os.ReadFile(filepath.Join(AppRoot(data, "example-app"), "required-data.bin"))
	if string(original) != string(current) {
		t.Fatal("conflict changed destination")
	}
}

func TestDryRunAndFailedCopyDoNotPublish(t *testing.T) {
	payload := []byte("payload")
	variant := testVariant()
	variant.SHA256 = sha256Hex(payload)
	data := t.TempDir()
	if err := Materialize(data, "example-app", "nested/data.bin", variant, func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(payload)), nil
	}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(AppRoot(data, "example-app")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run created data: %v", err)
	}
	wrong := variant
	wrong.SHA256 = strings.Repeat("0", 64)
	if err := Materialize(data, "example-app", "nested/data.bin", wrong, func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(payload)), nil
	}, false); !errors.Is(err, ErrMismatch) {
		t.Fatalf("expected mismatch, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(AppRoot(data, "example-app"), "nested", "data.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed copy published destination: %v", err)
	}
}

func TestSymlinkParentRejected(t *testing.T) {
	data := t.TempDir()
	root := AppRoot(data, "example-app")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	escape := t.TempDir()
	if err := os.Symlink(escape, filepath.Join(root, "nested")); err != nil {
		t.Skip("symlinks unavailable")
	}
	variant := testVariant()
	if err := Materialize(data, "example-app", "nested/data.bin", variant, func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("payload")), nil
	}, false); err == nil {
		t.Fatal("expected symlink parent rejection")
	}
	if _, err := os.Stat(filepath.Join(escape, "data.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("escaped destination was created")
	}
}

func TestManagedAppsSymlinkRejected(t *testing.T) {
	data := t.TempDir()
	escape := t.TempDir()
	if err := os.Symlink(escape, filepath.Join(data, "apps")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := Inspect(data, "example-app", "data.bin", nil); err == nil {
		t.Fatal("expected managed apps symlink rejection")
	}
}
