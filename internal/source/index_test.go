package source

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func digest(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func TestLocalIndexHashesReusesCacheAndRefreshesRename(t *testing.T) {
	root := t.TempDir()
	cache := t.TempDir()
	data := []byte("synthetic bytes")
	if err := os.WriteFile(filepath.Join(root, "random-name.bin"), data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewLocal(root, cache)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := s.Lookup(digest(data))
	if err != nil || len(entries) != 1 || entries[0].Path != "random-name.bin" {
		t.Fatalf("first index: %#v %v", entries, err)
	}
	cacheFiles, _ := filepath.Glob(filepath.Join(cache, "sources", "*.json"))
	if len(cacheFiles) != 1 {
		t.Fatalf("expected one cache file, got %v", cacheFiles)
	}
	if err := os.Rename(filepath.Join(root, "random-name.bin"), filepath.Join(root, "renamed.bin")); err != nil {
		t.Fatal(err)
	}
	s2, err := NewLocal(root, cache)
	if err != nil {
		t.Fatal(err)
	}
	entries, err = s2.Lookup(digest(data))
	if err != nil || len(entries) != 1 || entries[0].Path != "renamed.bin" {
		t.Fatalf("renamed index: %#v %v", entries, err)
	}
}

func TestLocalIndexSkipsSymlinksAndCorruptCacheRegenerates(t *testing.T) {
	root := t.TempDir()
	cache := t.TempDir()
	data := []byte("payload")
	file := filepath.Join(root, "real.bin")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, filepath.Join(root, "link.bin")); err != nil {
		t.Skip("symlinks unavailable")
	}
	s, err := NewLocal(root, cache)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(digest(data)); err != nil {
		t.Fatal(err)
	}
	cacheFiles, _ := filepath.Glob(filepath.Join(cache, "sources", "*.json"))
	if err := os.WriteFile(cacheFiles[0], []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	s2, err := NewLocal(root, cache)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := s2.Lookup(digest(data))
	if err != nil || len(entries) != 1 || entries[0].Path != "real.bin" {
		t.Fatalf("regenerated index: %#v %v", entries, err)
	}
}

func TestOversizedCacheRegenerates(t *testing.T) {
	root := t.TempDir()
	cache := t.TempDir()
	payload := []byte("payload")
	if err := os.WriteFile(filepath.Join(root, "data.bin"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewLocal(root, cache)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(digest(payload)); err != nil {
		t.Fatal(err)
	}
	cacheFiles, _ := filepath.Glob(filepath.Join(cache, "sources", "*.json"))
	if err := os.WriteFile(cacheFiles[0], make([]byte, maxIndexBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	s2, err := NewLocal(root, cache)
	if err != nil {
		t.Fatal(err)
	}
	if entries, err := s2.Lookup(digest(payload)); err != nil || len(entries) != 1 {
		t.Fatalf("oversized cache regeneration: %#v %v", entries, err)
	}
}

func TestIndexRejectsRemoteTraversalAndAbsoluteURLs(t *testing.T) {
	base := `{"version":1,"files":[{"path":"%s","size":1,"sha256":"%s"}]}`
	for _, candidate := range []string{"../escape.bin", "/absolute.bin", "https://bad.example/file", `dir\\file.bin`} {
		if _, err := parseIndex([]byte(fmt.Sprintf(base, candidate, strings.Repeat("a", 64))), true); err == nil {
			t.Errorf("%q: expected rejection", candidate)
		}
	}
}

func TestIndexOutputIsSorted(t *testing.T) {
	index := Index{Version: 1, Files: []Entry{{Path: "z", Size: 1, SHA256: strings.Repeat("b", 64)}, {Path: "a", Size: 1, SHA256: strings.Repeat("a", 64)}}}
	if err := validateIndex(index, false); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(index)
	if err != nil || !strings.Contains(string(data), `"path":"a"`) {
		t.Fatalf("marshal index: %s %v", data, err)
	}
}

func TestRemoteHTTPSIndexAndCandidate(t *testing.T) {
	if !loopbackAvailable() {
		t.Skip("loopback listeners unavailable in this environment")
	}
	payload := []byte("remote synthetic")
	indexBytes, _ := json.Marshal(Index{Version: 1, Files: []Entry{{Path: "files/random.bin", Size: int64(len(payload)), SHA256: digest(payload)}}})
	server := newTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			_, _ = w.Write(indexBytes)
		case "/files/random.bin":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewHTTPClient()
	client.Transport = &http.Transport{TLSClientConfig: server.Client().Transport.(*http.Transport).TLSClientConfig}
	remote, err := NewRemote(server.URL+"/index.json", t.TempDir(), client)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := remote.Lookup(digest(payload))
	if err != nil || len(entries) != 1 {
		t.Fatalf("remote lookup: %#v %v", entries, err)
	}
	body, err := remote.Open(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(body)
	body.Close()
	if err != nil || string(got) != string(payload) {
		t.Fatalf("remote body: %q %v", got, err)
	}
}

func TestHTTPSRedirectToHTTPRejected(t *testing.T) {
	if !loopbackAvailable() {
		t.Skip("loopback listeners unavailable in this environment")
	}
	plain := newHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"version":1,"files":[]}`)) }))
	defer plain.Close()
	tls := newTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, plain.URL, http.StatusFound) }))
	defer tls.Close()
	client := NewHTTPClient()
	client.Transport = &http.Transport{TLSClientConfig: tls.Client().Transport.(*http.Transport).TLSClientConfig}
	remote, err := NewRemote(tls.URL+"/index.json", t.TempDir(), client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Lookup(strings.Repeat("a", 64)); err == nil {
		t.Fatal("expected HTTP redirect rejection")
	}
	if _, err := NewRemote("http://example.invalid/index.json", t.TempDir(), client); err == nil {
		t.Fatal("expected HTTP source rejection")
	}
}

func loopbackAvailable() bool {
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		return false
	}
	listener.Close()
	return true
}

func newHTTPServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server.Listener = listener
	server.Start()
	return server
}

func newTLSServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server.Listener = listener
	server.StartTLS()
	return server
}
