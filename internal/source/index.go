package source

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	maxIndexBytes = 16 << 20
	maxPathBytes  = 4096
)

type Entry struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	MtimeNS int64  `json:"mtime_ns,omitempty"`
	SHA256  string `json:"sha256"`
}

type Index struct {
	Version int     `json:"version"`
	Files   []Entry `json:"files"`
}

type Provider interface {
	Name() string
	Lookup(digest string) ([]Entry, error)
	RefreshOnce() (bool, error)
	Open(entry Entry) (io.ReadCloser, error)
	Invalidate(entry Entry)
}

type base struct {
	mu        sync.Mutex
	index     Index
	bySHA     map[string][]Entry
	loaded    bool
	refreshed bool
}

func (b *base) setIndex(index Index) {
	b.index = index
	b.bySHA = make(map[string][]Entry)
	for _, entry := range index.Files {
		b.bySHA[entry.SHA256] = append(b.bySHA[entry.SHA256], entry)
	}
	for digest := range b.bySHA {
		sort.Slice(b.bySHA[digest], func(i, j int) bool { return b.bySHA[digest][i].Path < b.bySHA[digest][j].Path })
	}
	b.loaded = true
}

func (b *base) lookup(digest string) []Entry {
	entries := append([]Entry(nil), b.bySHA[digest]...)
	return entries
}

func (b *base) markRefresh() bool {
	if b.refreshed {
		return false
	}
	b.refreshed = true
	return true
}

func validDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validateIndex(index Index, remote bool) error {
	if index.Version != 1 {
		return fmt.Errorf("source index version must be 1")
	}
	seen := make(map[string]struct{}, len(index.Files))
	for i := range index.Files {
		entry := &index.Files[i]
		if err := validatePath(entry.Path, remote); err != nil {
			return fmt.Errorf("source index file %d: %w", i, err)
		}
		if entry.Size <= 0 {
			return fmt.Errorf("source index file %q has invalid size", entry.Path)
		}
		if !validDigest(entry.SHA256) {
			return fmt.Errorf("source index file %q has invalid SHA-256", entry.Path)
		}
		if _, exists := seen[entry.Path]; exists {
			return fmt.Errorf("source index has duplicate path %q", entry.Path)
		}
		seen[entry.Path] = struct{}{}
	}
	sort.Slice(index.Files, func(i, j int) bool { return index.Files[i].Path < index.Files[j].Path })
	return nil
}

func validatePath(value string, remote bool) error {
	if value == "" || len(value) > maxPathBytes || strings.ContainsAny(value, "\\\x00") || path.IsAbs(value) {
		return fmt.Errorf("invalid relative path %q", value)
	}
	clean := path.Clean(value)
	if clean != value || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("invalid relative path %q", value)
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("invalid relative path %q", value)
		}
	}
	if remote {
		u, err := url.Parse(value)
		if err != nil || u.IsAbs() || u.Host != "" || u.Scheme != "" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("invalid remote relative path %q", value)
		}
	}
	return nil
}

func parseIndex(data []byte, remote bool) (Index, error) {
	if len(data) > maxIndexBytes {
		return Index{}, fmt.Errorf("source index is too large")
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	var index Index
	if err := dec.Decode(&index); err != nil {
		return Index{}, fmt.Errorf("parse source index: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Index{}, fmt.Errorf("source index contains trailing JSON")
		}
		return Index{}, fmt.Errorf("parse source index: %w", err)
	}
	if err := validateIndex(index, remote); err != nil {
		return Index{}, err
	}
	return index, nil
}

func parseCachedIndex(filename string, remote bool) (Index, error) {
	file, err := os.Open(filename)
	if err != nil {
		return Index{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxIndexBytes+1))
	if err != nil {
		return Index{}, err
	}
	return parseIndex(data, remote)
}

func cacheID(identity string) string {
	digest := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(digest[:])
}

func writeCache(filename string, index Index) error {
	data, err := json.Marshal(Index{Version: 1, Files: index.Files})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(filename), ".index-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filename)
}

type Local struct {
	base
	rootPath  string
	root      *os.Root
	cacheFile string
}

func NewLocal(rootPath, cacheDir string) (*Local, error) {
	abs, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, fmt.Errorf("normalize source path %s: %w", rootPath, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("stat source %s: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("source is not a directory: %s", abs)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("open source %s: %w", abs, err)
	}
	return &Local{rootPath: filepath.Clean(abs), root: root, cacheFile: filepath.Join(cacheDir, "sources", cacheID(filepath.Clean(abs))+".json")}, nil
}

func (s *Local) Name() string { return s.rootPath }

func (s *Local) Lookup(digest string) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		if index, err := parseCachedIndex(s.cacheFile, false); err == nil {
			s.setIndex(index)
		} else {
			s.setIndex(Index{Version: 1})
		}
	}
	entries := s.lookup(digest)
	entries = s.currentEntries(entries)
	if len(entries) == 0 && s.markRefresh() {
		if err := s.refreshLocked(); err != nil {
			return nil, err
		}
		entries = s.lookup(digest)
	}
	if len(entries) == 0 && s.markRefresh() {
		if err := s.refreshLocked(); err != nil {
			return nil, err
		}
		entries = s.lookup(digest)
	}
	return entries, nil
}

func (s *Local) currentEntries(entries []Entry) []Entry {
	result := entries[:0]
	for _, entry := range entries {
		info, err := s.root.Lstat(filepath.FromSlash(entry.Path))
		if err == nil && info.Mode().IsRegular() && info.Size() == entry.Size && info.ModTime().UnixNano() == entry.MtimeNS {
			result = append(result, entry)
		}
	}
	return result
}

func (s *Local) RefreshOnce() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.markRefresh() {
		return false, nil
	}
	return true, s.refreshLocked()
}

func (s *Local) refreshLocked() error {
	old := make(map[string]Entry, len(s.index.Files))
	for _, entry := range s.index.Files {
		old[entry.Path] = entry
	}
	var files []Entry
	err := filepath.WalkDir(s.rootPath, func(filename string, dirEntry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filename == s.rootPath {
			return nil
		}
		if dirEntry.Type()&os.ModeSymlink != 0 {
			if dirEntry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !dirEntry.Type().IsRegular() {
			return nil
		}
		info, err := dirEntry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(s.rootPath, filename)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if info.Size() <= 0 {
			return nil
		}
		if previous, ok := old[rel]; ok && previous.Size == info.Size() && previous.MtimeNS == info.ModTime().UnixNano() {
			files = append(files, previous)
			return nil
		}
		digest, err := s.hashRelative(rel)
		if err != nil {
			return err
		}
		files = append(files, Entry{Path: rel, Size: info.Size(), MtimeNS: info.ModTime().UnixNano(), SHA256: digest})
		return nil
	})
	if err != nil {
		return fmt.Errorf("index source %s: %w", s.rootPath, err)
	}
	index := Index{Version: 1, Files: files}
	if err := validateIndex(index, false); err != nil {
		return err
	}
	if err := writeCache(s.cacheFile, index); err != nil {
		return fmt.Errorf("write source index cache: %w", err)
	}
	s.setIndex(index)
	return nil
}

func (s *Local) hashRelative(name string) (string, error) {
	f, err := s.root.OpenFile(filepath.FromSlash(name), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *Local) Open(entry Entry) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	info, err := s.root.Lstat(filepath.FromSlash(entry.Path))
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() != entry.Size || info.ModTime().UnixNano() != entry.MtimeNS {
		return nil, fmt.Errorf("stale source candidate %s", entry.Path)
	}
	f, err := s.root.Open(filepath.FromSlash(entry.Path))
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (s *Local) Invalidate(entry Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		return
	}
	filtered := s.index.Files[:0]
	for _, current := range s.index.Files {
		if current.Path != entry.Path {
			filtered = append(filtered, current)
		}
	}
	s.setIndex(Index{Version: 1, Files: filtered})
}

type Remote struct {
	base
	indexURL  string
	client    *http.Client
	cacheFile string
}

func NewRemote(indexURL, cacheDir string, client *http.Client) (*Remote, error) {
	u, err := url.Parse(indexURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("source index URL must be HTTPS: %s", indexURL)
	}
	if client == nil {
		client = NewHTTPClient()
	}
	return &Remote{indexURL: indexURL, client: client, cacheFile: filepath.Join(cacheDir, "sources", cacheID(indexURL)+".json")}, nil
}

func (s *Remote) Name() string { return s.indexURL }

func (s *Remote) Lookup(digest string) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		if index, err := parseCachedIndex(s.cacheFile, true); err == nil {
			s.setIndex(index)
		} else {
			s.setIndex(Index{Version: 1})
		}
	}
	entries := s.lookup(digest)
	if len(entries) == 0 && s.markRefresh() {
		if err := s.refreshLocked(); err != nil {
			return nil, err
		}
		entries = s.lookup(digest)
	}
	return entries, nil
}

func (s *Remote) RefreshOnce() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.markRefresh() {
		return false, nil
	}
	return true, s.refreshLocked()
}

func (s *Remote) refreshLocked() error {
	resp, err := s.client.Get(s.indexURL)
	if err != nil {
		return fmt.Errorf("fetch source index: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("fetch source index: HTTP status %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxIndexBytes+1))
	if err != nil {
		return fmt.Errorf("read source index: %w", err)
	}
	index, err := parseIndex(data, true)
	if err != nil {
		return err
	}
	if err := writeCache(s.cacheFile, index); err != nil {
		return fmt.Errorf("write source index cache: %w", err)
	}
	s.setIndex(index)
	return nil
}

func (s *Remote) Open(entry Entry) (io.ReadCloser, error) {
	baseURL, err := url.Parse(s.indexURL)
	if err != nil {
		return nil, err
	}
	resolved := baseURL.ResolveReference(&url.URL{Path: entry.Path})
	if resolved.Scheme != "https" || resolved.Host != baseURL.Host {
		return nil, fmt.Errorf("resolved remote candidate escaped HTTPS source")
	}
	resp, err := s.client.Get(resolved.String())
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("fetch candidate: HTTP status %s", resp.Status)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != entry.Size {
		resp.Body.Close()
		return nil, fmt.Errorf("candidate Content-Length does not match indexed size")
	}
	return &boundedBody{body: resp.Body, limit: entry.Size}, nil
}

type boundedBody struct {
	body  io.ReadCloser
	limit int64
	read  int64
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if b.read > b.limit {
		return 0, io.EOF
	}
	remaining := b.limit + 1 - b.read
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := b.body.Read(p)
	b.read += int64(n)
	return n, err
}

func (b *boundedBody) Close() error { return b.body.Close() }

func (s *Remote) Invalidate(entry Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		return
	}
	filtered := s.index.Files[:0]
	for _, current := range s.index.Files {
		if current.Path != entry.Path {
			filtered = append(filtered, current)
		}
	}
	s.setIndex(Index{Version: 1, Files: filtered})
}

func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			if req.URL.Scheme != "https" {
				return fmt.Errorf("HTTPS source redirected to non-HTTPS URL")
			}
			return nil
		},
	}
}
