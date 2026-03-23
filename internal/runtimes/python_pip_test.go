package runtimes

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestResolveVenvPathUsesDotVenvDefaults(t *testing.T) {
	workdir := t.TempDir()
	absoluteTarget := filepath.Join(workdir, "absolute-env")

	testCases := []struct {
		name string
		path string
		want string
	}{
		{
			name: "empty path uses dot venv",
			path: "",
			want: filepath.Join(workdir, ".venv"),
		},
		{
			name: "dot path uses dot venv",
			path: ".",
			want: filepath.Join(workdir, ".venv"),
		},
		{
			name: "custom relative path is preserved",
			path: ".venv-dev",
			want: filepath.Join(workdir, ".venv-dev"),
		},
		{
			name: "nested relative path is preserved",
			path: filepath.Join("envs", "demo"),
			want: filepath.Join(workdir, "envs", "demo"),
		},
		{
			name: "absolute path is preserved",
			path: absoluteTarget,
			want: absoluteTarget,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			got, err := resolveVenvPath(workdir, testCase.path)
			if err != nil {
				t.Fatalf("resolveVenvPath failed: %v", err)
			}
			if got != testCase.want {
				t.Fatalf("unexpected venv path: got %s want %s", got, testCase.want)
			}
		})
	}
}

func TestManagerPipInstallPrefersVirtualEnvOverNearestDotVenv(t *testing.T) {
	tempDir := t.TempDir()
	manager := newTestManager(tempDir, nil)

	workdir := filepath.Join(tempDir, "project", "nested")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("failed to create workdir: %v", err)
	}
	manager.getwdFunc = func() (string, error) { return workdir, nil }

	projectVenv := filepath.Join(tempDir, "project", ".venv")
	virtualEnv := filepath.Join(tempDir, "external-venv")
	writePythonVenvFixture(t, projectVenv)
	writePythonVenvFixture(t, virtualEnv)
	t.Setenv("VIRTUAL_ENV", virtualEnv)

	wheelURL, expectedWheelPath := configurePipFixture(t, manager)

	var got commandSpec
	manager.runCommandFunc = func(_ context.Context, spec commandSpec) error {
		got = spec
		return nil
	}

	if err := manager.PipInstall(context.Background(), PipInstallOptions{Args: []string{"requests"}}); err != nil {
		t.Fatalf("pip install failed: %v", err)
	}

	if got.Path != filepath.Join(virtualEnv, "Scripts", "python.exe") {
		t.Fatalf("expected VIRTUAL_ENV interpreter, got %s", got.Path)
	}
	if got.Dir != workdir {
		t.Fatalf("expected command dir %s, got %s", workdir, got.Dir)
	}
	if !containsExactEnvEntry(got.Env, "VIRTUAL_ENV="+virtualEnv) {
		t.Fatalf("expected VIRTUAL_ENV=%s in command env", virtualEnv)
	}

	wantArgs := []string{"-c", pipInstallRunnerScript, expectedWheelPath, "--", "install", "requests"}
	if !reflect.DeepEqual(got.Args, wantArgs) {
		t.Fatalf("unexpected command args: got %#v want %#v", got.Args, wantArgs)
	}

	if _, err := os.Stat(expectedWheelPath); err != nil {
		t.Fatalf("expected cached pip wheel %s to exist: %v", expectedWheelPath, err)
	}
	if !strings.Contains(expectedWheelPath, filepath.Base(wheelURL)) {
		t.Fatalf("expected cache path %s to include wheel file name", expectedWheelPath)
	}
}

func TestManagerPipInstallFindsNearestDotVenv(t *testing.T) {
	tempDir := t.TempDir()
	manager := newTestManager(tempDir, nil)

	projectDir := filepath.Join(tempDir, "workspace")
	workdir := filepath.Join(projectDir, "src", "pkg")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("failed to create workdir: %v", err)
	}
	manager.getwdFunc = func() (string, error) { return workdir, nil }

	writePythonVenvFixture(t, filepath.Join(tempDir, ".venv"))
	projectVenv := filepath.Join(projectDir, ".venv")
	writePythonVenvFixture(t, projectVenv)
	t.Setenv("VIRTUAL_ENV", "")

	_, _ = configurePipFixture(t, manager)

	var got commandSpec
	manager.runCommandFunc = func(_ context.Context, spec commandSpec) error {
		got = spec
		return nil
	}

	if err := manager.PipInstall(context.Background(), PipInstallOptions{
		Args: []string{"-r", "requirements.txt", "--upgrade"},
	}); err != nil {
		t.Fatalf("pip install failed: %v", err)
	}

	if got.Path != filepath.Join(projectVenv, "Scripts", "python.exe") {
		t.Fatalf("expected nearest .venv interpreter, got %s", got.Path)
	}
	wantSuffix := []string{"--", "install", "-r", "requirements.txt", "--upgrade"}
	if len(got.Args) < len(wantSuffix) || !reflect.DeepEqual(got.Args[len(got.Args)-len(wantSuffix):], wantSuffix) {
		t.Fatalf("expected pip args suffix %#v, got %#v", wantSuffix, got.Args)
	}
}

func TestManagerPipInstallErrorsWhenVenvIsMissing(t *testing.T) {
	tempDir := t.TempDir()
	manager := newTestManager(tempDir, nil)

	workdir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("failed to create workdir: %v", err)
	}
	manager.getwdFunc = func() (string, error) { return workdir, nil }
	manager.runCommandFunc = func(_ context.Context, _ commandSpec) error {
		t.Fatalf("runCommand should not be called when no venv is available")
		return nil
	}
	t.Setenv("VIRTUAL_ENV", "")

	err := manager.PipInstall(context.Background(), PipInstallOptions{Args: []string{"requests"}})
	if err == nil {
		t.Fatalf("expected missing venv error")
	}
	if !strings.Contains(err.Error(), "requires an existing Python virtual environment") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "toolmesh python venv") || !strings.Contains(err.Error(), "toolmesh venv python") {
		t.Fatalf("expected create-venv hint, got %v", err)
	}
}

func TestManagerPipInstallCachesPipWheel(t *testing.T) {
	tempDir := t.TempDir()
	manager := newTestManager(tempDir, nil)

	workdir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("failed to create workdir: %v", err)
	}
	manager.getwdFunc = func() (string, error) { return workdir, nil }
	writePythonVenvFixture(t, filepath.Join(workdir, ".venv"))
	t.Setenv("VIRTUAL_ENV", "")

	wheelBytes := buildZipArchive(t, map[string]string{
		"pip/__init__.py": "__version__ = '25.0'",
	})
	wheelVersion := "25.0"
	wheelURL := "https://files.pythonhosted.org/packages/pip-25.0-py3-none-any.whl"
	counts := map[string]int{}
	manager.client = &http.Client{Transport: &countingFixtureTransport{
		counts: counts,
		fixtures: fixtureTransport{
			pipMetadataURL: {
				contentType: "application/json",
				body: []byte(fmt.Sprintf(`{
					"info": {"version": %q},
					"urls": [{
						"filename": %q,
						"url": %q,
						"packagetype": "bdist_wheel",
						"size": %d,
						"digests": {"sha256": %q}
					}]
				}`, wheelVersion, "pip-"+wheelVersion+"-py3-none-any.whl", wheelURL, len(wheelBytes), checksumHex(wheelBytes))),
			},
			wheelURL: {
				contentType: "application/octet-stream",
				body:        wheelBytes,
			},
		},
	}}
	manager.runCommandFunc = func(_ context.Context, _ commandSpec) error { return nil }

	for i := 0; i < 2; i++ {
		if err := manager.PipInstall(context.Background(), PipInstallOptions{Args: []string{"requests"}}); err != nil {
			t.Fatalf("pip install failed on attempt %d: %v", i+1, err)
		}
	}

	if counts[pipMetadataURL] != 1 {
		t.Fatalf("expected pip metadata to be fetched once, got %d", counts[pipMetadataURL])
	}
	if counts[wheelURL] != 1 {
		t.Fatalf("expected pip wheel to be downloaded once, got %d", counts[wheelURL])
	}
}

func configurePipFixture(t *testing.T, manager *Manager) (string, string) {
	t.Helper()

	wheelBytes := buildZipArchive(t, map[string]string{
		"pip/__init__.py": "__version__ = '25.0'",
	})
	wheelURL := "https://files.pythonhosted.org/packages/pip-25.0-py3-none-any.whl"
	manager.client = &http.Client{Transport: fixtureTransport{
		pipMetadataURL: {
			contentType: "application/json",
			body: []byte(fmt.Sprintf(`{
				"info": {"version": "25.0"},
				"urls": [{
					"filename": "pip-25.0-py3-none-any.whl",
					"url": %q,
					"packagetype": "bdist_wheel",
					"size": %d,
					"digests": {"sha256": %q}
				}]
			}`, wheelURL, len(wheelBytes), checksumHex(wheelBytes))),
		},
		wheelURL: {
			contentType: "application/octet-stream",
			body:        wheelBytes,
		},
	}}

	cachePath, err := artifactCachePath(manager.paths.CacheRoot, wheelURL)
	if err != nil {
		t.Fatalf("failed to resolve cache path: %v", err)
	}
	return wheelURL, cachePath
}

func writePythonVenvFixture(t *testing.T, root string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(root, "Scripts"), 0o755); err != nil {
		t.Fatalf("failed to create venv scripts dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pyvenv.cfg"), []byte("home = C:\\toolmesh\\python\r\n"), 0o644); err != nil {
		t.Fatalf("failed to write pyvenv.cfg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Scripts", "python.exe"), []byte("python"), 0o755); err != nil {
		t.Fatalf("failed to write python.exe: %v", err)
	}
}

func containsExactEnvEntry(environment []string, want string) bool {
	for _, item := range environment {
		if item == want {
			return true
		}
	}
	return false
}

type countingFixtureTransport struct {
	counts   map[string]int
	fixtures fixtureTransport
}

func (t *countingFixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.counts[request.URL.String()]++
	return fixtureTransport(t.fixtures).RoundTrip(request)
}
