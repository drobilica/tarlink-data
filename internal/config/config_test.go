package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigStrictAndExpandsTilde(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(file, []byte("version: 1\nrecipes: [~/recipes]\nsources: [/tmp/data]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(file)
	if err != nil || cfg.Version != 1 || len(cfg.Recipes) != 1 {
		t.Fatalf("load config: %#v %v", cfg, err)
	}
	if expanded, err := ExpandPath(cfg.Recipes[0]); err != nil || expanded == cfg.Recipes[0] {
		t.Fatalf("tilde expansion: %q %v", expanded, err)
	}
}

func TestLoadConfigRejectsVersionUnknownAndRelativeXDG(t *testing.T) {
	dir := t.TempDir()
	for name, contents := range map[string]string{
		"version": "version: 2\n",
		"unknown": "version: 1\nextra: true\n",
	} {
		file := filepath.Join(dir, name+".yaml")
		if err := os.WriteFile(file, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(file); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", "relative")
	if _, err := XDGPaths(); err == nil {
		t.Fatal("expected relative XDG path rejection")
	}
}

func TestLoadConfigRejectsOversizedFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(file, make([]byte, maxConfigBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(file); err == nil {
		t.Fatal("expected oversized config rejection")
	}
}
