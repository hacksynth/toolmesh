package runtimes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type nodejsNpmInstaller struct{}

func (nodejsProvider) PackageInstallers() []PackageInstaller {
	return []PackageInstaller{nodejsNpmInstaller{}}
}

func (nodejsNpmInstaller) Name() string {
	return "npm"
}

func (m *Manager) NpmInstall(ctx context.Context, options PackageInstallOptions) error {
	return m.InstallPackages(ctx, "npm", options)
}

func (nodejsNpmInstaller) Install(ctx context.Context, manager *Manager, options PackageInstallOptions) error {
	if len(options.Args) == 0 {
		return fmt.Errorf("npm install arguments are required")
	}

	workdir, err := manager.getwd()
	if err != nil {
		return err
	}

	current, err := manager.Current("nodejs")
	if err != nil {
		return fmt.Errorf("toolmesh npm install requires an active or project-selected nodejs runtime: %w", err)
	}

	npmPath, err := resolveNodejsNpmPath(current)
	if err != nil {
		return err
	}

	command := buildNpmInstallCommand(npmPath, workdir, options)
	if err := manager.runCommand(ctx, command); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return fmt.Errorf("npm install failed")
		}
		return fmt.Errorf("failed to execute npm install: %w", err)
	}

	return nil
}

func resolveNodejsNpmPath(current InstalledRuntime) (string, error) {
	baseExecutable := strings.TrimSpace(current.Executable)
	if baseExecutable == "" {
		return "", fmt.Errorf("nodejs runtime executable is not configured")
	}
	if err := requireRegularFile(baseExecutable); err != nil {
		return "", fmt.Errorf("nodejs runtime executable is unavailable: %w", err)
	}

	commandDir := filepath.Dir(baseExecutable)
	for _, name := range []string{"npm.cmd", "npm.exe", "npm"} {
		candidate := filepath.Join(commandDir, name)
		info, err := os.Stat(candidate)
		if err == nil {
			if info.IsDir() {
				return "", fmt.Errorf("%s is a directory", candidate)
			}
			return candidate, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}

	return "", fmt.Errorf("runtime %s %s does not include npm", current.Runtime, current.Version)
}

func buildNpmInstallCommand(npmPath string, workdir string, options PackageInstallOptions) commandSpec {
	args := make([]string, 0, len(options.Args)+1)
	args = append(args, "install")
	args = append(args, options.Args...)

	return commandSpec{
		Path:   npmPath,
		Args:   args,
		Dir:    workdir,
		Env:    prependPathEntries(os.Environ(), filepath.Dir(npmPath)),
		Stdin:  options.Stdin,
		Stdout: options.Stdout,
		Stderr: options.Stderr,
	}
}

func prependPathEntries(environment []string, pathEntries ...string) []string {
	filteredEntries := make([]string, 0, len(pathEntries))
	for _, entry := range pathEntries {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		filteredEntries = append(filteredEntries, entry)
	}
	if len(filteredEntries) == 0 {
		return append([]string(nil), environment...)
	}

	pathKey := "PATH"
	currentValue := ""
	index := -1
	for i, item := range environment {
		key, value, found := strings.Cut(item, "=")
		if !found {
			continue
		}
		if strings.EqualFold(key, "PATH") {
			pathKey = key
			currentValue = value
			index = i
			break
		}
	}

	merged := strings.Join(filteredEntries, string(os.PathListSeparator))
	if currentValue != "" {
		merged += string(os.PathListSeparator) + currentValue
	}

	entry := pathKey + "=" + merged
	updated := append([]string(nil), environment...)
	if index >= 0 {
		updated[index] = entry
		return updated
	}

	return append(updated, entry)
}
