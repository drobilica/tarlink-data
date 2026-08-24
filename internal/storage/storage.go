package storage

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/drobilica/tarlink-data/internal/recipe"
)

var (
	ErrConflict = errors.New("destination conflict")
	ErrMismatch = errors.New("source verification mismatch")
	ErrStale    = errors.New("source candidate is stale")
)

type DestinationState int

const (
	Absent DestinationState = iota
	Satisfied
	Conflict
)

type Destination struct {
	State  DestinationState
	Digest string
}

func AppRoot(dataDir, app string) string {
	return filepath.Join(dataDir, "apps", app)
}

func Inspect(dataDir, app, target string, accepted []recipe.Variant) (Destination, error) {
	root, err := openAppRoot(dataDir, app, false)
	if errors.Is(err, os.ErrNotExist) {
		return Destination{State: Absent}, nil
	}
	if err != nil {
		return Destination{}, fmt.Errorf("open app data root: %w", err)
	}
	defer root.Close()
	if err := checkParents(root, filepath.ToSlash(filepath.Dir(target)), false); err != nil {
		return Destination{State: Conflict}, err
	}
	info, err := root.Lstat(filepath.FromSlash(target))
	if errors.Is(err, os.ErrNotExist) {
		return Destination{State: Absent}, nil
	}
	if err != nil {
		return Destination{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Destination{State: Conflict}, fmt.Errorf("destination is not a regular file: %s", target)
	}
	digest, size, err := hashRootFile(root, filepath.FromSlash(target))
	if err != nil {
		return Destination{}, err
	}
	for _, variant := range accepted {
		if size == variant.Size && digest == variant.SHA256 {
			return Destination{State: Satisfied, Digest: digest}, nil
		}
	}
	return Destination{State: Conflict, Digest: digest}, fmt.Errorf("destination has unaccepted bytes: %s", target)
}

type ReaderFactory func() (io.ReadCloser, error)

func Materialize(dataDir, app, target string, expected recipe.Variant, open ReaderFactory, dryRun bool) error {
	root, err := openAppRoot(dataDir, app, !dryRun)
	if errors.Is(err, os.ErrNotExist) && dryRun {
		return nil
	}
	if err != nil {
		return err
	}
	if dryRun {
		_ = root.Close()
		return nil
	}
	defer root.Close()
	parent := filepath.ToSlash(filepath.Dir(target))
	if parent == "." {
		parent = ""
	}
	if err := checkParents(root, parent, true); err != nil {
		return err
	}
	if info, err := root.Lstat(filepath.FromSlash(target)); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("destination is not a regular file: %s", target)
		}
		return ErrConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmpName, tmp, err := createTemp(root, parent)
	if err != nil {
		return fmt.Errorf("create destination temporary file: %w", err)
	}
	keep := false
	defer func() {
		_ = tmp.Close()
		if !keep {
			_ = root.Remove(tmpName)
		}
	}()
	reader, err := open()
	if err != nil {
		return err
	}
	digest, size, copyErr := copyVerified(tmp, reader, expected)
	closeErr := reader.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if size != expected.Size || digest != expected.SHA256 {
		return fmt.Errorf("%w: expected %d/%s, got %d/%s", ErrMismatch, expected.Size, expected.SHA256, size, digest)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("flush destination temporary file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if info, err := root.Lstat(filepath.FromSlash(target)); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("destination appeared as non-regular file: %s", target)
		}
		return ErrConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Link-then-unlink is the portable standard-library no-replace publish
	// sequence: Link fails with EEXIST and never replaces an existing target.
	// The temporary name is removed immediately after the destination link is
	// created, leaving only the verified regular file at the final path.
	if err := root.Link(tmpName, filepath.FromSlash(target)); err != nil {
		return err
	}
	if err := root.Remove(tmpName); err != nil {
		return err
	}
	keep = true
	dirName := parent
	if dirName == "" {
		dirName = "."
	}
	if dir, err := root.Open(dirName); err == nil {
		syncErr := dir.Sync()
		dir.Close()
		if syncErr != nil {
			return syncErr
		}
	}
	return nil
}

func openAppRoot(dataDir, app string, create bool) (*os.Root, error) {
	parentPath := filepath.Dir(dataDir)
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		return nil, fmt.Errorf("open managed data parent: %w", err)
	}
	defer parent.Close()
	dataName := filepath.Base(dataDir)
	if info, err := parent.Lstat(dataName); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("managed data root is not a real directory")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if !create {
			return nil, os.ErrNotExist
		}
		if err := parent.Mkdir(dataName, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	} else {
		return nil, err
	}
	dataRoot, err := parent.OpenRoot(dataName)
	if err != nil {
		return nil, err
	}
	if err := ensureRootDir(dataRoot, "apps", create); err != nil {
		dataRoot.Close()
		return nil, err
	}
	appsRoot, err := dataRoot.OpenRoot("apps")
	dataRoot.Close()
	if err != nil {
		return nil, err
	}
	if err := ensureRootDir(appsRoot, app, create); err != nil {
		appsRoot.Close()
		return nil, err
	}
	appRoot, err := appsRoot.OpenRoot(app)
	appsRoot.Close()
	if err != nil {
		return nil, err
	}
	return appRoot, nil
}

func ensureRootDir(root *os.Root, name string, create bool) error {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		if !create {
			return os.ErrNotExist
		}
		if err := root.Mkdir(name, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err = root.Lstat(name)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("managed data directory is not a real directory: %s", name)
	}
	return nil
}

func checkParents(root *os.Root, parent string, create bool) error {
	if parent == "" || parent == "." {
		return nil
	}
	current := ""
	for _, part := range strings.Split(parent, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("invalid destination parent")
		}
		if current == "" {
			current = part
		} else {
			current += "/" + part
		}
		info, err := root.Lstat(filepath.FromSlash(current))
		if errors.Is(err, os.ErrNotExist) {
			if !create {
				return nil
			}
			if err := root.Mkdir(current, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = root.Lstat(filepath.FromSlash(current))
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("destination parent is not a real directory: %s", current)
		}
	}
	return nil
}

func createTemp(root *os.Root, parent string) (string, *os.File, error) {
	for attempt := 0; attempt < 10; attempt++ {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", nil, err
		}
		name := ".tarlink-data-" + hex.EncodeToString(random[:]) + ".tmp"
		if parent != "" {
			name = parent + "/" + name
		}
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", nil, err
		}
		return name, file, nil
	}
	return "", nil, fmt.Errorf("could not create unique temporary file")
}

func copyVerified(destination *os.File, source io.Reader, expected recipe.Variant) (string, int64, error) {
	h := sha256.New()
	writer := io.MultiWriter(destination, h)
	limit := expected.Size + 1
	if expected.Size == int64(^uint64(0)>>1) {
		limit = expected.Size
	}
	count, err := io.Copy(writer, io.LimitReader(source, limit))
	if err != nil {
		return "", count, err
	}
	return hex.EncodeToString(h.Sum(nil)), count, nil
}

func hashRootFile(root *os.Root, name string) (string, int64, error) {
	f, err := root.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	count, err := io.Copy(h, f)
	if err != nil {
		return "", count, err
	}
	return hex.EncodeToString(h.Sum(nil)), count, nil
}
