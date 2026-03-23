package runtimes

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Paths struct {
	ConfigDir   string
	DataDir     string
	StatePath   string
	InstallRoot string
	CacheRoot   string
	ShimsDir    string
}

type State struct {
	Active map[string]string `json:"active"`
}

func DefaultPaths() (Paths, error) {
	homeOverride := os.Getenv("TOOLMESH_HOME")
	dataOverride := os.Getenv("TOOLMESH_DATA_DIR")

	var configDir string
	var dataDir string

	if homeOverride != "" {
		configDir = homeOverride
		if dataOverride != "" {
			dataDir = dataOverride
		} else {
			dataDir = homeOverride
		}
	} else {
		userConfigDir, err := os.UserConfigDir()
		if err != nil {
			return Paths{}, err
		}
		userCacheDir, err := os.UserCacheDir()
		if err != nil {
			return Paths{}, err
		}

		configDir = filepath.Join(userConfigDir, "toolmesh")
		if dataOverride != "" {
			dataDir = dataOverride
		} else {
			dataDir = filepath.Join(userCacheDir, "toolmesh")
		}
	}

	return Paths{
		ConfigDir:   configDir,
		DataDir:     dataDir,
		StatePath:   filepath.Join(configDir, "state.json"),
		InstallRoot: filepath.Join(dataDir, "runtimes"),
		CacheRoot:   filepath.Join(dataDir, "downloads"),
		ShimsDir:    filepath.Join(dataDir, "shims"),
	}, nil
}

func ensurePaths(paths Paths) error {
	if err := os.MkdirAll(paths.ConfigDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.InstallRoot, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.CacheRoot, 0o755); err != nil {
		return err
	}
	if paths.ShimsDir != "" {
		if err := os.MkdirAll(paths.ShimsDir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func loadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{Active: map[string]string{}}, nil
		}
		return State{}, err
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, err
	}
	if state.Active == nil {
		state.Active = map[string]string{}
	}
	return state, nil
}

func saveState(path string, state State) error {
	if state.Active == nil {
		state.Active = map[string]string{}
	}
	return writeJSONAtomic(path, state)
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tempFile, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}

	tempPath := tempFile.Name()
	if _, err := tempFile.Write(data); err != nil {
		tempFile.Close()
		os.Remove(tempPath)
		return err
	}
	if err := tempFile.Close(); err != nil {
		os.Remove(tempPath)
		return err
	}

	if err := os.Rename(tempPath, path); err != nil {
		os.Remove(tempPath)
		return err
	}
	return nil
}
