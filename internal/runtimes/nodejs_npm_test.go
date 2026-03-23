package runtimes

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestManagerNpmInstallUsesSelectedNodeRuntime(t *testing.T) {
	tempDir := t.TempDir()
	manager := newTestManager(tempDir, NewBuiltinRegistry())

	workdir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("failed to create workdir: %v", err)
	}
	manager.getwdFunc = func() (string, error) { return workdir, nil }

	writeInstalledRuntime(t, manager, "nodejs", "20.12.2", "node.exe", "node")
	npmPath := filepath.Join(manager.installDir("nodejs", "20.12.2"), "npm.cmd")
	if err := os.WriteFile(npmPath, []byte("@echo off\r\necho npm\r\n"), 0o755); err != nil {
		t.Fatalf("failed to write npm launcher: %v", err)
	}

	if err := saveState(manager.paths.StatePath, State{
		Active: map[string]string{"nodejs": "20.12.2"},
	}); err != nil {
		t.Fatalf("failed to seed state: %v", err)
	}

	var got commandSpec
	manager.runCommandFunc = func(_ context.Context, spec commandSpec) error {
		got = spec
		return nil
	}

	if err := manager.NpmInstall(context.Background(), PackageInstallOptions{
		Args: []string{"react", "--save-dev"},
	}); err != nil {
		t.Fatalf("npm install failed: %v", err)
	}

	if got.Path != npmPath {
		t.Fatalf("expected npm path %s, got %s", npmPath, got.Path)
	}
	if got.Dir != workdir {
		t.Fatalf("expected workdir %s, got %s", workdir, got.Dir)
	}

	wantArgs := []string{"install", "react", "--save-dev"}
	if !reflect.DeepEqual(got.Args, wantArgs) {
		t.Fatalf("unexpected npm args: got %#v want %#v", got.Args, wantArgs)
	}

	pathValue := envValue(got.Env, "PATH")
	wantPrefix := filepath.Dir(npmPath)
	if pathValue == "" {
		t.Fatalf("expected PATH to be set for npm command")
	}
	if !strings.EqualFold(pathValue, wantPrefix) && !strings.HasPrefix(strings.ToLower(pathValue), strings.ToLower(wantPrefix)+string(os.PathListSeparator)) {
		t.Fatalf("expected PATH to start with %s, got %s", wantPrefix, pathValue)
	}
}

func TestManagerNpmInstallRequiresSelectedNodeRuntime(t *testing.T) {
	tempDir := t.TempDir()
	manager := newTestManager(tempDir, NewBuiltinRegistry())

	workdir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("failed to create workdir: %v", err)
	}
	manager.getwdFunc = func() (string, error) { return workdir, nil }

	err := manager.NpmInstall(context.Background(), PackageInstallOptions{Args: []string{"react"}})
	if err == nil {
		t.Fatalf("expected missing node selection error")
	}
	if !strings.Contains(err.Error(), "requires an active or project-selected nodejs runtime") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestManagerNpmInstallErrorsWhenRuntimeHasNoNpm(t *testing.T) {
	tempDir := t.TempDir()
	manager := newTestManager(tempDir, NewBuiltinRegistry())

	workdir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("failed to create workdir: %v", err)
	}
	manager.getwdFunc = func() (string, error) { return workdir, nil }

	writeInstalledRuntime(t, manager, "nodejs", "20.12.2", "node.exe", "node")
	if err := saveState(manager.paths.StatePath, State{
		Active: map[string]string{"nodejs": "20.12.2"},
	}); err != nil {
		t.Fatalf("failed to seed state: %v", err)
	}

	err := manager.NpmInstall(context.Background(), PackageInstallOptions{Args: []string{"react"}})
	if err == nil {
		t.Fatalf("expected missing npm error")
	}
	if !strings.Contains(err.Error(), "does not include npm") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func envValue(environment []string, key string) string {
	for _, item := range environment {
		currentKey, value, found := strings.Cut(item, "=")
		if !found {
			continue
		}
		if strings.EqualFold(currentKey, key) {
			return value
		}
	}
	return ""
}
