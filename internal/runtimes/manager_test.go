package runtimes

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeProvider struct {
	serverURL string
}

func (fakeProvider) Name() string {
	return "fake"
}

func (fakeProvider) Aliases() []string {
	return []string{"fk"}
}

func (fakeProvider) NormalizeVersion(version string) (string, error) {
	return normalizeVersion(version, "fake")
}

func (p fakeProvider) ResolvePackage(_ context.Context, version string, _ Platform, _ MetadataSource) (PackageSpec, error) {
	return PackageSpec{
		Version: version,
		URL:     p.serverURL + "/fake.zip",
		Archive: ArchiveFormatZip,
	}, nil
}

func (fakeProvider) FinalizeInstall(installDir string, _ PackageSpec) (InstallMetadata, error) {
	return finalizeByExecutable(installDir, "fake.exe", 1)
}

type fakeRemoteProvider struct {
	fakeProvider
	versions []RemoteVersion
}

type httpResponseFixture struct {
	statusCode  int
	contentType string
	body        []byte
}

type fixtureTransport map[string]httpResponseFixture

type recordingDownloadObserver struct {
	events []DownloadProgressEvent
}

func (t fixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	fixture, ok := t[request.URL.String()]
	if !ok {
		return nil, fmt.Errorf("unexpected request: %s", request.URL.String())
	}

	statusCode := fixture.statusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}

	header := make(http.Header)
	if fixture.contentType != "" {
		header.Set("Content-Type", fixture.contentType)
	}

	return &http.Response{
		StatusCode: statusCode,
		Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(fixture.body)),
		Request:    request,
	}, nil
}

func (r *recordingDownloadObserver) OnDownloadProgress(event DownloadProgressEvent) {
	r.events = append(r.events, event)
}

type builtinRuntimeCoverageFixture struct {
	commandRuntime       string
	installVersion       string
	wantInstalledVersion string
	listRemoteSelector   string
	wantRemoteVersions   []RemoteVersion
	latestSelector       string
	wantLatestVersion    RemoteVersion
	expectedExecutable   string
	httpResponseFixture  map[string]httpResponseFixture
}

func (p fakeRemoteProvider) ListRemoteVersions(_ context.Context, _ Platform, _ MetadataSource) ([]RemoteVersion, error) {
	versions := append([]RemoteVersion(nil), p.versions...)
	sortRemoteVersions(versions)
	return versions, nil
}

func TestBuiltinRuntimeCoverageForCoreCommands(t *testing.T) {
	platform := Platform{OS: "windows", Arch: "amd64"}
	fixtures := builtinRuntimeCoverageFixtures(t, platform)
	registry := NewBuiltinRegistry()
	providers := registry.Providers()

	if len(fixtures) != len(providers) {
		t.Fatalf("builtin runtime fixture count mismatch: got %d fixtures for %d providers", len(fixtures), len(providers))
	}

	for _, provider := range providers {
		provider := provider
		fixture, ok := fixtures[provider.Name()]
		if !ok {
			t.Fatalf("missing builtin runtime fixture for %s", provider.Name())
		}

		t.Run(provider.Name(), func(t *testing.T) {
			manager := newTestManager(t.TempDir(), registry)
			manager.platform = platform
			manager.client = &http.Client{Transport: fixtureTransport(fixture.httpResponseFixture)}

			remoteVersions, err := manager.ListRemote(context.Background(), fixture.commandRuntime, fixture.listRemoteSelector)
			if err != nil {
				t.Fatalf("list-remote failed: %v", err)
			}
			if !reflect.DeepEqual(remoteVersions, fixture.wantRemoteVersions) {
				t.Fatalf("unexpected remote versions: got %#v want %#v", remoteVersions, fixture.wantRemoteVersions)
			}

			latest, err := manager.Latest(context.Background(), fixture.commandRuntime, fixture.latestSelector)
			if err != nil {
				t.Fatalf("latest failed: %v", err)
			}
			if latest != fixture.wantLatestVersion {
				t.Fatalf("unexpected latest version: got %#v want %#v", latest, fixture.wantLatestVersion)
			}

			installed, err := manager.Install(context.Background(), fixture.commandRuntime, fixture.installVersion)
			if err != nil {
				t.Fatalf("install failed: %v", err)
			}
			wantInstalledVersion := fixture.wantInstalledVersion
			if wantInstalledVersion == "" {
				wantInstalledVersion = fixture.installVersion
			}
			if installed.Runtime != provider.Name() || installed.Version != wantInstalledVersion {
				t.Fatalf("unexpected install result: %#v", installed)
			}
			if filepath.Base(installed.Executable) != fixture.expectedExecutable {
				t.Fatalf("unexpected executable path: %s", installed.Executable)
			}
			if _, err := os.Stat(installed.Executable); err != nil {
				t.Fatalf("expected executable to exist: %v", err)
			}

			listed, err := manager.List(fixture.commandRuntime)
			if err != nil {
				t.Fatalf("list failed: %v", err)
			}
			if len(listed) != 1 {
				t.Fatalf("expected 1 installed runtime, got %d", len(listed))
			}
			if listed[0].Runtime != provider.Name() || listed[0].Version != wantInstalledVersion {
				t.Fatalf("unexpected listed runtime: %#v", listed[0])
			}
			if listed[0].Active {
				t.Fatalf("expected installed runtime to remain inactive after list")
			}
		})
	}
}

func TestManagerInstallUseCurrentAndRemove(t *testing.T) {
	archiveBytes := buildZipArchive(t, map[string]string{
		"fake-1.0.0/bin/fake.exe": "runtime",
	})

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/zip")
		_, _ = writer.Write(archiveBytes)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	registry, err := NewRegistry(fakeProvider{serverURL: server.URL})
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	manager := newTestManager(tempDir, registry)
	manager.client = server.Client()

	installed, err := manager.Install(context.Background(), "fake", "1.0.0")
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}

	if installed.Runtime != "fake" || installed.Version != "1.0.0" {
		t.Fatalf("unexpected install result: %#v", installed)
	}

	if _, err := os.Stat(installed.Executable); err != nil {
		t.Fatalf("expected executable to exist: %v", err)
	}

	listed, err := manager.List("fake")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected 1 installed runtime, got %d", len(listed))
	}

	current, err := manager.Use("fake", "1.0.0")
	if err != nil {
		t.Fatalf("use failed: %v", err)
	}
	if !current.Active {
		t.Fatalf("expected runtime to become active")
	}

	active, err := manager.Current("fake")
	if err != nil {
		t.Fatalf("current failed: %v", err)
	}
	if active.Version != "1.0.0" {
		t.Fatalf("unexpected current version: %#v", active)
	}

	if err := manager.Remove("fake", "1.0.0"); err != nil {
		t.Fatalf("remove failed: %v", err)
	}

	listed, err = manager.List("fake")
	if err != nil {
		t.Fatalf("list after remove failed: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("expected no installed runtimes after remove, got %d", len(listed))
	}
}

func TestManagerProjectConfigOverridesGlobalCurrent(t *testing.T) {
	tempDir := t.TempDir()
	registry, err := NewRegistry(fakeProvider{})
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	manager := newTestManager(tempDir, registry)
	writeInstalledRuntime(t, manager, "fake", "1.0.0", "bin/fake.cmd", "@echo off\r\necho fake-1.0.0\r\n")
	writeInstalledRuntime(t, manager, "fake", "2.0.0", "bin/fake.cmd", "@echo off\r\necho fake-2.0.0\r\n")

	if _, err := manager.Use("fake", "1.0.0"); err != nil {
		t.Fatalf("use failed: %v", err)
	}

	projectDir := filepath.Join(tempDir, "workspace")
	if err := os.MkdirAll(filepath.Join(projectDir, "nested"), 0o755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}
	if err := saveProjectConfig(filepath.Join(projectDir, projectConfigFileName), ProjectConfig{
		Runtimes: map[string]string{"fake": "2.0.0"},
	}); err != nil {
		t.Fatalf("failed to save project config: %v", err)
	}
	manager.getwdFunc = func() (string, error) {
		return filepath.Join(projectDir, "nested"), nil
	}

	current, err := manager.Current("fake")
	if err != nil {
		t.Fatalf("current failed: %v", err)
	}
	if current.Version != "2.0.0" {
		t.Fatalf("expected project version 2.0.0, got %s", current.Version)
	}

	listed, err := manager.List("fake")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("expected 2 installed runtimes, got %d", len(listed))
	}
	if !listed[0].Active || listed[0].Version != "2.0.0" {
		t.Fatalf("expected project version to be marked active, got %#v", listed)
	}
}

func TestManagerCreateVenvUsesActivePythonRuntime(t *testing.T) {
	tempDir := t.TempDir()
	manager := newTestManager(tempDir, NewBuiltinRegistry())
	writeInstalledRuntime(t, manager, "python", "3.12.2", "python.exe", "python")
	expectedWheelPaths := configureVirtualenvBootstrapFixture(t, manager)

	if err := saveState(manager.paths.StatePath, State{
		Active: map[string]string{"python": "3.12.2"},
	}); err != nil {
		t.Fatalf("failed to seed state: %v", err)
	}

	var got commandSpec
	manager.runCommandFunc = func(_ context.Context, spec commandSpec) error {
		got = spec
		writePythonVenvFixture(t, filepath.Join(tempDir, ".venv"))
		return nil
	}

	created, err := manager.CreateVenv(context.Background(), "py", "")
	if err != nil {
		t.Fatalf("create venv failed: %v", err)
	}

	wantPath := filepath.Join(tempDir, ".venv")
	if created.Runtime != "python" || created.Version != "3.12.2" {
		t.Fatalf("unexpected venv result: %#v", created)
	}
	if created.Path != wantPath {
		t.Fatalf("expected venv path %s, got %s", wantPath, created.Path)
	}
	if created.Executable != filepath.Join(wantPath, "Scripts", "python.exe") {
		t.Fatalf("expected venv executable %s, got %s", filepath.Join(wantPath, "Scripts", "python.exe"), created.Executable)
	}

	if got.Path != filepath.Join(manager.installDir("python", "3.12.2"), "python.exe") {
		t.Fatalf("expected bootstrap interpreter %s, got %s", filepath.Join(manager.installDir("python", "3.12.2"), "python.exe"), got.Path)
	}
	if got.Dir != tempDir {
		t.Fatalf("expected bootstrap dir %s, got %s", tempDir, got.Dir)
	}
	if !containsExactEnvEntry(got.Env, "VIRTUAL_ENV=") {
		t.Fatalf("expected VIRTUAL_ENV to be cleared in bootstrap command")
	}

	wantArgs := append([]string{"-c", pythonVenvRunnerScript}, expectedWheelPaths...)
	wantArgs = append(wantArgs, "--", wantPath)
	if !reflect.DeepEqual(got.Args, wantArgs) {
		t.Fatalf("unexpected bootstrap args: got %#v want %#v", got.Args, wantArgs)
	}

	for _, wheelPath := range expectedWheelPaths {
		if _, err := os.Stat(wheelPath); err != nil {
			t.Fatalf("expected cached wheel %s to exist: %v", wheelPath, err)
		}
	}
	for _, path := range []string{
		filepath.Join(wantPath, "pyvenv.cfg"),
		filepath.Join(wantPath, "Scripts", "python.exe"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected venv file %s to exist: %v", path, err)
		}
	}
}

func TestManagerCreateVenvTreatsDotAsDefaultVenvDirectory(t *testing.T) {
	tempDir := t.TempDir()
	manager := newTestManager(tempDir, NewBuiltinRegistry())
	writeInstalledRuntime(t, manager, "python", "3.12.2", "python.exe", "python")
	_ = configureVirtualenvBootstrapFixture(t, manager)

	if err := saveState(manager.paths.StatePath, State{
		Active: map[string]string{"python": "3.12.2"},
	}); err != nil {
		t.Fatalf("failed to seed state: %v", err)
	}

	markerPath := filepath.Join(tempDir, "keep.txt")
	if err := os.WriteFile(markerPath, []byte("keep"), 0o644); err != nil {
		t.Fatalf("failed to seed existing target directory: %v", err)
	}

	manager.runCommandFunc = func(_ context.Context, _ commandSpec) error {
		writePythonVenvFixture(t, filepath.Join(tempDir, ".venv"))
		return nil
	}

	created, err := manager.CreateVenv(context.Background(), "python", ".")
	if err != nil {
		t.Fatalf("create venv with dot path failed: %v", err)
	}

	wantPath := filepath.Join(tempDir, ".venv")
	if created.Path != wantPath {
		t.Fatalf("expected venv path %s, got %s", wantPath, created.Path)
	}
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("expected pre-existing file to remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wantPath, "Scripts", "python.exe")); err != nil {
		t.Fatalf("expected venv executable to exist in %s: %v", wantPath, err)
	}
}

func TestManagerCreateVenvRejectsUnsupportedRuntime(t *testing.T) {
	manager := newTestManager(t.TempDir(), NewBuiltinRegistry())

	testCases := []struct {
		runtime string
		want    string
	}{
		{runtime: "node", want: "runtime nodejs does not support venv"},
		{runtime: "java", want: "runtime java does not support venv"},
		{runtime: "go", want: "runtime go does not support venv"},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.runtime, func(t *testing.T) {
			_, err := manager.CreateVenv(context.Background(), testCase.runtime, ".venv")
			if err == nil {
				t.Fatalf("expected unsupported runtime error")
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestManagerCreateVenvRequiresActivePythonRuntime(t *testing.T) {
	manager := newTestManager(t.TempDir(), NewBuiltinRegistry())

	_, err := manager.CreateVenv(context.Background(), "python", ".venv")
	if err == nil {
		t.Fatalf("expected missing active runtime error")
	}
	if !strings.Contains(err.Error(), "no active version for runtime python") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestManagerCreateVenvReportsBootstrapFailures(t *testing.T) {
	tempDir := t.TempDir()
	manager := newTestManager(tempDir, NewBuiltinRegistry())
	writeInstalledRuntime(t, manager, "python", "3.12.2", "python.exe", "python")
	_ = configureVirtualenvBootstrapFixture(t, manager)

	if err := saveState(manager.paths.StatePath, State{
		Active: map[string]string{"python": "3.12.2"},
	}); err != nil {
		t.Fatalf("failed to seed state: %v", err)
	}

	manager.runCommandFunc = func(_ context.Context, spec commandSpec) error {
		if _, err := io.WriteString(spec.Stderr, "virtualenv bootstrap failed"); err != nil {
			t.Fatalf("failed to seed bootstrap stderr: %v", err)
		}
		return &exec.ExitError{}
	}

	_, err := manager.CreateVenv(context.Background(), "python", "envs/demo")
	if err == nil {
		t.Fatalf("expected interpreter failure")
	}
	if !strings.Contains(err.Error(), filepath.Join(tempDir, "envs", "demo")) {
		t.Fatalf("expected failure path in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "virtualenv bootstrap failed") {
		t.Fatalf("expected interpreter output in error, got %v", err)
	}
}

func TestManagerListRemoteAndLatestResolveAliases(t *testing.T) {
	tempDir := t.TempDir()
	registry, err := NewRegistry(fakeRemoteProvider{
		versions: []RemoteVersion{
			{Version: "2.1.3", Stable: true},
			{Version: "2.1.2", Stable: true},
			{Version: "2.0.5", Stable: true, LTS: true},
			{Version: "1.9.9", Stable: true, LTS: true},
		},
	})
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	manager := newTestManager(tempDir, registry)

	listed, err := manager.ListRemote(context.Background(), "fake", "2.1")
	if err != nil {
		t.Fatalf("list-remote failed: %v", err)
	}
	if len(listed) != 2 || listed[0].Version != "2.1.3" || listed[1].Version != "2.1.2" {
		t.Fatalf("unexpected filtered versions: %#v", listed)
	}

	latest, err := manager.Latest(context.Background(), "fake", "lts")
	if err != nil {
		t.Fatalf("latest failed: %v", err)
	}
	if latest.Version != "2.0.5" {
		t.Fatalf("expected lts 2.0.5, got %#v", latest)
	}
}

func TestManagerUseProjectStoresResolvedInstalledVersion(t *testing.T) {
	tempDir := t.TempDir()
	registry, err := NewRegistry(fakeRemoteProvider{
		versions: []RemoteVersion{
			{Version: "2.1.3", Stable: true},
			{Version: "2.1.2", Stable: true},
			{Version: "2.0.5", Stable: true, LTS: true},
		},
	})
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	manager := newTestManager(tempDir, registry)
	writeInstalledRuntime(t, manager, "fake", "2.1.2", "bin/fake.cmd", "@echo off\r\necho fake-2.1.2\r\n")
	writeInstalledRuntime(t, manager, "fake", "2.0.5", "bin/fake.cmd", "@echo off\r\necho fake-2.0.5\r\n")

	projectDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}
	manager.getwdFunc = func() (string, error) {
		return projectDir, nil
	}

	current, err := manager.UseProject(context.Background(), "fake", "2.1")
	if err != nil {
		t.Fatalf("use --project failed: %v", err)
	}
	if current.Version != "2.1.2" {
		t.Fatalf("expected resolved installed version 2.1.2, got %s", current.Version)
	}

	config, err := loadProjectConfig(filepath.Join(projectDir, projectConfigFileName))
	if err != nil {
		t.Fatalf("failed to load project config: %v", err)
	}
	if config.Runtimes["fake"] != "2.1.2" {
		t.Fatalf("expected pinned version 2.1.2, got %#v", config.Runtimes)
	}
}

func TestManagerDownloadRetriesAndVerifiesChecksum(t *testing.T) {
	tempDir := t.TempDir()
	manager := newTestManager(tempDir, nil)

	payload := []byte("artifact")
	sum := sha256.Sum256(payload)
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts < 3 {
			http.Error(writer, "retry", http.StatusBadGateway)
			return
		}
		_, _ = writer.Write(payload)
	}))
	defer server.Close()

	manager.client = server.Client()
	path, err := manager.download(context.Background(), "fake", "1.0.0", PackageSpec{
		URL:    server.URL + "/artifact.zip",
		SHA256: hex.EncodeToString(sum[:]),
	})
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected cached artifact to exist: %v", err)
	}
}

func TestManagerDownloadChecksumFailure(t *testing.T) {
	tempDir := t.TempDir()
	manager := newTestManager(tempDir, nil)

	payload := []byte("wrong")
	sum := sha256.Sum256([]byte("expected"))
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		attempts++
		_, _ = writer.Write(payload)
	}))
	defer server.Close()

	manager.client = server.Client()
	_, err := manager.download(context.Background(), "fake", "1.0.0", PackageSpec{
		URL:    server.URL + "/artifact.zip",
		SHA256: hex.EncodeToString(sum[:]),
	})
	if err == nil {
		t.Fatalf("expected checksum failure")
	}
	if attempts != downloadRetryAttempts {
		t.Fatalf("expected %d attempts, got %d", downloadRetryAttempts, attempts)
	}

	cachePath, err := artifactCachePath(manager.paths.CacheRoot, server.URL+"/artifact.zip")
	if err != nil {
		t.Fatalf("failed to build cache path: %v", err)
	}
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatalf("expected no cached artifact after checksum failure, got %v", err)
	}
}

func TestManagerDownloadReportsProgressOnlyForCacheMiss(t *testing.T) {
	tempDir := t.TempDir()
	manager := newTestManager(tempDir, nil)

	payload := bytes.Repeat([]byte("artifact"), 8192)
	sum := sha256.Sum256(payload)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests++
		writer.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = writer.Write(payload)
	}))
	defer server.Close()

	observer := &recordingDownloadObserver{}
	manager.SetDownloadProgressObserver(observer)
	manager.client = server.Client()

	pkg := PackageSpec{
		URL:    server.URL + "/artifact.zip",
		SHA256: hex.EncodeToString(sum[:]),
		Size:   int64(len(payload)),
	}

	if _, err := manager.download(context.Background(), "fake", "1.0.0", pkg); err != nil {
		t.Fatalf("first download failed: %v", err)
	}
	if requests != 1 {
		t.Fatalf("expected one network request, got %d", requests)
	}

	var sawStarted bool
	var sawAdvanced bool
	var sawCompleted bool
	for _, event := range observer.events {
		switch event.State {
		case DownloadProgressStarted:
			sawStarted = true
		case DownloadProgressAdvanced:
			sawAdvanced = true
		case DownloadProgressCompleted:
			sawCompleted = true
		case DownloadProgressFailed:
			t.Fatalf("did not expect failed progress event: %#v", event)
		}
	}
	if !sawStarted || !sawAdvanced || !sawCompleted {
		t.Fatalf("expected started/advanced/completed events, got %#v", observer.events)
	}

	eventCount := len(observer.events)
	if _, err := manager.download(context.Background(), "fake", "1.0.0", pkg); err != nil {
		t.Fatalf("cached download failed: %v", err)
	}
	if requests != 1 {
		t.Fatalf("expected cached download to avoid network, got %d requests", requests)
	}
	if len(observer.events) != eventCount {
		t.Fatalf("expected cached download to stay quiet, got %#v", observer.events[eventCount:])
	}
}

func TestManagerDownloadReportsRetryFailures(t *testing.T) {
	tempDir := t.TempDir()
	manager := newTestManager(tempDir, nil)

	payload := []byte("artifact")
	sum := sha256.Sum256(payload)
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			http.Error(writer, "retry", http.StatusBadGateway)
			return
		}
		_, _ = writer.Write(payload)
	}))
	defer server.Close()

	observer := &recordingDownloadObserver{}
	manager.SetDownloadProgressObserver(observer)
	manager.client = server.Client()

	if _, err := manager.download(context.Background(), "fake", "1.0.0", PackageSpec{
		URL:    server.URL + "/artifact.zip",
		SHA256: hex.EncodeToString(sum[:]),
	}); err != nil {
		t.Fatalf("download failed: %v", err)
	}

	var failed DownloadProgressEvent
	var sawRetryFailure bool
	for _, event := range observer.events {
		if event.State == DownloadProgressFailed {
			failed = event
			sawRetryFailure = true
			break
		}
	}
	if !sawRetryFailure {
		t.Fatalf("expected retry failure event, got %#v", observer.events)
	}
	if failed.Attempt != 1 || !failed.WillRetry {
		t.Fatalf("unexpected failure event: %#v", failed)
	}
}

func TestPruneCacheDirRemovesExpiredAndOldestEntries(t *testing.T) {
	cacheDir := t.TempDir()

	oldPath := filepath.Join(cacheDir, "old.bin")
	newerPath := filepath.Join(cacheDir, "newer.bin")
	latestPath := filepath.Join(cacheDir, "latest.bin")
	for _, path := range []string{oldPath, newerPath, latestPath} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("failed to write cache file: %v", err)
		}
	}

	now := time.Now()
	if err := os.Chtimes(oldPath, now.Add(-48*time.Hour), now.Add(-48*time.Hour)); err != nil {
		t.Fatalf("failed to age old file: %v", err)
	}
	if err := os.Chtimes(newerPath, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("failed to age newer file: %v", err)
	}

	if err := pruneCacheDir(cacheDir, cachePolicy{MaxAge: 24 * time.Hour, MaxEntries: 1, MaxSizeBytes: 10}); err != nil {
		t.Fatalf("prune failed: %v", err)
	}

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expected expired file to be removed, got %v", err)
	}
	if _, err := os.Stat(newerPath); !os.IsNotExist(err) {
		t.Fatalf("expected oldest remaining file to be removed, got %v", err)
	}
	if _, err := os.Stat(latestPath); err != nil {
		t.Fatalf("expected newest file to remain: %v", err)
	}
}

func TestManagerExecPathAffectsCommandResolution(t *testing.T) {
	if goruntime.GOOS != "windows" {
		t.Skip("exec path test relies on cmd.exe")
	}

	tempDir := t.TempDir()
	registry, err := NewRegistry(fakeProvider{})
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	manager := newTestManager(tempDir, registry)
	writeInstalledRuntime(t, manager, "fake", "1.0.0", "bin/toolmesh-fake.cmd", "@echo off\r\necho fake-1.0.0\r\n")
	writeInstalledRuntime(t, manager, "fake", "2.0.0", "bin/toolmesh-fake.cmd", "@echo off\r\necho fake-2.0.0\r\n")

	if _, err := manager.Use("fake", "1.0.0"); err != nil {
		t.Fatalf("use 1.0.0 failed: %v", err)
	}
	output := runWithExecPath(t, manager, "cmd", "/c", "toolmesh-fake")
	if !strings.Contains(output, "fake-1.0.0") {
		t.Fatalf("expected version 1.0.0 output, got %q", output)
	}

	if _, err := manager.Use("fake", "2.0.0"); err != nil {
		t.Fatalf("use 2.0.0 failed: %v", err)
	}
	output = runWithExecPath(t, manager, "cmd", "/c", "toolmesh-fake")
	if !strings.Contains(output, "fake-2.0.0") {
		t.Fatalf("expected version 2.0.0 output, got %q", output)
	}
}

func builtinRuntimeCoverageFixtures(t *testing.T, platform Platform) map[string]builtinRuntimeCoverageFixture {
	t.Helper()

	pythonArchive := buildZipArchive(t, map[string]string{
		"python.exe": "python",
	})
	pythonChecksum := checksumHex(pythonArchive)
	pythonDownloadURL := "https://downloads.toolmesh.test/python-3.12.10-embeddable-amd64.zip"

	gitVersion := "2.53.0.2"
	gitArchiveFile, err := gitArchiveName(gitVersion, platform)
	if err != nil {
		t.Fatalf("failed to resolve git archive name: %v", err)
	}
	gitPreviousVersion := "2.52.0.1"
	gitPreviousArchiveFile, err := gitArchiveName(gitPreviousVersion, platform)
	if err != nil {
		t.Fatalf("failed to resolve previous git archive name: %v", err)
	}
	gitArchive := buildZipArchive(t, map[string]string{
		"cmd/git.exe":         "git",
		"cmd/git-cmd.exe":     "git-cmd",
		"mingw64/bin/git.exe": "git-bin",
	})
	gitChecksum := checksumHex(gitArchive)
	gitDownloadURL := "https://github.com/git-for-windows/git/releases/download/v2.53.0.windows.2/" + gitArchiveFile
	gitPreviousDownloadURL := "https://github.com/git-for-windows/git/releases/download/v2.52.0.windows.1/" + gitPreviousArchiveFile

	cmakeVersion := "4.3.0"
	cmakeArchiveFile, err := cmakeArchiveName(cmakeVersion, platform)
	if err != nil {
		t.Fatalf("failed to resolve cmake archive name: %v", err)
	}
	cmakePreviousVersion := "4.2.3"
	cmakePreviousArchiveFile, err := cmakeArchiveName(cmakePreviousVersion, platform)
	if err != nil {
		t.Fatalf("failed to resolve previous cmake archive name: %v", err)
	}
	cmakeArchive := buildZipArchive(t, map[string]string{
		"cmake-4.3.0-windows-x86_64/bin/cmake.exe": "cmake",
		"cmake-4.3.0-windows-x86_64/bin/ctest.exe": "ctest",
	})
	cmakeChecksum := checksumHex(cmakeArchive)
	cmakeDownloadURL := "https://github.com/Kitware/CMake/releases/download/v4.3.0/" + cmakeArchiveFile
	cmakePreviousDownloadURL := "https://github.com/Kitware/CMake/releases/download/v4.2.3/" + cmakePreviousArchiveFile

	nodeVersion := "20.12.2"
	nodeArchiveFile, err := nodeArchiveName(nodeVersion, platform)
	if err != nil {
		t.Fatalf("failed to resolve node archive name: %v", err)
	}
	nodeArchive := buildZipArchive(t, map[string]string{
		"node-v20.12.2-win-x64/node.exe": "node",
	})
	nodeChecksum := checksumHex(nodeArchive)
	nodeShasumsURL := fmt.Sprintf("https://nodejs.org/dist/v%s/SHASUMS256.txt", nodeVersion)
	nodeDownloadURL := fmt.Sprintf("https://nodejs.org/dist/v%s/%s", nodeVersion, nodeArchiveFile)

	javaVersion := "21.0.2+13"
	javaArchive := buildZipArchive(t, map[string]string{
		"jdk-21.0.2+13/bin/java.exe": "java",
	})
	javaChecksum := checksumHex(javaArchive)
	javaDownloadURL := "https://downloads.toolmesh.test/OpenJDK21U-jdk_x64_windows_hotspot_21.0.2_13.zip"
	java17DownloadURL := "https://downloads.toolmesh.test/OpenJDK17U-jdk_x64_windows_hotspot_17.0.10_7.zip"

	goVersion := "1.22.2"
	goArchiveFile := "go1.22.2.windows-amd64.zip"
	goArchive := buildZipArchive(t, map[string]string{
		"go/bin/go.exe": "go",
	})
	goChecksum := checksumHex(goArchive)
	goDownloadURL := "https://go.dev/dl/" + goArchiveFile

	mingwStableVersion := "15.2.0posix-13.0.0-ucrt-r6"
	mingwSnapshotVersion := "16.0.1-snapshot20260222posix-13.0.0-ucrt-r1"
	mingwArchiveNamePrefix, err := mingwArchivePrefix(platform)
	if err != nil {
		t.Fatalf("failed to resolve mingw archive prefix: %v", err)
	}
	mingwStableArchiveFile := mingwArchiveNamePrefix + "15.2.0-mingw-w64ucrt-13.0.0-r6.zip"
	mingwSnapshotArchiveFile := mingwArchiveNamePrefix + "16.0.1-snapshot20260222-mingw-w64ucrt-13.0.0-r1.zip"
	mingwArchive := buildZipArchive(t, map[string]string{
		"mingw64/bin/gcc.exe": "gcc",
		"mingw64/bin/g++.exe": "g++",
		"mingw64/bin/c++.exe": "c++",
	})
	mingwChecksum := checksumHex(mingwArchive)
	mingwDownloadURL := "https://github.com/brechtsanders/winlibs_mingw/releases/download/" + mingwStableVersion + "/" + mingwStableArchiveFile

	return map[string]builtinRuntimeCoverageFixture{
		"cmake": {
			commandRuntime:       "cmake",
			installVersion:       "latest",
			wantInstalledVersion: cmakeVersion,
			listRemoteSelector:   "4",
			wantRemoteVersions: []RemoteVersion{
				{Version: "4.3.0", Stable: true},
				{Version: "4.2.3", Stable: true},
			},
			wantLatestVersion:  RemoteVersion{Version: "4.3.0", Stable: true},
			expectedExecutable: "cmake.exe",
			httpResponseFixture: map[string]httpResponseFixture{
				cmakeReleasesURL: {
					contentType: "application/json",
					body: []byte(fmt.Sprintf(`[
						{"tag_name":"v4.3.0","draft":false,"prerelease":false,"assets":[{"name":"%s","browser_download_url":"%s","digest":"sha256:%s","size":%d}]},
						{"tag_name":"v4.3.0-rc3","draft":false,"prerelease":true,"assets":[{"name":"cmake-4.3.0-rc3-windows-x86_64.zip","browser_download_url":"https://downloads.toolmesh.test/cmake-4.3.0-rc3-windows-x86_64.zip","digest":"sha256:ignored","size":1}]},
						{"tag_name":"v4.2.3","draft":false,"prerelease":false,"assets":[{"name":"%s","browser_download_url":"%s","digest":"sha256:ignored","size":1}]}
					]`, cmakeArchiveFile, cmakeDownloadURL, cmakeChecksum, len(cmakeArchive), cmakePreviousArchiveFile, cmakePreviousDownloadURL)),
				},
				cmakeDownloadURL: {
					contentType: "application/zip",
					body:        cmakeArchive,
				},
			},
		},
		"git": {
			commandRuntime:       "gitforwindows",
			installVersion:       "latest",
			wantInstalledVersion: gitVersion,
			listRemoteSelector:   "2",
			wantRemoteVersions: []RemoteVersion{
				{Version: "2.53.0.2", Stable: true},
				{Version: "2.52.0.1", Stable: true},
			},
			wantLatestVersion:  RemoteVersion{Version: "2.53.0.2", Stable: true},
			expectedExecutable: "git.exe",
			httpResponseFixture: map[string]httpResponseFixture{
				gitForWindowsReleasesURL: {
					contentType: "application/json",
					body: []byte(fmt.Sprintf(`[
						{"tag_name":"untagged-8231769e9b878a01c378","draft":false,"prerelease":true,"assets":[{"name":"MinGit-2.47.3.2-64-bit.zip","browser_download_url":"https://downloads.toolmesh.test/MinGit-2.47.3.2-64-bit.zip","digest":"sha256:ignored","size":1}]},
						{"tag_name":"v2.53.0.windows.2","draft":false,"prerelease":false,"assets":[{"name":"%s","browser_download_url":"%s","digest":"sha256:%s","size":%d}]},
						{"tag_name":"v2.52.0.windows.2","draft":false,"prerelease":true,"assets":[{"name":"MinGit-2.52.0.2-64-bit.zip","browser_download_url":"https://downloads.toolmesh.test/MinGit-2.52.0.2-64-bit.zip","digest":"sha256:ignored","size":1}]},
						{"tag_name":"v2.52.0.windows.1","draft":false,"prerelease":false,"assets":[{"name":"%s","browser_download_url":"%s","digest":"sha256:ignored","size":1}]}
					]`, gitArchiveFile, gitDownloadURL, gitChecksum, len(gitArchive), gitPreviousArchiveFile, gitPreviousDownloadURL)),
				},
				gitDownloadURL: {
					contentType: "application/zip",
					body:        gitArchive,
				},
			},
		},
		"go": {
			commandRuntime:     "golang",
			installVersion:     goVersion,
			listRemoteSelector: "1.22",
			wantRemoteVersions: []RemoteVersion{
				{Version: "1.22.2", Stable: true},
				{Version: "1.22.1", Stable: true},
			},
			wantLatestVersion:  RemoteVersion{Version: "1.22.2", Stable: true},
			expectedExecutable: "go.exe",
			httpResponseFixture: map[string]httpResponseFixture{
				"https://go.dev/dl/?mode=json&include=all": {
					contentType: "application/json",
					body: []byte(fmt.Sprintf(`[
						{"version":"go1.22.2","stable":true,"files":[{"filename":"%s","os":"windows","arch":"amd64","kind":"archive","sha256":"%s","size":%d}]},
						{"version":"go1.22.1","stable":true,"files":[{"filename":"go1.22.1.windows-amd64.zip","os":"windows","arch":"amd64","kind":"archive","sha256":"ignored","size":1}]},
						{"version":"go1.21.10","stable":true,"files":[{"filename":"go1.21.10.windows-amd64.zip","os":"windows","arch":"amd64","kind":"archive","sha256":"ignored","size":1}]}
					]`, goArchiveFile, goChecksum, len(goArchive))),
				},
				goDownloadURL: {
					contentType: "application/zip",
					body:        goArchive,
				},
			},
		},
		"java": {
			commandRuntime:     "java",
			installVersion:     javaVersion,
			listRemoteSelector: "21",
			wantRemoteVersions: []RemoteVersion{
				{Version: "21.0.2+13", Stable: true, LTS: true},
				{Version: "21.0.1+12", Stable: true, LTS: true},
			},
			latestSelector:     "lts",
			wantLatestVersion:  RemoteVersion{Version: "21.0.2+13", Stable: true, LTS: true},
			expectedExecutable: "java.exe",
			httpResponseFixture: map[string]httpResponseFixture{
				"https://api.adoptium.net/v3/info/available_releases": {
					contentType: "application/json",
					body:        []byte(`{"available_releases":[21,17],"available_lts_releases":[21,17]}`),
				},
				javaFeatureReleasesURL(21, platform): {
					contentType: "application/json",
					body: []byte(fmt.Sprintf(`[
						{"release_name":"jdk-21.0.2+13","binaries":[{"package":{"checksum":"%s","link":"%s","size":%d}}]},
						{"release_name":"jdk-21.0.1+12","binaries":[{"package":{"checksum":"ignored","link":"%s","size":1}}]}
					]`, javaChecksum, javaDownloadURL, len(javaArchive), java17DownloadURL)),
				},
				javaFeatureReleasesURL(17, platform): {
					contentType: "application/json",
					body: []byte(fmt.Sprintf(`[
						{"release_name":"jdk-17.0.10+7","binaries":[{"package":{"checksum":"ignored","link":"%s","size":1}}]}
					]`, java17DownloadURL)),
				},
				javaDownloadURL: {
					contentType: "application/zip",
					body:        javaArchive,
				},
			},
		},
		"nodejs": {
			commandRuntime:     "node",
			installVersion:     nodeVersion,
			listRemoteSelector: "20",
			wantRemoteVersions: []RemoteVersion{
				{Version: "20.12.2", Stable: true, LTS: true},
				{Version: "20.11.1", Stable: true, LTS: true},
			},
			latestSelector:     "lts",
			wantLatestVersion:  RemoteVersion{Version: "20.12.2", Stable: true, LTS: true},
			expectedExecutable: "node.exe",
			httpResponseFixture: map[string]httpResponseFixture{
				"https://nodejs.org/dist/index.json": {
					contentType: "application/json",
					body: []byte(`[
						{"version":"v22.0.0","files":["win-x64-zip"],"lts":false},
						{"version":"v20.12.2","files":["win-x64-zip"],"lts":"Iron"},
						{"version":"v20.11.1","files":["win-x64-zip"],"lts":"Iron"}
					]`),
				},
				nodeShasumsURL: {
					contentType: "text/plain",
					body:        []byte(fmt.Sprintf("%s  %s\n", nodeChecksum, nodeArchiveFile)),
				},
				nodeDownloadURL: {
					contentType: "application/zip",
					body:        nodeArchive,
				},
			},
		},
		"mingw": {
			commandRuntime:       "gcc",
			installVersion:       "latest",
			wantInstalledVersion: mingwStableVersion,
			listRemoteSelector:   "15",
			wantRemoteVersions: []RemoteVersion{
				{Version: mingwStableVersion, Stable: true},
			},
			wantLatestVersion:  RemoteVersion{Version: mingwStableVersion, Stable: true},
			expectedExecutable: "gcc.exe",
			httpResponseFixture: map[string]httpResponseFixture{
				winlibsReleasesURL: {
					contentType: "application/json",
					body: []byte(fmt.Sprintf(`[
						{"tag_name":"%s","draft":false,"prerelease":false,"assets":[
							{"name":"%s","browser_download_url":"%s","digest":"sha256:%s","size":%d},
							{"name":"%s.sha256","browser_download_url":"%s.sha256","digest":"sha256:ignored","size":1}
						]},
						{"tag_name":"%s","draft":false,"prerelease":false,"assets":[
							{"name":"%s","browser_download_url":"https://downloads.toolmesh.test/%s","digest":"sha256:ignored","size":1}
						]},
						{"tag_name":"16.0.1-snapshot20260222posix-13.0.0-msvcrt-r1","draft":false,"prerelease":false,"assets":[
							{"name":"winlibs-x86_64-posix-seh-gcc-16.0.1-snapshot20260222-mingw-w64msvcrt-13.0.0-r1.zip","browser_download_url":"https://downloads.toolmesh.test/winlibs-msvcrt.zip","digest":"sha256:ignored","size":1}
						]},
						{"tag_name":"15.2.0posix-13.0.0-msvcrt-r6","draft":false,"prerelease":false,"assets":[
							{"name":"%s","browser_download_url":"https://downloads.toolmesh.test/%s","digest":"sha256:ignored","size":1}
						]}
					]`, mingwStableVersion, mingwStableArchiveFile, mingwDownloadURL, mingwChecksum, len(mingwArchive), mingwStableArchiveFile, mingwStableArchiveFile, mingwSnapshotVersion, mingwSnapshotArchiveFile, mingwSnapshotArchiveFile, strings.ReplaceAll(mingwStableArchiveFile, "ucrt", "msvcrt"), strings.ReplaceAll(mingwStableArchiveFile, "ucrt", "msvcrt"))),
				},
				mingwDownloadURL: {
					contentType: "application/zip",
					body:        mingwArchive,
				},
			},
		},
		"python": {
			commandRuntime:       "py",
			installVersion:       "3.12",
			wantInstalledVersion: "3.12.10",
			listRemoteSelector:   "3.12",
			wantRemoteVersions: []RemoteVersion{
				{Version: "3.12.10", Stable: true},
				{Version: "3.12.9", Stable: true},
			},
			latestSelector:     "3.12",
			wantLatestVersion:  RemoteVersion{Version: "3.12.10", Stable: true},
			expectedExecutable: "python.exe",
			httpResponseFixture: map[string]httpResponseFixture{
				pythonWindowsDownloadsURL: {
					contentType: "text/html",
					body: []byte(`
						<a href="https://www.python.org/ftp/python/3.13.12/python-3.13.12-embeddable-amd64.zip">3.13.12 amd64 embeddable</a>
						<a href="https://www.python.org/ftp/python/3.12.10/python-3.12.10-embeddable-amd64.zip">3.12.10 amd64 embeddable</a>
						<a href="https://www.python.org/ftp/python/3.12.9/python-3.12.9-embed-amd64.zip">3.12.9 amd64 embed</a>
						<a href="https://www.python.org/ftp/python/3.11.13/python-3.11.13-embeddable-amd64.zip">3.11.13 amd64 embeddable</a>
					`),
				},
				"https://www.python.org/ftp/python/3.12.10/windows-3.12.10.json": {
					contentType: "application/json",
					body: []byte(fmt.Sprintf(`{"versions":[{"company":"PythonEmbed","tag":"3.12-64","url":"%s","hash":{"sha256":"%s"}}]}`,
						pythonDownloadURL, pythonChecksum)),
				},
				pythonDownloadURL: {
					contentType: "application/zip",
					body:        pythonArchive,
				},
			},
		},
	}
}

func javaFeatureReleasesURL(major int, platform Platform) string {
	arch, err := platform.javaArchToken()
	if err != nil {
		panic(err)
	}

	query := url.Values{}
	query.Set("architecture", arch)
	query.Set("heap_size", "normal")
	query.Set("image_type", "jdk")
	query.Set("jvm_impl", "hotspot")
	query.Set("os", platform.OS)
	query.Set("vendor", "eclipse")

	return fmt.Sprintf("https://api.adoptium.net/v3/assets/feature_releases/%d/ga?%s", major, query.Encode())
}

func checksumHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func newTestManager(tempDir string, registry *Registry) *Manager {
	manager := NewManager(Paths{
		ConfigDir:   filepath.Join(tempDir, "config"),
		DataDir:     filepath.Join(tempDir, "data"),
		StatePath:   filepath.Join(tempDir, "config", "state.json"),
		InstallRoot: filepath.Join(tempDir, "data", "runtimes"),
		CacheRoot:   filepath.Join(tempDir, "data", "downloads"),
		ShimsDir:    filepath.Join(tempDir, "data", "shims"),
	}, registry)
	manager.platform = Platform{OS: "windows", Arch: "amd64"}
	manager.getwdFunc = func() (string, error) { return tempDir, nil }
	manager.ensureUserPathEntryFunc = func(string) error { return nil }
	return manager
}

func TestManagerUseCreatesGlobalCommandShims(t *testing.T) {
	tempDir := t.TempDir()
	registry, err := NewRegistry(fakeProvider{})
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	manager := newTestManager(tempDir, registry)

	toolmeshPath := filepath.Join(tempDir, "toolmesh.exe")
	if err := os.WriteFile(toolmeshPath, []byte("toolmesh"), 0o755); err != nil {
		t.Fatalf("failed to write toolmesh launcher: %v", err)
	}

	var ensuredPath string
	manager.executablePathFunc = func() (string, error) { return toolmeshPath, nil }
	manager.ensureUserPathEntryFunc = func(path string) error {
		ensuredPath = path
		return nil
	}

	writeInstalledRuntime(t, manager, "fake", "1.0.0", "bin/node.exe", "node")

	npmPath := filepath.Join(manager.installDir("fake", "1.0.0"), "bin", "npm.cmd")
	if err := os.WriteFile(npmPath, []byte("@echo off\r\necho npm\r\n"), 0o755); err != nil {
		t.Fatalf("failed to write npm launcher: %v", err)
	}

	npmPSPath := filepath.Join(manager.installDir("fake", "1.0.0"), "bin", "npm.ps1")
	if err := os.WriteFile(npmPSPath, []byte("Write-Output npm"), 0o755); err != nil {
		t.Fatalf("failed to write npm ps1 launcher: %v", err)
	}

	if _, err := manager.Use("fake", "1.0.0"); err != nil {
		t.Fatalf("use failed: %v", err)
	}

	if ensuredPath != manager.paths.ShimsDir {
		t.Fatalf("expected PATH registration for %s, got %s", manager.paths.ShimsDir, ensuredPath)
	}

	launcherPath := filepath.Join(manager.paths.ShimsDir, shimLauncherName)
	if _, err := os.Stat(launcherPath); err != nil {
		t.Fatalf("expected copied toolmesh launcher: %v", err)
	}

	nodeShimPath := filepath.Join(manager.paths.ShimsDir, "node.cmd")
	nodeShim, err := os.ReadFile(nodeShimPath)
	if err != nil {
		t.Fatalf("failed to read node shim: %v", err)
	}
	if !strings.Contains(string(nodeShim), "\"%~dp0"+shimLauncherName+"\" exec \"node\" %*") {
		t.Fatalf("unexpected node shim content: %q", string(nodeShim))
	}

	npmShimPath := filepath.Join(manager.paths.ShimsDir, "npm.cmd")
	if _, err := os.Stat(npmShimPath); err != nil {
		t.Fatalf("expected npm shim: %v", err)
	}
}

func TestManagerUseCreatesGNUCommandShims(t *testing.T) {
	tempDir := t.TempDir()
	registry, err := NewRegistry(fakeProvider{})
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	manager := newTestManager(tempDir, registry)

	toolmeshPath := filepath.Join(tempDir, "toolmesh.exe")
	if err := os.WriteFile(toolmeshPath, []byte("toolmesh"), 0o755); err != nil {
		t.Fatalf("failed to write toolmesh launcher: %v", err)
	}
	manager.executablePathFunc = func() (string, error) { return toolmeshPath, nil }
	manager.ensureUserPathEntryFunc = func(string) error { return nil }

	writeInstalledRuntime(t, manager, "fake", "1.0.0", "bin/gcc.exe", "gcc")
	for _, name := range []string{"g++.exe", "c++.exe"} {
		path := filepath.Join(manager.installDir("fake", "1.0.0"), "bin", name)
		if err := os.WriteFile(path, []byte(name), 0o755); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}

	if _, err := manager.Use("fake", "1.0.0"); err != nil {
		t.Fatalf("use failed: %v", err)
	}

	for _, commandName := range []string{"gcc", "g++", "c++"} {
		shimPath := filepath.Join(manager.paths.ShimsDir, commandName+".cmd")
		content, err := os.ReadFile(shimPath)
		if err != nil {
			t.Fatalf("expected shim for %s: %v", commandName, err)
		}
		if !strings.Contains(string(content), "\"%~dp0"+shimLauncherName+"\" exec \""+commandName+"\" %*") {
			t.Fatalf("unexpected shim content for %s: %q", commandName, string(content))
		}
	}
}

func TestManagerRemoveActiveRuntimeRefreshesGlobalCommandShims(t *testing.T) {
	tempDir := t.TempDir()
	registry, err := NewRegistry(fakeProvider{})
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	manager := newTestManager(tempDir, registry)

	toolmeshPath := filepath.Join(tempDir, "toolmesh.exe")
	if err := os.WriteFile(toolmeshPath, []byte("toolmesh"), 0o755); err != nil {
		t.Fatalf("failed to write toolmesh launcher: %v", err)
	}
	manager.executablePathFunc = func() (string, error) { return toolmeshPath, nil }
	manager.ensureUserPathEntryFunc = func(string) error { return nil }

	writeInstalledRuntime(t, manager, "fake", "1.0.0", "bin/node.exe", "node")

	if _, err := manager.Use("fake", "1.0.0"); err != nil {
		t.Fatalf("use failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(manager.paths.ShimsDir, "node.cmd")); err != nil {
		t.Fatalf("expected node shim before remove: %v", err)
	}

	if err := manager.Remove("fake", "1.0.0"); err != nil {
		t.Fatalf("remove failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(manager.paths.ShimsDir, "node.cmd")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected node shim to be removed, got err=%v", err)
	}
}

func TestManagerUsePrunesMissingActiveRuntimeSelectionsBeforeSyncingShims(t *testing.T) {
	tempDir := t.TempDir()
	registry, err := NewRegistry(fakeProvider{})
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	manager := newTestManager(tempDir, registry)

	toolmeshPath := filepath.Join(tempDir, "toolmesh.exe")
	if err := os.WriteFile(toolmeshPath, []byte("toolmesh"), 0o755); err != nil {
		t.Fatalf("failed to write toolmesh launcher: %v", err)
	}
	manager.executablePathFunc = func() (string, error) { return toolmeshPath, nil }
	manager.ensureUserPathEntryFunc = func(string) error { return nil }

	writeInstalledRuntime(t, manager, "fake", "1.0.0", "bin/node.exe", "node")
	if err := saveState(manager.paths.StatePath, State{
		Active: map[string]string{
			"go": "1.26.1",
		},
	}); err != nil {
		t.Fatalf("failed to seed state: %v", err)
	}

	current, err := manager.Use("fake", "1.0.0")
	if err != nil {
		t.Fatalf("use failed: %v", err)
	}
	if !current.Active {
		t.Fatalf("expected selected runtime to become active")
	}

	state, err := loadState(manager.paths.StatePath)
	if err != nil {
		t.Fatalf("failed to load state: %v", err)
	}
	if _, exists := state.Active["go"]; exists {
		t.Fatalf("expected missing go selection to be pruned, got %#v", state.Active)
	}
	if state.Active["fake"] != "1.0.0" {
		t.Fatalf("expected fake to remain active, got %#v", state.Active)
	}

	if _, err := os.Stat(filepath.Join(manager.paths.ShimsDir, "node.cmd")); err != nil {
		t.Fatalf("expected node shim to exist after pruning stale state: %v", err)
	}
}

func TestManagerIgnoresIncompleteInstallDirectories(t *testing.T) {
	tempDir := t.TempDir()
	registry, err := NewRegistry(fakeProvider{})
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	manager := newTestManager(tempDir, registry)
	writeInstalledRuntime(t, manager, "fake", "1.0.0", "bin/fake.exe", "fake")

	incompleteDir := manager.installDir("fake", "2.0.0")
	if err := os.MkdirAll(incompleteDir, 0o755); err != nil {
		t.Fatalf("failed to create incomplete install dir: %v", err)
	}

	listed, err := manager.List("fake")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(listed) != 1 || listed[0].Version != "1.0.0" {
		t.Fatalf("expected only complete installs to be listed, got %#v", listed)
	}

	if _, err := manager.Use("fake", "2.0.0"); err == nil {
		t.Fatalf("expected incomplete install dir to be ignored by version resolution")
	} else if !strings.Contains(err.Error(), "runtime fake 2.0.0 is not installed") {
		t.Fatalf("unexpected use error: %v", err)
	}
}

func writeInstalledRuntime(t *testing.T, manager *Manager, runtimeName string, version string, executableRelative string, content string) {
	t.Helper()

	installDir := manager.installDir(runtimeName, version)
	executablePath := filepath.Join(installDir, filepath.FromSlash(executableRelative))
	if err := os.MkdirAll(filepath.Dir(executablePath), 0o755); err != nil {
		t.Fatalf("failed to create install dir: %v", err)
	}
	if err := os.WriteFile(executablePath, []byte(content), 0o755); err != nil {
		t.Fatalf("failed to write executable: %v", err)
	}

	record := installationRecord{
		Runtime:     runtimeName,
		Version:     version,
		Home:        ".",
		Executable:  filepath.ToSlash(executableRelative),
		InstalledAt: time.Now().UTC(),
	}
	if err := writeJSONAtomic(filepath.Join(installDir, installationMetadataFileName), record); err != nil {
		t.Fatalf("failed to write installation metadata: %v", err)
	}
}

func configureVirtualenvBootstrapFixture(t *testing.T, manager *Manager) []string {
	t.Helper()

	type fixturePackage struct {
		name         string
		version      string
		filename     string
		url          string
		requiresDist []string
	}

	packages := []fixturePackage{
		{
			name:     "virtualenv",
			version:  "21.2.0",
			filename: "virtualenv-21.2.0-py3-none-any.whl",
			url:      "https://files.pythonhosted.org/packages/virtualenv-21.2.0-py3-none-any.whl",
			requiresDist: []string{
				"distlib<1,>=0.3.7",
				"filelock<4,>=3.24.2; python_version >= \"3.10\"",
				"filelock<=3.19.1,>=3.16.1; python_version < \"3.10\"",
				"platformdirs<5,>=3.9.1",
				"python-discovery>=1",
				"furo>=2025.12.19; extra == \"docs\"",
			},
		},
		{
			name:     "distlib",
			version:  "0.4.0",
			filename: "distlib-0.4.0-py2.py3-none-any.whl",
			url:      "https://files.pythonhosted.org/packages/distlib-0.4.0-py2.py3-none-any.whl",
		},
		{
			name:     "filelock",
			version:  "3.25.2",
			filename: "filelock-3.25.2-py3-none-any.whl",
			url:      "https://files.pythonhosted.org/packages/filelock-3.25.2-py3-none-any.whl",
		},
		{
			name:     "platformdirs",
			version:  "4.9.4",
			filename: "platformdirs-4.9.4-py3-none-any.whl",
			url:      "https://files.pythonhosted.org/packages/platformdirs-4.9.4-py3-none-any.whl",
		},
		{
			name:     "python-discovery",
			version:  "1.2.0",
			filename: "python_discovery-1.2.0-py3-none-any.whl",
			url:      "https://files.pythonhosted.org/packages/python_discovery-1.2.0-py3-none-any.whl",
		},
	}

	fixtures := make(map[string]httpResponseFixture, len(packages)*2)
	expectedWheelPaths := make([]string, 0, len(packages))

	for _, pkg := range packages {
		wheelBytes := buildZipArchive(t, map[string]string{
			"pkg/__init__.py": pkg.name,
		})
		fixtures[fmt.Sprintf("https://pypi.org/pypi/%s/json", pkg.name)] = httpResponseFixture{
			contentType: "application/json",
			body: []byte(fmt.Sprintf(`{
				"info": {"version": %q, "requires_dist": %s},
				"urls": [{
					"filename": %q,
					"url": %q,
					"packagetype": "bdist_wheel",
					"size": %d,
					"digests": {"sha256": %q}
				}]
			}`, pkg.version, marshalStringSliceForJSON(t, pkg.requiresDist), pkg.filename, pkg.url, len(wheelBytes), checksumHex(wheelBytes))),
		}
		fixtures[pkg.url] = httpResponseFixture{
			contentType: "application/octet-stream",
			body:        wheelBytes,
		}

		cachePath, err := artifactCachePath(manager.paths.CacheRoot, pkg.url)
		if err != nil {
			t.Fatalf("failed to resolve cache path for %s: %v", pkg.name, err)
		}
		expectedWheelPaths = append(expectedWheelPaths, cachePath)
	}

	manager.client = &http.Client{Transport: fixtureTransport(fixtures)}
	return expectedWheelPaths
}

func marshalStringSliceForJSON(t *testing.T, values []string) string {
	t.Helper()

	if len(values) == 0 {
		return "[]"
	}

	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("failed to marshal string slice: %v", err)
	}
	return string(encoded)
}

func runWithExecPath(t *testing.T, manager *Manager, args ...string) string {
	t.Helper()

	pathEntries, err := manager.ExecPath()
	if err != nil {
		t.Fatalf("failed to resolve exec path: %v", err)
	}

	command := exec.Command(args[0], args[1:]...)
	command.Env = prependPath(os.Environ(), pathEntries)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v, output=%s", err, output)
	}
	return string(output)
}

func prependPath(environment []string, pathEntries []string) []string {
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

	entryValue := strings.Join(pathEntries, string(os.PathListSeparator))
	if currentValue != "" {
		entryValue += string(os.PathListSeparator) + currentValue
	}

	entry := pathKey + "=" + entryValue
	if index >= 0 {
		environment[index] = entry
		return environment
	}
	return append(environment, entry)
}

func buildZipArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buffer bytes.Buffer
	archiveWriter := zip.NewWriter(&buffer)

	for name, content := range files {
		writer, err := archiveWriter.Create(name)
		if err != nil {
			t.Fatalf("failed to create zip entry %s: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("failed to write zip entry %s: %v", name, err)
		}
	}

	if err := archiveWriter.Close(); err != nil {
		t.Fatalf("failed to close zip archive: %v", err)
	}

	return buffer.Bytes()
}
