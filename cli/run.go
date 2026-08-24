package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/drobilica/tarlink-data/internal/config"
	"github.com/drobilica/tarlink-data/internal/recipe"
	"github.com/drobilica/tarlink-data/internal/source"
	"github.com/drobilica/tarlink-data/internal/syncer"
)

const help = `TarLink Data resolves user-selected external application data.

Usage:
  tarlink-data sync [--dry-run] [--json]
  tarlink-data --help
  tarlink-data --version
`

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer, version string) int {
	if len(args) == 0 {
		_, _ = io.WriteString(stdout, help)
		return 0
	}
	if args[0] == "--help" || args[0] == "-h" {
		if len(args) != 1 {
			return fail(stderr, "--help does not accept arguments")
		}
		_, _ = io.WriteString(stdout, help)
		return 0
	}
	if args[0] == "--version" {
		if len(args) != 1 {
			return fail(stderr, "--version does not accept arguments")
		}
		_, _ = fmt.Fprintf(stdout, "tarlink-data %s\n", version)
		return 0
	}
	if args[0] != "sync" {
		return fail(stderr, "unknown command %q", args[0])
	}
	dryRun, jsonOutput, err := parseSyncArgs(args[1:])
	if err != nil {
		return fail(stderr, "%v", err)
	}
	paths, err := config.XDGPaths()
	if err != nil {
		return fail(stderr, "%v", err)
	}
	cfg, err := config.Load(paths.ConfigFile)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	client := source.NewHTTPClient()
	recipes, err := recipe.LoadAll(expandEntries(cfg.Recipes), client)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	providers, err := makeSources(expandEntries(cfg.Sources), paths.CacheDir, client)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	apps, err := syncer.ParseInstalled(stdin)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	if !jsonOutput {
		_, _ = io.WriteString(stderr, "Resolving external data...\n")
	}
	results, runErr := syncer.Run(apps, syncer.Options{
		DataDir: paths.DataDir, CacheDir: paths.CacheDir, Recipes: recipes, Sources: providers,
		HTTPClient: client, DryRun: dryRun,
	})
	if jsonOutput {
		payload := struct {
			Results []syncer.Result `json:"results"`
		}{Results: results}
		if err := json.NewEncoder(stdout).Encode(payload); err != nil {
			return fail(stderr, "write JSON output: %v", err)
		}
	} else {
		for _, result := range results {
			if result.Error != "" {
				_, _ = fmt.Fprintf(stdout, "%s: %s (%s)\n", result.App, result.Target, result.Status)
			} else {
				_, _ = fmt.Fprintf(stdout, "%s: %s %s\n", result.App, result.Target, result.Status)
			}
		}
		if runErr == nil {
			_, _ = io.WriteString(stdout, "Complete.\n")
		}
	}
	if runErr != nil {
		if !jsonOutput {
			_, _ = fmt.Fprintf(stderr, "tarlink-data: %v\n", runErr)
		}
		return 1
	}
	return 0
}

func parseSyncArgs(args []string) (bool, bool, error) {
	var dryRun, jsonOutput bool
	for _, arg := range args {
		switch arg {
		case "--dry-run":
			dryRun = true
		case "--json":
			jsonOutput = true
		case "--help", "-h":
			return false, false, errors.New("use tarlink-data --help")
		default:
			return false, false, fmt.Errorf("unknown sync option %q", arg)
		}
	}
	return dryRun, jsonOutput, nil
}

func expandEntries(entries []string) []string {
	result := make([]string, len(entries))
	for i, entry := range entries {
		if value, err := config.ExpandPath(entry); err == nil {
			result[i] = value
		} else {
			result[i] = entry
		}
	}
	return result
}

func makeSources(entries []string, cacheDir string, client *http.Client) ([]source.Provider, error) {
	providers := make([]source.Provider, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry, "https://") {
			provider, err := source.NewRemote(entry, cacheDir, client)
			if err != nil {
				return nil, err
			}
			providers = append(providers, provider)
			continue
		}
		if strings.Contains(entry, "://") || strings.HasPrefix(entry, "http:") {
			return nil, fmt.Errorf("data source must be a local directory or HTTPS index URL: %s", entry)
		}
		provider, err := source.NewLocal(entry, cacheDir)
		if err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}
	return providers, nil
}

func fail(stderr io.Writer, format string, args ...any) int {
	_, _ = fmt.Fprintf(stderr, "tarlink-data: "+format+"\n", args...)
	return 2
}
