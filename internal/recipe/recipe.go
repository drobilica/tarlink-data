package recipe

import (
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
	"syscall"
	"time"
	"unicode"

	"go.yaml.in/yaml/v3"
)

const maxCatalogBytes = 4 << 20

type Catalog struct {
	Version int      `yaml:"version"`
	Recipes []Recipe `yaml:"recipes"`
}

type Recipe struct {
	App   string        `yaml:"app"`
	Files []Requirement `yaml:"files"`
}

type Requirement struct {
	Target   string    `yaml:"target"`
	Accepted []Variant `yaml:"accepted"`
}

type Variant struct {
	Size   int64  `yaml:"size"`
	SHA256 string `yaml:"sha256"`
}

func (r Recipe) Requirements() []Requirement { return r.Files }

func LoadAll(entries []string, client *http.Client) (map[string]Recipe, error) {
	result := make(map[string]Recipe)
	for _, raw := range entries {
		entry := raw
		if strings.HasPrefix(entry, "https://") {
			catalog, err := loadURL(entry, client)
			if err != nil {
				return nil, fmt.Errorf("load recipe catalog %s: %w", entry, err)
			}
			if err := addCatalog(result, catalog, entry); err != nil {
				return nil, err
			}
			continue
		}
		if strings.Contains(entry, "://") || strings.HasPrefix(entry, "http:") {
			return nil, fmt.Errorf("recipe source must be a local path or HTTPS URL: %s", entry)
		}
		pathValue, err := expandPath(entry)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(pathValue)
		if err != nil {
			return nil, fmt.Errorf("stat recipe source %s: %w", pathValue, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("recipe source symlink is not allowed: %s", pathValue)
		}
		if info.IsDir() {
			if err := loadDirectory(result, pathValue); err != nil {
				return nil, err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("recipe source is not a regular file or directory: %s", pathValue)
		}
		catalog, err := loadFile(pathValue)
		if err != nil {
			return nil, err
		}
		if err := addCatalog(result, catalog, pathValue); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func expandPath(value string) (string, error) {
	if value == "~" || strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand recipe path %q: %w", value, err)
		}
		if value == "~" {
			return home, nil
		}
		return filepath.Join(home, value[2:]), nil
	}
	return value, nil
}

func loadDirectory(result map[string]Recipe, directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read recipe directory %s: %w", directory, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if filepath.Ext(name) != ".yaml" && filepath.Ext(name) != ".yml" {
			continue
		}
		full := filepath.Join(directory, name)
		info, err := os.Lstat(full)
		if err != nil {
			return fmt.Errorf("stat recipe file %s: %w", full, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("recipe file is not regular: %s", full)
		}
		catalog, err := loadFile(full)
		if err != nil {
			return err
		}
		if err := addCatalog(result, catalog, full); err != nil {
			return err
		}
	}
	return nil
}

func loadFile(filename string) (Catalog, error) {
	file, err := os.OpenFile(filename, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return Catalog{}, fmt.Errorf("read recipe catalog %s: %w", filename, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxCatalogBytes+1))
	if err != nil {
		return Catalog{}, fmt.Errorf("read recipe catalog %s: %w", filename, err)
	}
	if len(data) > maxCatalogBytes {
		return Catalog{}, fmt.Errorf("recipe catalog %s is too large", filename)
	}
	return parse(data, filename)
}

func loadURL(raw string, client *http.Client) (Catalog, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return Catalog{}, fmt.Errorf("recipe catalog URL must be HTTPS: %s", raw)
	}
	if client == nil {
		client = newHTTPClient()
	}
	resp, err := client.Get(raw)
	if err != nil {
		return Catalog{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Catalog{}, fmt.Errorf("HTTP status %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes+1))
	if err != nil {
		return Catalog{}, fmt.Errorf("read catalog response: %w", err)
	}
	if len(data) > maxCatalogBytes {
		return Catalog{}, fmt.Errorf("catalog response is too large")
	}
	return parse(data, raw)
}

func newHTTPClient() *http.Client {
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

func parse(data []byte, source string) (Catalog, error) {
	var catalog Catalog
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&catalog); err != nil {
		return Catalog{}, fmt.Errorf("parse recipe catalog %s: %w", source, err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Catalog{}, fmt.Errorf("recipe catalog %s contains multiple documents", source)
		}
		return Catalog{}, fmt.Errorf("parse recipe catalog %s: %w", source, err)
	}
	if catalog.Version != 1 {
		return Catalog{}, fmt.Errorf("recipe catalog %s version must be 1", source)
	}
	seenApps := make(map[string]struct{}, len(catalog.Recipes))
	for i := range catalog.Recipes {
		if err := validateRecipe(&catalog.Recipes[i]); err != nil {
			return Catalog{}, fmt.Errorf("recipe catalog %s: %w", source, err)
		}
		if _, exists := seenApps[catalog.Recipes[i].App]; exists {
			return Catalog{}, fmt.Errorf("recipe catalog %s has duplicate app %q", source, catalog.Recipes[i].App)
		}
		seenApps[catalog.Recipes[i].App] = struct{}{}
	}
	return catalog, nil
}

func addCatalog(result map[string]Recipe, catalog Catalog, source string) error {
	for _, item := range catalog.Recipes {
		if _, exists := result[item.App]; exists {
			return fmt.Errorf("duplicate recipe app %q from %s", item.App, source)
		}
		result[item.App] = item
	}
	return nil
}

func validateRecipe(item *Recipe) error {
	if !validAppID(item.App) {
		return fmt.Errorf("invalid app ID %q", item.App)
	}
	if len(item.Files) == 0 {
		return fmt.Errorf("recipe %q has no files", item.App)
	}
	seen := make(map[string]struct{}, len(item.Files))
	for i := range item.Files {
		file := &item.Files[i]
		if !validTarget(file.Target) {
			return fmt.Errorf("recipe %q has invalid target %q", item.App, file.Target)
		}
		if _, exists := seen[file.Target]; exists {
			return fmt.Errorf("recipe %q has duplicate target %q", item.App, file.Target)
		}
		seen[file.Target] = struct{}{}
		if len(file.Accepted) == 0 {
			return fmt.Errorf("recipe %q target %q has no accepted variants", item.App, file.Target)
		}
		seenSHA := make(map[string]struct{}, len(file.Accepted))
		for _, variant := range file.Accepted {
			if variant.Size <= 0 {
				return fmt.Errorf("recipe %q target %q has invalid size", item.App, file.Target)
			}
			if len(variant.SHA256) != 64 || strings.ToLower(variant.SHA256) != variant.SHA256 || !isHex(variant.SHA256) {
				return fmt.Errorf("recipe %q target %q has invalid SHA-256", item.App, file.Target)
			}
			if _, exists := seenSHA[variant.SHA256]; exists {
				return fmt.Errorf("recipe %q target %q has duplicate accepted digest", item.App, file.Target)
			}
			seenSHA[variant.SHA256] = struct{}{}
		}
	}
	return nil
}

func validAppID(value string) bool {
	if value == "" || value == "." || value == ".." || strings.ContainsAny(value, "/\\\x00") {
		return false
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validTarget(value string) bool {
	if value == "" || strings.ContainsAny(value, "\\\x00") || filepath.IsAbs(value) || path.IsAbs(value) || filepath.VolumeName(value) != "" {
		return false
	}
	clean := path.Clean(value)
	if clean != value || clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func isHex(value string) bool {
	for _, r := range value {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func SortedRequirements(r Recipe) []Requirement {
	result := append([]Requirement(nil), r.Files...)
	sort.Slice(result, func(i, j int) bool { return result[i].Target < result[j].Target })
	return result
}
