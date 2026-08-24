package update

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
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	cacheName       = "update.json"
	passiveInterval = 24 * time.Hour
	upgradeInterval = 5 * time.Minute
	maxDiscovery    = 64 << 10
	maxChecksum     = 1 << 20
	maxBinary       = 256 << 20
)

var (
	discoveryURL   = "https://github.com/drobilica/tarlink-data/releases/latest"
	repositoryHost = "github.com"
	repositoryPath = "/drobilica/tarlink-data"
	assetName      = "tarlink-data-linux-amd64"
	now            = time.Now
	executablePath = os.Executable
)

type cache struct {
	CheckedAt      int64  `json:"checked_at"`
	Latest         string `json:"latest,omitempty"`
	UpgradeChecked int64  `json:"upgrade_checked_at,omitempty"`
	UpgradeLatest  string `json:"upgrade_latest,omitempty"`
}

var stableVersion = regexp.MustCompile("^v([0-9]+)\\.([0-9]+)\\.([0-9]+)$")

func Notify(cacheDir, current string, client *http.Client, stderr io.Writer) {
	if current == "dev" || !isStable(current) {
		return
	}
	state, ok := readCache(cacheDir)
	age := time.Duration(now().Unix()-state.CheckedAt) * time.Second
	if ok && age >= 0 && age < passiveInterval {
		if newer(state.Latest, current) {
			_, _ = fmt.Fprintf(stderr, "tarlink-data: update available: %s; run 'tarlink-data upgrade'\n", state.Latest)
		}
		return
	}
	latest, err := discover(client)
	state.CheckedAt = now().Unix()
	if err == nil {
		state.Latest = latest
	}
	writeCache(cacheDir, state)
	if err == nil && newer(latest, current) {
		_, _ = fmt.Fprintf(stderr, "tarlink-data: update available: %s; run 'tarlink-data upgrade'\n", latest)
	}
}

func Upgrade(current, cacheDir, stateDir string, client *http.Client) (string, error) {
	if !isStable(current) {
		return "", fmt.Errorf("tarlink-data upgrade requires a stable release build")
	}
	target, err := executablePath()
	if err != nil {
		return "", fmt.Errorf("find installed binary: %w", err)
	}
	if err := verifyOwnership(target, filepath.Join(stateDir, "install.sha256")); err != nil {
		return "", err
	}
	state, _ := readCache(cacheDir)
	latest := ""
	age := time.Duration(now().Unix()-state.UpgradeChecked) * time.Second
	if state.UpgradeLatest == current && age >= 0 && age < upgradeInterval {
		latest = current
	} else {
		latest, err = discover(client)
		if err != nil {
			return "", fmt.Errorf("discover latest release: %w", err)
		}
	}
	if !newer(latest, current) {
		state.CheckedAt, state.Latest = now().Unix(), latest
		state.UpgradeChecked, state.UpgradeLatest = now().Unix(), current
		writeCache(cacheDir, state)
		return "already current", nil
	}
	if err := replace(target, stateDir, latest, client); err != nil {
		return "", err
	}
	state.CheckedAt, state.Latest = now().Unix(), latest
	state.UpgradeChecked, state.UpgradeLatest = now().Unix(), latest
	writeCache(cacheDir, state)
	return latest, nil
}

func discover(client *http.Client) (string, error) {
	if client == nil {
		client = &http.Client{}
	}
	client = cloneClient(client)
	redirects := 0
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		redirects++
		if redirects > 3 || !officialURL(req.URL) {
			return errors.New("invalid release discovery redirect")
		}
		return nil
	}
	parsed, err := url.Parse(discoveryURL)
	if err != nil || !officialURL(parsed) || parsed.Path != repositoryPath+"/releases/latest" {
		return "", errors.New("invalid release discovery URL")
	}
	resp, err := client.Get(parsed.String())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("release discovery returned HTTP %s", resp.Status)
	}
	if !officialURL(resp.Request.URL) {
		return "", errors.New("release discovery resolved outside the official repository")
	}
	prefix := repositoryPath + "/releases/tag/"
	if !strings.HasPrefix(resp.Request.URL.Path, prefix) || resp.Request.URL.RawQuery != "" || resp.Request.URL.Fragment != "" {
		return "", errors.New("release discovery did not resolve to a stable release")
	}
	version := strings.TrimPrefix(resp.Request.URL.Path, prefix)
	if !isStable(version) {
		return "", errors.New("release discovery did not resolve to a stable release")
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDiscovery))
	return version, nil
}

func replace(target, stateDir, version string, client *http.Client) error {
	binURL := fmt.Sprintf("https://%s%s/releases/download/%s/%s", repositoryHost, repositoryPath, version, assetName)
	sumURL := fmt.Sprintf("https://%s%s/releases/download/%s/SHA256SUMS", repositoryHost, repositoryPath, version)
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("prepare binary directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".tarlink-data-upgrade-*")
	if err != nil {
		return fmt.Errorf("create upgrade temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = tmp.Close(); _ = os.Remove(tmpName) }()
	if err := download(client, binURL, tmp, maxBinary); err != nil {
		return fmt.Errorf("download release binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close release binary: %w", err)
	}
	actual, err := hashFile(tmpName)
	if err != nil {
		return fmt.Errorf("hash release binary: %w", err)
	}
	expected, err := checksum(client, sumURL)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("release binary SHA-256 verification failed")
	}
	if err := os.Chmod(tmpName, 0755); err != nil {
		return fmt.Errorf("make release binary executable: %w", err)
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return fmt.Errorf("prepare ownership directory: %w", err)
	}
	markerTmp, err := os.CreateTemp(stateDir, ".install.sha256-*")
	if err != nil {
		return fmt.Errorf("create ownership marker: %w", err)
	}
	markerName := markerTmp.Name()
	defer func() { _ = markerTmp.Close(); _ = os.Remove(markerName) }()
	if _, err := fmt.Fprintln(markerTmp, actual); err != nil {
		return fmt.Errorf("write ownership marker: %w", err)
	}
	if err := markerTmp.Chmod(0600); err != nil {
		return fmt.Errorf("protect ownership marker: %w", err)
	}
	if err := markerTmp.Close(); err != nil {
		return fmt.Errorf("close ownership marker: %w", err)
	}
	backup := filepath.Join(dir, ".tarlink-data-upgrade-previous")
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("prepare upgrade rollback: %w", err)
	}
	if err := os.Rename(target, backup); err != nil {
		return fmt.Errorf("stage existing binary: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Rename(backup, target)
		return fmt.Errorf("publish release binary: %w", err)
	}
	if err := os.Rename(markerName, filepath.Join(stateDir, "install.sha256")); err != nil {
		_ = os.Remove(target)
		_ = os.Rename(backup, target)
		return fmt.Errorf("publish ownership marker: %w", err)
	}
	if err := os.Remove(backup); err != nil {
		return fmt.Errorf("remove upgrade rollback file: %w", err)
	}
	return nil
}

func download(client *http.Client, rawURL string, dst io.Writer, limit int64) error {
	if client == nil {
		client = &http.Client{}
	}
	clone := cloneClient(client)
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || req.URL.User != nil || len(via) >= 5 {
			return errors.New("invalid release redirect")
		}
		return nil
	}
	resp, err := clone.Get(rawURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %s", resp.Status)
	}
	n, err := io.Copy(dst, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if n == 0 || n > limit {
		return errors.New("downloaded release asset is empty or too large")
	}
	return nil
}

func checksum(client *http.Client, rawURL string) (string, error) {
	var b limitedBuffer
	if err := download(client, rawURL, &b, maxChecksum); err != nil {
		return "", fmt.Errorf("download checksums: %w", err)
	}
	for _, line := range strings.Split(b.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == assetName && isHexDigest(fields[0]) {
			return fields[0], nil
		}
	}
	return "", errors.New("SHA256SUMS has no valid checksum for release binary")
}

type limitedBuffer struct {
	b strings.Builder
	n int64
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.n+int64(len(p)) > maxChecksum {
		return 0, errors.New("response too large")
	}
	n, err := b.b.Write(p)
	b.n += int64(n)
	return n, err
}
func (b *limitedBuffer) String() string { return b.b.String() }
func verifyOwnership(target, marker string) error {
	info, err := os.Lstat(target)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return errors.New("installed binary is not a regular executable")
	}
	data, err := os.ReadFile(marker)
	if err != nil || len(strings.TrimSpace(string(data))) != 64 {
		return errors.New("ownership marker is absent or malformed")
	}
	digest := strings.TrimSpace(string(data))
	actual, err := hashFile(target)
	if err != nil || actual != digest {
		return errors.New("ownership marker does not match installed binary")
	}
	return nil
}
func hashFile(name string) (string, error) {
	f, err := os.Open(name)
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
func isHexDigest(v string) bool {
	if len(v) != 64 {
		return false
	}
	_, err := hex.DecodeString(v)
	return err == nil && strings.ToLower(v) == v
}
func isStable(v string) bool { return stableVersion.MatchString(v) }
func newer(a, b string) bool {
	am, ok := semver(a)
	if !ok {
		return false
	}
	bm, ok := semver(b)
	if !ok {
		return false
	}
	for i := range am {
		if am[i] != bm[i] {
			return am[i] > bm[i]
		}
	}
	return false
}
func semver(v string) ([3]int64, bool) {
	var out [3]int64
	m := stableVersion.FindStringSubmatch(v)
	if m == nil {
		return out, false
	}
	for i := range out {
		n, err := strconv.ParseInt(m[i+1], 10, 32)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
func officialURL(u *url.URL) bool {
	return u.Scheme == "https" && u.User == nil && u.Host == repositoryHost && u.RawQuery == "" && u.Fragment == "" && (u.Path == repositoryPath+"/releases/latest" || strings.HasPrefix(u.Path, repositoryPath+"/releases/tag/"))
}
func cloneClient(c *http.Client) *http.Client { out := *c; return &out }
func readCache(dir string) (cache, bool) {
	b, err := os.ReadFile(filepath.Join(dir, cacheName))
	if err != nil {
		return cache{}, false
	}
	var v cache
	if json.Unmarshal(b, &v) != nil {
		return cache{}, false
	}
	return v, true
}
func writeCache(dir string, v cache) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = os.MkdirAll(dir, 0700)
	tmp, err := os.CreateTemp(dir, ".update-*")
	if err != nil {
		return
	}
	name := tmp.Name()
	if _, err = tmp.Write(b); err == nil {
		_ = tmp.Chmod(0600)
		_ = tmp.Close()
		_ = os.Rename(name, filepath.Join(dir, cacheName))
	} else {
		_ = tmp.Close()
	}
	_ = os.Remove(name)
}
