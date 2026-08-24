package update

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testServer(t *testing.T, version string, checksumOK, offline bool) (*http.Client, *int32) {
	t.Helper()
	var discoveries int32
	binary := []byte("new synthetic tarlink-data binary")
	digest := sha256.Sum256(binary)
	mux := http.NewServeMux()
	mux.HandleFunc("/drobilica/tarlink-data/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&discoveries, 1)
		if offline {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		http.Redirect(w, r, "/drobilica/tarlink-data/releases/tag/"+version, http.StatusFound)
	})
	mux.HandleFunc("/drobilica/tarlink-data/releases/tag/"+version, func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/drobilica/tarlink-data/releases/download/"+version+"/"+assetName, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(binary) })
	mux.HandleFunc("/drobilica/tarlink-data/releases/download/"+version+"/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		value := hex.EncodeToString(digest[:])
		if !checksumOK {
			value = strings.Repeat("0", 64)
		}
		_, _ = fmt.Fprintf(w, "%s  %s\n", value, assetName)
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	baseURL := "https://" + listener.Addr().String()
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		copy := req.Clone(req.Context())
		copy.URL.Scheme, copy.URL.Host = "http", listener.Addr().String()
		response, err := http.DefaultTransport.RoundTrip(copy)
		if response != nil && response.Request != nil {
			response.Request.URL = req.URL
		}
		return response, err
	})
	restore(t, baseURL+repositoryPath+"/releases/latest", listener.Addr().String(), server)
	return &http.Client{Transport: transport}, &discoveries
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func restore(t *testing.T, endpoint, host string, server *http.Server) {
	oldURL, oldHost, oldPath, oldNow := discoveryURL, repositoryHost, repositoryPath, now
	discoveryURL, repositoryHost, repositoryPath = endpoint, host, "/drobilica/tarlink-data"
	now = func() time.Time { return time.Unix(100000, 0) }
	t.Cleanup(func() {
		discoveryURL, repositoryHost, repositoryPath, now = oldURL, oldHost, oldPath, oldNow
		_ = server.Close()
	})
}

func TestDiscoveryAndStableValidation(t *testing.T) {
	client, _ := testServer(t, "v1.2.3", true, false)
	got, err := discover(client)
	if err != nil || got != "v1.2.3" {
		t.Fatalf("discover = %q, %v", got, err)
	}
	for _, value := range []string{"v1.2", "v1.2.3-rc1", "1.2.3"} {
		if isStable(value) {
			t.Fatalf("accepted unstable version %q", value)
		}
	}
	for _, endpoint := range []string{
		"https://github.com/drobilica/tarlink-data/releases/latest?query=1",
		"https://user@github.com/drobilica/tarlink-data/releases/latest",
		"https://evil.example/drobilica/tarlink-data/releases/latest",
	} {
		discoveryURL = endpoint
		if _, err := discover(client); err == nil {
			t.Fatalf("accepted malformed discovery URL %q", endpoint)
		}
	}
}

func TestPassiveCacheCooldownAndDev(t *testing.T) {
	client, requests := testServer(t, "v2.0.0", true, false)
	dir := t.TempDir()
	var stderr bytes.Buffer
	writeCache(dir, cache{CheckedAt: now().Unix(), Latest: "v2.0.0"})
	Notify(dir, "v1.0.0", client, &stderr)
	if *requests != 0 || !strings.Contains(stderr.String(), "update available: v2.0.0") {
		t.Fatalf("fresh cache: requests=%d stderr=%q", *requests, stderr.String())
	}
	writeCache(dir, cache{CheckedAt: now().Add(-passiveInterval).Unix()})
	Notify(dir, "v1.0.0", client, &stderr)
	if *requests != 1 {
		t.Fatalf("expired cache requests=%d, want 1", *requests)
	}
	client, requests = testServer(t, "v2.0.0", true, true)
	writeCache(dir, cache{CheckedAt: now().Add(-passiveInterval).Unix()})
	Notify(dir, "v1.0.0", client, &stderr)
	Notify(dir, "v1.0.0", client, &stderr)
	if *requests != 1 {
		t.Fatalf("failed lookup repeated: requests=%d, want 1", *requests)
	}
	*requests = 0
	Notify(dir, "dev", client, &stderr)
	if *requests != 0 {
		t.Fatal("dev build made an update request")
	}
}

func TestUpgradeAtomicChecksumAndOwnership(t *testing.T) {
	client, _ := testServer(t, "v1.0.1", true, false)
	bin := filepath.Join(t.TempDir(), "tarlink-data")
	old := []byte("old binary")
	if err := os.WriteFile(bin, old, 0755); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	digest := sha256.Sum256(old)
	if err := os.WriteFile(filepath.Join(state, "install.sha256"), []byte(hex.EncodeToString(digest[:])+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	oldExecutable := executablePath
	executablePath = func() (string, error) { return bin, nil }
	t.Cleanup(func() { executablePath = oldExecutable })
	if got, err := Upgrade("v1.0.0", t.TempDir(), state, client); err != nil || got != "v1.0.1" {
		t.Fatalf("upgrade = %q, %v", got, err)
	}
	if got, _ := os.ReadFile(bin); string(got) != "new synthetic tarlink-data binary" {
		t.Fatalf("binary was not replaced: %q", got)
	}

	badClient, _ := testServer(t, "v1.0.2", false, false)
	digest = sha256.Sum256([]byte("new synthetic tarlink-data binary"))
	if err := os.WriteFile(filepath.Join(state, "install.sha256"), []byte(hex.EncodeToString(digest[:])+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(bin)
	if _, err := Upgrade("v1.0.1", t.TempDir(), state, badClient); err == nil {
		t.Fatal("checksum failure succeeded")
	}
	after, _ := os.ReadFile(bin)
	if !bytes.Equal(before, after) {
		t.Fatal("checksum failure replaced the installation")
	}
}

func TestUpgradeCurrentUsesRecentProof(t *testing.T) {
	client, requests := testServer(t, "v1.0.0", true, false)
	bin := filepath.Join(t.TempDir(), "tarlink-data")
	old := []byte("current binary")
	if err := os.WriteFile(bin, old, 0755); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	digest := sha256.Sum256(old)
	if err := os.WriteFile(filepath.Join(state, "install.sha256"), []byte(hex.EncodeToString(digest[:])+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	oldExecutable := executablePath
	executablePath = func() (string, error) { return bin, nil }
	t.Cleanup(func() { executablePath = oldExecutable })
	cacheDir := t.TempDir()
	if got, err := Upgrade("v1.0.0", cacheDir, state, client); err != nil || got != "already current" {
		t.Fatalf("first current upgrade = %q, %v", got, err)
	}
	if got, err := Upgrade("v1.0.0", cacheDir, state, client); err != nil || got != "already current" {
		t.Fatalf("second current upgrade = %q, %v", got, err)
	}
	if *requests != 1 {
		t.Fatalf("current upgrade discovery requests=%d, want 1", *requests)
	}
}
