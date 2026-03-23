package runtimes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const installationMetadataFileName = ".toolmesh-install.json"

type InstalledRuntime struct {
	Runtime     string
	Version     string
	Home        string
	Executable  string
	Active      bool
	InstalledAt time.Time
}

type VenvResult struct {
	Runtime    string
	Version    string
	Path       string
	Executable string
}

type Service interface {
	Install(ctx context.Context, runtimeName string, version string) (InstalledRuntime, error)
	List(runtimeName string) ([]InstalledRuntime, error)
	Use(runtimeName string, version string) (InstalledRuntime, error)
	UseProject(ctx context.Context, runtimeName string, version string) (InstalledRuntime, error)
	Current(runtimeName string) (InstalledRuntime, error)
	CurrentAll() ([]InstalledRuntime, error)
	CreateVenv(ctx context.Context, runtimeName string, path string) (VenvResult, error)
	InstallPackages(ctx context.Context, packageInstallerName string, options PackageInstallOptions) error
	Remove(runtimeName string, version string) error
	ListRemote(ctx context.Context, runtimeName string, selector string) ([]RemoteVersion, error)
	Latest(ctx context.Context, runtimeName string, selector string) (RemoteVersion, error)
	ExecPath() ([]string, error)
}

type Manager struct {
	registry                *Registry
	paths                   Paths
	platform                Platform
	client                  *http.Client
	downloadObserver        DownloadProgressObserver
	getwdFunc               func() (string, error)
	executablePathFunc      func() (string, error)
	ensureUserPathEntryFunc func(string) error
	runCommandFunc          func(context.Context, commandSpec) error
}

type installationRecord struct {
	Runtime     string    `json:"runtime"`
	Version     string    `json:"version"`
	Home        string    `json:"home"`
	Executable  string    `json:"executable"`
	InstalledAt time.Time `json:"installed_at"`
}

func DefaultManager() (*Manager, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return nil, err
	}
	return NewManager(paths, NewBuiltinRegistry()), nil
}

func NewManager(paths Paths, registry *Registry) *Manager {
	if registry == nil {
		registry = NewBuiltinRegistry()
	}

	return &Manager{
		registry:                registry,
		paths:                   paths,
		platform:                CurrentPlatform(),
		client:                  http.DefaultClient,
		getwdFunc:               os.Getwd,
		executablePathFunc:      os.Executable,
		ensureUserPathEntryFunc: ensureUserPathContains,
	}
}

func (m *Manager) Install(ctx context.Context, runtimeName string, version string) (InstalledRuntime, error) {
	provider, normalizedVersion, pkg, err := m.resolvePackageSpec(ctx, runtimeName, version)
	if err != nil {
		return InstalledRuntime{}, err
	}
	if err := ensurePaths(m.paths); err != nil {
		return InstalledRuntime{}, err
	}

	if existing, installDir, err := m.loadInstallation(provider.Name(), normalizedVersion); err == nil {
		active, err := m.isEffectiveActive(provider.Name(), normalizedVersion)
		if err != nil {
			return InstalledRuntime{}, err
		}
		return installationFromRecord(existing, installDir, active), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return InstalledRuntime{}, err
	}

	archivePath, err := m.download(ctx, provider.Name(), normalizedVersion, pkg)
	if err != nil {
		return InstalledRuntime{}, err
	}

	runtimeRoot := filepath.Join(m.paths.InstallRoot, provider.Name())
	if err := os.MkdirAll(runtimeRoot, 0o755); err != nil {
		return InstalledRuntime{}, err
	}

	tempDir, err := os.MkdirTemp(runtimeRoot, normalizedVersion+".tmp-*")
	if err != nil {
		return InstalledRuntime{}, err
	}

	keepTemp := false
	defer func() {
		if !keepTemp {
			_ = os.RemoveAll(tempDir)
		}
	}()

	if err := extractArchive(tempDir, archivePath, pkg.Archive); err != nil {
		return InstalledRuntime{}, err
	}

	metadata, err := provider.FinalizeInstall(tempDir, pkg)
	if err != nil {
		return InstalledRuntime{}, err
	}

	record := installationRecord{
		Runtime:     provider.Name(),
		Version:     normalizedVersion,
		Home:        metadata.Home,
		Executable:  metadata.Executable,
		InstalledAt: time.Now().UTC(),
	}

	if err := writeJSONAtomic(filepath.Join(tempDir, installationMetadataFileName), record); err != nil {
		return InstalledRuntime{}, err
	}

	finalDir := m.installDir(provider.Name(), normalizedVersion)
	if _, err := os.Stat(finalDir); err == nil {
		return InstalledRuntime{}, fmt.Errorf("runtime %s %s already exists", provider.Name(), normalizedVersion)
	} else if !errors.Is(err, os.ErrNotExist) {
		return InstalledRuntime{}, err
	}

	if err := os.Rename(tempDir, finalDir); err != nil {
		return InstalledRuntime{}, err
	}
	keepTemp = true

	return installationFromRecord(record, finalDir, false), nil
}

func (m *Manager) List(runtimeName string) ([]InstalledRuntime, error) {
	if err := ensurePaths(m.paths); err != nil {
		return nil, err
	}

	effectiveActive, err := m.effectiveSelections()
	if err != nil {
		return nil, err
	}

	var runtimesToScan []string
	if strings.TrimSpace(runtimeName) != "" {
		provider, err := m.registry.Provider(runtimeName)
		if err != nil {
			return nil, err
		}
		runtimesToScan = []string{provider.Name()}
	} else {
		runtimeEntries, err := os.ReadDir(m.paths.InstallRoot)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, nil
			}
			return nil, err
		}

		for _, entry := range runtimeEntries {
			if entry.IsDir() {
				runtimesToScan = append(runtimesToScan, entry.Name())
			}
		}
		sort.Strings(runtimesToScan)
	}

	items := make([]InstalledRuntime, 0)
	for _, runtimeKey := range runtimesToScan {
		versionRoot := filepath.Join(m.paths.InstallRoot, runtimeKey)
		versionEntries, err := os.ReadDir(versionRoot)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}

		for _, versionEntry := range versionEntries {
			if !versionEntry.IsDir() {
				continue
			}

			record, installDir, err := m.loadInstallationFromDir(runtimeKey, filepath.Join(versionRoot, versionEntry.Name()))
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return nil, err
			}
			active := effectiveActive[record.Runtime] == record.Version
			items = append(items, installationFromRecord(record, installDir, active))
		}
	}

	sort.Slice(items, func(i int, j int) bool {
		if items[i].Runtime != items[j].Runtime {
			return items[i].Runtime < items[j].Runtime
		}
		return compareVersionStrings(items[i].Version, items[j].Version) > 0
	})

	return items, nil
}

func (m *Manager) Use(runtimeName string, version string) (InstalledRuntime, error) {
	provider, normalizedVersion, err := m.resolveInstalledVersion(context.Background(), runtimeName, version)
	if err != nil {
		return InstalledRuntime{}, err
	}
	if err := ensurePaths(m.paths); err != nil {
		return InstalledRuntime{}, err
	}

	record, installDir, err := m.loadInstallation(provider.Name(), normalizedVersion)
	if err != nil {
		return InstalledRuntime{}, err
	}

	state, err := loadState(m.paths.StatePath)
	if err != nil {
		return InstalledRuntime{}, err
	}
	state.Active[provider.Name()] = normalizedVersion
	if err := saveState(m.paths.StatePath, state); err != nil {
		return InstalledRuntime{}, err
	}
	if err := m.syncGlobalShims(); err != nil {
		return InstalledRuntime{}, err
	}

	return installationFromRecord(record, installDir, true), nil
}

func (m *Manager) UseProject(ctx context.Context, runtimeName string, version string) (InstalledRuntime, error) {
	provider, normalizedVersion, err := m.resolveInstalledVersion(ctx, runtimeName, version)
	if err != nil {
		return InstalledRuntime{}, err
	}
	if err := ensurePaths(m.paths); err != nil {
		return InstalledRuntime{}, err
	}

	record, installDir, err := m.loadInstallation(provider.Name(), normalizedVersion)
	if err != nil {
		return InstalledRuntime{}, err
	}

	projectFile, err := m.projectConfigTarget()
	if err != nil {
		return InstalledRuntime{}, err
	}
	projectFile.Config.Runtimes[provider.Name()] = normalizedVersion
	if err := saveProjectConfig(projectFile.Path, projectFile.Config); err != nil {
		return InstalledRuntime{}, err
	}

	return installationFromRecord(record, installDir, true), nil
}

func (m *Manager) Current(runtimeName string) (InstalledRuntime, error) {
	if err := ensurePaths(m.paths); err != nil {
		return InstalledRuntime{}, err
	}

	provider, err := m.registry.Provider(runtimeName)
	if err != nil {
		return InstalledRuntime{}, err
	}

	selections, err := m.effectiveSelections()
	if err != nil {
		return InstalledRuntime{}, err
	}

	version, ok := selections[provider.Name()]
	if !ok {
		return InstalledRuntime{}, fmt.Errorf("no active version for runtime %s", provider.Name())
	}

	record, installDir, err := m.loadInstallation(provider.Name(), version)
	if err != nil {
		return InstalledRuntime{}, err
	}

	return installationFromRecord(record, installDir, true), nil
}

func (m *Manager) CurrentAll() ([]InstalledRuntime, error) {
	if err := ensurePaths(m.paths); err != nil {
		return nil, err
	}

	selections, err := m.effectiveSelections()
	if err != nil {
		return nil, err
	}
	if len(selections) == 0 {
		return nil, nil
	}

	runtimeNames := make([]string, 0, len(selections))
	for runtimeName := range selections {
		runtimeNames = append(runtimeNames, runtimeName)
	}
	sort.Strings(runtimeNames)

	items := make([]InstalledRuntime, 0, len(runtimeNames))
	for _, runtimeName := range runtimeNames {
		record, installDir, err := m.loadInstallation(runtimeName, selections[runtimeName])
		if err != nil {
			return nil, err
		}
		items = append(items, installationFromRecord(record, installDir, true))
	}

	return items, nil
}

func (m *Manager) CreateVenv(ctx context.Context, runtimeName string, path string) (VenvResult, error) {
	provider, err := m.registry.Provider(runtimeName)
	if err != nil {
		return VenvResult{}, err
	}
	venvProvider, ok := provider.(VenvProvider)
	if !ok {
		return VenvResult{}, fmt.Errorf("runtime %s does not support venv", provider.Name())
	}

	current, err := m.Current(provider.Name())
	if err != nil {
		return VenvResult{}, err
	}

	workdir, err := m.getwd()
	if err != nil {
		return VenvResult{}, err
	}

	targetPath, err := resolveVenvPath(workdir, path)
	if err != nil {
		return VenvResult{}, err
	}

	if err := ctx.Err(); err != nil {
		return VenvResult{}, err
	}

	venvExecutable, err := venvProvider.CreateVenv(ctx, m, current, workdir, targetPath)
	if err != nil {
		return VenvResult{}, fmt.Errorf("failed to create venv at %s: %w", targetPath, err)
	}

	return VenvResult{
		Runtime:    current.Runtime,
		Version:    current.Version,
		Path:       targetPath,
		Executable: venvExecutable,
	}, nil
}

func (m *Manager) Remove(runtimeName string, version string) error {
	provider, normalizedVersion, err := m.resolveInstalledVersion(context.Background(), runtimeName, version)
	if err != nil {
		return err
	}
	if err := ensurePaths(m.paths); err != nil {
		return err
	}

	installDir := m.installDir(provider.Name(), normalizedVersion)
	if _, err := os.Stat(installDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("runtime %s %s is not installed", provider.Name(), normalizedVersion)
		}
		return err
	}

	if err := os.RemoveAll(installDir); err != nil {
		return err
	}

	state, err := loadState(m.paths.StatePath)
	if err != nil {
		return err
	}
	if state.Active[provider.Name()] == normalizedVersion {
		delete(state.Active, provider.Name())
		if err := saveState(m.paths.StatePath, state); err != nil {
			return err
		}
	}
	if err := m.syncGlobalShims(); err != nil {
		return err
	}

	return nil
}

func (m *Manager) ListRemote(ctx context.Context, runtimeName string, selector string) ([]RemoteVersion, error) {
	provider, remoteProvider, err := m.remoteProvider(runtimeName)
	if err != nil {
		return nil, err
	}

	versions, err := remoteProvider.ListRemoteVersions(ctx, m.platform, m)
	if err != nil {
		return nil, err
	}
	sortRemoteVersions(versions)

	selection, err := newVersionSelector(provider, selector)
	if err != nil {
		return nil, err
	}
	if selection.kind == versionSelectorAll {
		return versions, nil
	}

	filtered := make([]RemoteVersion, 0, len(versions))
	for _, version := range versions {
		if selection.MatchRemote(version) {
			filtered = append(filtered, version)
		}
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("no remote version matches %q", selector)
	}
	return filtered, nil
}

func (m *Manager) Latest(ctx context.Context, runtimeName string, selector string) (RemoteVersion, error) {
	provider, remoteProvider, err := m.remoteProvider(runtimeName)
	if err != nil {
		return RemoteVersion{}, err
	}

	versions, err := remoteProvider.ListRemoteVersions(ctx, m.platform, m)
	if err != nil {
		return RemoteVersion{}, err
	}
	sortRemoteVersions(versions)

	query := selector
	if strings.TrimSpace(query) == "" {
		query = "stable"
	}

	selection, err := newVersionSelector(provider, query)
	if err != nil {
		return RemoteVersion{}, err
	}
	return selection.SelectRemote(versions)
}

func (m *Manager) ExecPath() ([]string, error) {
	current, err := m.CurrentAll()
	if err != nil {
		return nil, err
	}
	if len(current) == 0 {
		return nil, nil
	}

	seen := map[string]struct{}{}
	entries := make([]string, 0, len(current))
	for _, item := range current {
		pathEntry := filepath.Dir(item.Executable)
		if pathEntry == "" {
			continue
		}
		if _, exists := seen[pathEntry]; exists {
			continue
		}
		seen[pathEntry] = struct{}{}
		entries = append(entries, pathEntry)
	}

	return entries, nil
}

func (m *Manager) resolvePackageSpec(ctx context.Context, runtimeName string, version string) (Provider, string, PackageSpec, error) {
	provider, err := m.registry.Provider(runtimeName)
	if err != nil {
		return nil, "", PackageSpec{}, err
	}

	selection, err := newVersionSelector(provider, version)
	if err != nil {
		return nil, "", PackageSpec{}, err
	}

	if remoteProvider, ok := provider.(RemoteProvider); ok {
		versions, err := remoteProvider.ListRemoteVersions(ctx, m.platform, m)
		if err != nil {
			return nil, "", PackageSpec{}, err
		}
		sortRemoteVersions(versions)

		selected, err := selection.SelectRemote(versions)
		if err != nil {
			return nil, "", PackageSpec{}, err
		}

		pkg, err := provider.ResolvePackage(ctx, selected.Version, m.platform, m)
		if err != nil {
			return nil, "", PackageSpec{}, err
		}
		if pkg.Version == "" {
			pkg.Version = selected.Version
		}
		return provider, selected.Version, pkg, nil
	}

	normalizedVersion, err := provider.NormalizeVersion(version)
	if err != nil {
		return nil, "", PackageSpec{}, err
	}

	pkg, err := provider.ResolvePackage(ctx, normalizedVersion, m.platform, m)
	if err != nil {
		return nil, "", PackageSpec{}, err
	}
	if pkg.Version == "" {
		pkg.Version = normalizedVersion
	}
	return provider, normalizedVersion, pkg, nil
}

func (m *Manager) resolveInstalledVersion(ctx context.Context, runtimeName string, version string) (Provider, string, error) {
	provider, err := m.registry.Provider(runtimeName)
	if err != nil {
		return nil, "", err
	}

	selection, err := newVersionSelector(provider, version)
	if err != nil {
		return nil, "", err
	}

	installedVersions, err := m.installedVersions(provider.Name())
	if err != nil {
		return nil, "", err
	}
	if len(installedVersions) == 0 {
		return nil, "", fmt.Errorf("no installed versions for runtime %s", provider.Name())
	}

	switch selection.kind {
	case versionSelectorExact:
		if _, exists := installedVersions[selection.exact]; exists {
			return provider, selection.exact, nil
		}
		return nil, "", fmt.Errorf("runtime %s %s is not installed", provider.Name(), selection.exact)
	case versionSelectorPrefix:
		if remoteProvider, ok := provider.(RemoteProvider); ok {
			if resolved, err := m.selectInstalledFromRemote(ctx, remoteProvider, installedVersions, selection); err == nil {
				return provider, resolved, nil
			}
		}
		resolved, err := highestInstalledMatch(installedVersions, selection)
		if err != nil {
			return nil, "", err
		}
		return provider, resolved, nil
	case versionSelectorAlias:
		if remoteProvider, ok := provider.(RemoteProvider); ok {
			resolved, err := m.selectInstalledFromRemote(ctx, remoteProvider, installedVersions, selection)
			if err != nil {
				return nil, "", err
			}
			return provider, resolved, nil
		}
		if selection.alias == "lts" {
			return nil, "", fmt.Errorf("runtime %s does not support lts selection without remote metadata", provider.Name())
		}
		return provider, highestInstalledVersion(installedVersions), nil
	default:
		return nil, "", fmt.Errorf("unsupported version selector %q", selection.raw)
	}
}

func (m *Manager) selectInstalledFromRemote(ctx context.Context, remoteProvider RemoteProvider, installedVersions map[string]struct{}, selection versionSelector) (string, error) {
	versions, err := remoteProvider.ListRemoteVersions(ctx, m.platform, m)
	if err != nil {
		return "", err
	}
	sortRemoteVersions(versions)

	for _, version := range versions {
		if !selection.MatchRemote(version) {
			continue
		}
		if _, exists := installedVersions[version.Version]; exists {
			return version.Version, nil
		}
	}
	return "", fmt.Errorf("no installed version matches %q", selection.raw)
}

func highestInstalledMatch(installedVersions map[string]struct{}, selection versionSelector) (string, error) {
	matches := make([]string, 0, len(installedVersions))
	for version := range installedVersions {
		if selection.MatchVersion(version) {
			matches = append(matches, version)
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no installed version matches %q", selection.raw)
	}
	sortVersionsDescending(matches)
	return matches[0], nil
}

func highestInstalledVersion(installedVersions map[string]struct{}) string {
	versions := make([]string, 0, len(installedVersions))
	for version := range installedVersions {
		versions = append(versions, version)
	}
	sortVersionsDescending(versions)
	return versions[0]
}

func (m *Manager) installedVersions(runtimeName string) (map[string]struct{}, error) {
	versionRoot := filepath.Join(m.paths.InstallRoot, runtimeName)
	entries, err := os.ReadDir(versionRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]struct{}{}, nil
		}
		return nil, err
	}

	versions := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		record, _, err := m.loadInstallationFromDir(runtimeName, filepath.Join(versionRoot, entry.Name()))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		version := record.Version
		if version == "" {
			version = entry.Name()
		}
		versions[version] = struct{}{}
	}
	return versions, nil
}

func (m *Manager) remoteProvider(runtimeName string) (Provider, RemoteProvider, error) {
	provider, err := m.registry.Provider(runtimeName)
	if err != nil {
		return nil, nil, err
	}
	remoteProvider, ok := provider.(RemoteProvider)
	if !ok {
		return nil, nil, fmt.Errorf("runtime %s does not support remote metadata", provider.Name())
	}
	return provider, remoteProvider, nil
}

func (m *Manager) effectiveSelections() (map[string]string, error) {
	state, err := loadState(m.paths.StatePath)
	if err != nil {
		return nil, err
	}

	selections := make(map[string]string, len(state.Active))
	for runtimeName, version := range state.Active {
		selections[runtimeName] = version
	}

	projectFile, err := m.currentProjectConfig()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return selections, nil
		}
		return nil, err
	}

	for runtimeName, version := range projectFile.Config.Runtimes {
		provider, err := m.registry.Provider(runtimeName)
		if err != nil {
			return nil, err
		}
		selections[provider.Name()] = version
	}

	return selections, nil
}

func (m *Manager) isEffectiveActive(runtimeName string, version string) (bool, error) {
	selections, err := m.effectiveSelections()
	if err != nil {
		return false, err
	}
	return selections[runtimeName] == version, nil
}

func (m *Manager) currentProjectConfig() (ProjectConfigFile, error) {
	workdir, err := m.getwd()
	if err != nil {
		return ProjectConfigFile{}, err
	}
	return findProjectConfig(workdir)
}

func (m *Manager) projectConfigTarget() (ProjectConfigFile, error) {
	workdir, err := m.getwd()
	if err != nil {
		return ProjectConfigFile{}, err
	}
	return projectConfigTarget(workdir)
}

func (m *Manager) getwd() (string, error) {
	if m.getwdFunc == nil {
		return os.Getwd()
	}
	return m.getwdFunc()
}

func (m *Manager) httpClient() *http.Client {
	if m.client != nil {
		return m.client
	}
	return http.DefaultClient
}

func (m *Manager) installDir(runtimeName string, version string) string {
	return filepath.Join(m.paths.InstallRoot, runtimeName, version)
}

func (m *Manager) loadInstallation(runtimeName string, version string) (installationRecord, string, error) {
	return m.loadInstallationFromDir(runtimeName, m.installDir(runtimeName, version))
}

func (m *Manager) loadInstallationFromDir(runtimeName string, installDir string) (installationRecord, string, error) {
	metadataPath := filepath.Join(installDir, installationMetadataFileName)
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return installationRecord{}, "", err
	}

	var record installationRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return installationRecord{}, "", err
	}
	if record.Runtime == "" {
		record.Runtime = runtimeName
	}
	return record, installDir, nil
}

func installationFromRecord(record installationRecord, installDir string, active bool) InstalledRuntime {
	return InstalledRuntime{
		Runtime:     record.Runtime,
		Version:     record.Version,
		Home:        absoluteInstallPath(installDir, record.Home),
		Executable:  absoluteInstallPath(installDir, record.Executable),
		Active:      active,
		InstalledAt: record.InstalledAt,
	}
}

func absoluteInstallPath(installDir string, relative string) string {
	if relative == "" {
		return installDir
	}
	return filepath.Clean(filepath.Join(installDir, filepath.FromSlash(relative)))
}

func resolveVenvPath(workdir string, path string) (string, error) {
	target := strings.TrimSpace(path)
	if target == "" || target == "." {
		target = ".venv"
	}
	if filepath.IsAbs(target) {
		return filepath.Clean(target), nil
	}
	if strings.TrimSpace(workdir) == "" {
		return "", fmt.Errorf("working directory is not available")
	}
	return filepath.Clean(filepath.Join(workdir, target)), nil
}
