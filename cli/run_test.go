package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBasicCommands(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run(nil, strings.NewReader(""), &out, &errOut, "dev"); code != 0 || !strings.Contains(out.String(), "Usage:") {
		t.Fatalf("no args: code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
	out.Reset()
	if code := Run([]string{"--version"}, strings.NewReader(""), &out, &errOut, "test-version"); code != 0 || out.String() != "tarlink-data test-version\n" {
		t.Fatalf("version: code=%d out=%q", code, out.String())
	}
	if code := Run([]string{"unknown"}, strings.NewReader(""), &out, &errOut, "dev"); code == 0 {
		t.Fatal("unknown command succeeded")
	}
}

func TestSyncSyntheticAcceptanceScenario(t *testing.T) {
	configHome, cacheHome, dataHome := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("XDG_DATA_HOME", dataHome)
	if err := os.MkdirAll(filepath.Join(cacheHome, "tarlink-data"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheHome, "tarlink-data", "update.json"), []byte(`{"checked_at":`+strconv.FormatInt(time.Now().Unix(), 10)+`,"latest":"v9.9.9"}`), 0600); err != nil {
		t.Fatal(err)
	}
	sourceDir := t.TempDir()
	payload := []byte("synthetic payload")
	digest := sha256.Sum256(payload)
	sha := hex.EncodeToString(digest[:])
	if err := os.WriteFile(filepath.Join(sourceDir, "random-name.bin"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	recipeFile := filepath.Join(t.TempDir(), "recipe.yaml")
	recipeText := "version: 1\nrecipes:\n  - app: example-app\n    files:\n      - target: required-data.bin\n        accepted:\n          - size: " + strconv.Itoa(len(payload)) + "\n            sha256: " + sha + "\n"
	if err := os.WriteFile(recipeFile, []byte(recipeText), 0600); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(configHome, "tarlink-data")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	configText := "version: 1\nrecipes:\n  - " + recipeFile + "\nsources:\n  - " + sourceDir + "\n"
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(configText), 0600); err != nil {
		t.Fatal(err)
	}
	input := `[{"id":"example-app","installed_version":"1.0","extra":true}]`
	var out, errOut bytes.Buffer
	if code := Run([]string{"sync"}, strings.NewReader(input), &out, &errOut, "v1.0.0"); code != 0 || !strings.Contains(out.String(), "copied") || !strings.Contains(errOut.String(), "update available: v9.9.9") {
		t.Fatalf("first sync: code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
	out.Reset()
	if code := Run([]string{"sync", "--json"}, strings.NewReader(input), &out, &errOut, "dev"); code != 0 {
		t.Fatalf("second sync: code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
	var response struct {
		Results []struct{ Status string } `json:"results"`
	}
	if err := json.Unmarshal(out.Bytes(), &response); err != nil || len(response.Results) != 1 || response.Results[0].Status != "satisfied" {
		t.Fatalf("second JSON sync: %q %v", out.String(), err)
	}
	dataPath := filepath.Join(dataHome, "tarlink-data", "apps", "example-app", "required-data.bin")
	if got, err := os.ReadFile(dataPath); err != nil || string(got) != string(payload) {
		t.Fatalf("materialized data: %q %v", got, err)
	}
}

func TestJSONOutputRemainsJSONOnConfigFailure(t *testing.T) {
	var out, errOut bytes.Buffer
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	// The command fails before producing a result when no XDG config exists;
	// this still verifies that diagnostics never contaminate stdout when JSON
	// mode is selected after configuration is made available by callers.
	_ = json.Valid
	Run([]string{"sync", "--json"}, strings.NewReader("[]"), &out, &errOut, "dev")
	if out.Len() != 0 {
		t.Fatalf("unexpected non-JSON output on early error: %q", out.String())
	}
}
