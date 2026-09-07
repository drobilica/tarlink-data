package syncer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/drobilica/tarlink-data/internal/recipe"
	"github.com/drobilica/tarlink-data/internal/source"
	"github.com/drobilica/tarlink-data/internal/storage"
)

const maxInputBytes = 1 << 20

type installedContract struct {
	Version      int             `json:"version"`
	Applications *[]InstalledApp `json:"applications"`
}

type InstalledApp struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type Result struct {
	App    string `json:"app"`
	Target string `json:"target"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type Options struct {
	DataDir    string
	CacheDir   string
	Recipes    map[string]recipe.Recipe
	Sources    []source.Provider
	HTTPClient *http.Client
	DryRun     bool
}

func ParseInstalled(r io.Reader) ([]InstalledApp, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxInputBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read installed application input: %w", err)
	}
	if len(data) > maxInputBytes {
		return nil, fmt.Errorf("installed application input is too large")
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	var contract installedContract
	if err := dec.Decode(&contract); err != nil {
		return nil, fmt.Errorf("parse installed application JSON: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("installed application JSON has trailing data")
		}
		return nil, fmt.Errorf("parse installed application JSON: %w", err)
	}
	if contract.Version != 1 {
		return nil, fmt.Errorf("unsupported installed application contract version %d", contract.Version)
	}
	if contract.Applications == nil {
		return nil, fmt.Errorf("installed application contract is missing applications")
	}
	seen := make(map[string]struct{}, len(*contract.Applications))
	result := make([]InstalledApp, 0, len(*contract.Applications))
	for _, app := range *contract.Applications {
		if app.ID == "" {
			return nil, fmt.Errorf("installed application has empty id")
		}
		if app.Version == "" {
			return nil, fmt.Errorf("installed application %q has empty version", app.ID)
		}
		if _, exists := seen[app.ID]; exists {
			return nil, fmt.Errorf("installed application %q is duplicated", app.ID)
		}
		seen[app.ID] = struct{}{}
		result = append(result, app)
	}
	return result, nil
}

func Run(apps []InstalledApp, opts Options) ([]Result, error) {
	var results []Result
	var runErr error
	for _, app := range apps {
		r, exists := opts.Recipes[app.ID]
		if !exists {
			continue
		}
		for _, requirement := range recipe.SortedRequirements(r) {
			result, err := resolveRequirement(app.ID, requirement, opts)
			results = append(results, result)
			if err != nil {
				runErr = errors.Join(runErr, err)
			}
		}
	}
	return results, runErr
}

func resolveRequirement(app string, req recipe.Requirement, opts Options) (Result, error) {
	result := Result{App: app, Target: req.Target}
	destination, err := storage.Inspect(opts.DataDir, app, req.Target, req.Accepted)
	if err != nil {
		result.Status = "conflict"
		result.Error = err.Error()
		return result, err
	}
	if destination.State == storage.Satisfied {
		result.Status = "satisfied"
		return result, nil
	}
	if destination.State == storage.Conflict {
		result.Status = "conflict"
		result.Error = "destination contains unaccepted data"
		return result, storage.ErrConflict
	}

	for _, provider := range opts.Sources {
		for _, variant := range req.Accepted {
			refreshed := false
			for {
				entries, err := provider.Lookup(variant.SHA256)
				if err != nil {
					result.Status = "missing"
					result.Error = err.Error()
					return result, err
				}
				candidateFailed := false
				for _, entry := range entries {
					if entry.Size != variant.Size {
						continue
					}
					if opts.DryRun {
						result.Status = "would-copy"
						return result, nil
					}
					err := storage.Materialize(opts.DataDir, app, req.Target, variant, func() (io.ReadCloser, error) {
						return provider.Open(entry)
					}, false)
					if err == nil {
						result.Status = "copied"
						return result, nil
					}
					candidateFailed = true
					provider.Invalidate(entry)
				}
				if !candidateFailed || refreshed {
					break
				}
				didRefresh, refreshErr := provider.RefreshOnce()
				if refreshErr != nil {
					result.Status = "missing"
					result.Error = refreshErr.Error()
					return result, refreshErr
				}
				if !didRefresh {
					break
				}
				refreshed = true
			}
		}
	}
	result.Status = "missing"
	result.Error = fmt.Sprintf("no verified source data for %s", req.Target)
	return result, errors.New(result.Error)
}
