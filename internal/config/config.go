package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

const maxConfigBytes = 1 << 20

type Config struct {
	Version int      `yaml:"version"`
	Recipes []string `yaml:"recipes"`
	Sources []string `yaml:"sources"`
}

type Paths struct {
	ConfigFile string
	CacheDir   string
	DataDir    string
}

func XDGPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("find home directory: %w", err)
	}
	configHome, err := xdgDir("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if err != nil {
		return Paths{}, err
	}
	cacheHome, err := xdgDir("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	if err != nil {
		return Paths{}, err
	}
	dataHome, err := xdgDir("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		ConfigFile: filepath.Join(configHome, "tarlink-data", "config.yaml"),
		CacheDir:   filepath.Join(cacheHome, "tarlink-data"),
		DataDir:    filepath.Join(dataHome, "tarlink-data"),
	}, nil
}

func xdgDir(name, fallback string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("%s must be an absolute path", name)
	}
	return filepath.Clean(value), nil
}

func ExpandPath(value string) (string, error) {
	if strings.HasPrefix(value, "~/") || value == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand %q: %w", value, err)
		}
		if value == "~" {
			return home, nil
		}
		return filepath.Join(home, value[2:]), nil
	}
	return value, nil
}

func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	if len(data) > maxConfigBytes {
		return Config{}, fmt.Errorf("config %s is too large", path)
	}
	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, fmt.Errorf("config %s contains multiple documents", path)
		}
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	if cfg.Version != 1 {
		return Config{}, fmt.Errorf("config version must be 1")
	}
	if cfg.Recipes == nil {
		cfg.Recipes = []string{}
	}
	if cfg.Sources == nil {
		cfg.Sources = []string{}
	}
	for i, value := range append(append([]string{}, cfg.Recipes...), cfg.Sources...) {
		if strings.TrimSpace(value) == "" {
			return Config{}, fmt.Errorf("config path %d is empty", i)
		}
	}
	return cfg, nil
}
