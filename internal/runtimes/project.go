package runtimes

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const projectConfigFileName = ".toolmesh.json"

type ProjectConfig struct {
	Runtimes map[string]string `json:"runtimes"`
}

type ProjectConfigFile struct {
	Path   string
	Config ProjectConfig
}

func loadProjectConfig(path string) (ProjectConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ProjectConfig{}, err
	}

	var config ProjectConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return ProjectConfig{}, err
	}
	if config.Runtimes == nil {
		config.Runtimes = map[string]string{}
	}
	return config, nil
}

func saveProjectConfig(path string, config ProjectConfig) error {
	if config.Runtimes == nil {
		config.Runtimes = map[string]string{}
	}
	return writeJSONAtomic(path, config)
}

func findProjectConfig(startDir string) (ProjectConfigFile, error) {
	if strings.TrimSpace(startDir) == "" {
		return ProjectConfigFile{}, os.ErrNotExist
	}

	currentDir, err := filepath.Abs(startDir)
	if err != nil {
		return ProjectConfigFile{}, err
	}

	for {
		candidate := filepath.Join(currentDir, projectConfigFileName)
		config, err := loadProjectConfig(candidate)
		if err == nil {
			return ProjectConfigFile{
				Path:   candidate,
				Config: config,
			}, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return ProjectConfigFile{}, err
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			return ProjectConfigFile{}, os.ErrNotExist
		}
		currentDir = parentDir
	}
}

func projectConfigTarget(startDir string) (ProjectConfigFile, error) {
	configFile, err := findProjectConfig(startDir)
	if err == nil {
		return configFile, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return ProjectConfigFile{}, err
	}

	currentDir, err := filepath.Abs(startDir)
	if err != nil {
		return ProjectConfigFile{}, err
	}

	return ProjectConfigFile{
		Path: filepath.Join(currentDir, projectConfigFileName),
		Config: ProjectConfig{
			Runtimes: map[string]string{},
		},
	}, nil
}
