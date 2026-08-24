package recipe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validRecipe = `version: 1
recipes:
  - app: example-app
    files:
      - target: data/required.bin
        accepted:
          - size: 4
            sha256: 3a6eb0790f39ac87c94f3856b2dd2c5d110e6811602261a9a923d3bb23adc8b7
`

func TestParseValidRecipe(t *testing.T) {
	catalog, err := parse([]byte(validRecipe), "synthetic")
	if err != nil || len(catalog.Recipes) != 1 || catalog.Recipes[0].Files[0].Target != "data/required.bin" {
		t.Fatalf("parse valid recipe: %#v %v", catalog, err)
	}
}

func TestRecipeRejectsHostileDefinitions(t *testing.T) {
	tests := map[string]string{
		"uppercase sha":    strings.Replace(validRecipe, "3a6eb", "3A6eb", 1),
		"zero size":        strings.Replace(validRecipe, "size: 4", "size: 0", 1),
		"empty accepted":   strings.Replace(validRecipe, "          - size: 4\n            sha256: 3a6eb0790f39ac87c94f3856b2dd2c5d110e6811602261a9a923d3bb23adc8b7\n", "", 1),
		"absolute target":  strings.Replace(validRecipe, "data/required.bin", "/tmp/escape", 1),
		"dot target":       strings.Replace(validRecipe, "data/required.bin", "data/../required.bin", 1),
		"backslash target": strings.Replace(validRecipe, "data/required.bin", `data\\required.bin`, 1),
		"unknown field":    strings.Replace(validRecipe, "version: 1", "version: 1\nunknown: true", 1),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parse([]byte(input), name); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestRecipeRejectsDuplicateDefinitions(t *testing.T) {
	duplicateApp := validRecipe + "\n  - app: example-app\n    files:\n      - target: other.bin\n        accepted:\n          - size: 4\n            sha256: 3a6eb0790f39ac87c94f3856b2dd2c5d110e6811602261a9a923d3bb23adc8b7\n"
	if _, err := parse([]byte(duplicateApp), "duplicate-app"); err == nil {
		t.Fatal("expected duplicate app rejection")
	}
	duplicateTarget := strings.Replace(validRecipe, "      - target: data/required.bin", "      - target: data/required.bin\n      - target: data/required.bin", 1)
	if _, err := parse([]byte(duplicateTarget), "duplicate-target"); err == nil {
		t.Fatal("expected duplicate target rejection")
	}
}

func TestRecipeDirectorySkipsSymlinkFilesAndRejectsDuplicateAcrossSources(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "one.yaml")
	if err := os.WriteFile(file, []byte(validRecipe), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, filepath.Join(dir, "linked.yaml")); err == nil {
		recipes, err := LoadAll([]string{dir}, nil)
		if err != nil || len(recipes) != 1 {
			t.Fatalf("symlink handling: %v", err)
		}
	}
	other := filepath.Join(t.TempDir(), "two.yaml")
	if err := os.WriteFile(other, []byte(validRecipe), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAll([]string{file, other}, nil); err == nil {
		t.Fatal("expected duplicate app across configured sources")
	}
}

func TestRecipeRejectsOversizedLocalFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "large.yaml")
	if err := os.WriteFile(file, make([]byte, maxCatalogBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAll([]string{file}, nil); err == nil {
		t.Fatal("expected oversized recipe rejection")
	}
}
