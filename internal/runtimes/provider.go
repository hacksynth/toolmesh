package runtimes

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

type ArchiveFormat string

const (
	ArchiveFormatZip   ArchiveFormat = "zip"
	ArchiveFormatTarGz ArchiveFormat = "tar.gz"
)

type PackageSpec struct {
	Version string
	URL     string
	Archive ArchiveFormat
	SHA256  string
	Size    int64
}

type InstallMetadata struct {
	Home       string `json:"home"`
	Executable string `json:"executable"`
}

type RemoteVersion struct {
	Version string
	Stable  bool
	LTS     bool
}

type MetadataSource interface {
	FetchJSON(ctx context.Context, rawURL string, ttl time.Duration, target any) error
	FetchText(ctx context.Context, rawURL string, ttl time.Duration) (string, error)
}

type Provider interface {
	Name() string
	Aliases() []string
	NormalizeVersion(version string) (string, error)
	ResolvePackage(ctx context.Context, version string, platform Platform, source MetadataSource) (PackageSpec, error)
	FinalizeInstall(installDir string, pkg PackageSpec) (InstallMetadata, error)
}

type RemoteProvider interface {
	ListRemoteVersions(ctx context.Context, platform Platform, source MetadataSource) ([]RemoteVersion, error)
}

type VenvProvider interface {
	CreateVenv(ctx context.Context, manager *Manager, current InstalledRuntime, workdir string, targetPath string) (string, error)
}

type PackageInstallOptions struct {
	Args   []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type PackageInstaller interface {
	Name() string
	Install(ctx context.Context, manager *Manager, options PackageInstallOptions) error
}

type PackageInstallerProvider interface {
	PackageInstallers() []PackageInstaller
}

type Registry struct {
	providers         map[string]Provider
	aliases           map[string]string
	packageInstallers map[string]PackageInstaller
}

func NewRegistry(providers ...Provider) (*Registry, error) {
	registry := &Registry{
		providers:         make(map[string]Provider, len(providers)),
		aliases:           make(map[string]string, len(providers)*2),
		packageInstallers: make(map[string]PackageInstaller, len(providers)),
	}

	for _, provider := range providers {
		name := strings.ToLower(strings.TrimSpace(provider.Name()))
		if name == "" {
			return nil, fmt.Errorf("provider name cannot be empty")
		}
		if _, exists := registry.providers[name]; exists {
			return nil, fmt.Errorf("duplicate provider %q", name)
		}

		registry.providers[name] = provider
		registry.aliases[name] = name

		for _, alias := range provider.Aliases() {
			key := strings.ToLower(strings.TrimSpace(alias))
			if key == "" {
				continue
			}
			if existing, exists := registry.aliases[key]; exists && existing != name {
				return nil, fmt.Errorf("alias %q already belongs to %q", key, existing)
			}
			registry.aliases[key] = name
		}

		installerProvider, ok := provider.(PackageInstallerProvider)
		if !ok {
			continue
		}

		for _, installer := range installerProvider.PackageInstallers() {
			key := strings.ToLower(strings.TrimSpace(installer.Name()))
			if key == "" {
				return nil, fmt.Errorf("package installer name cannot be empty for runtime %q", name)
			}
			if _, exists := registry.packageInstallers[key]; exists {
				return nil, fmt.Errorf("duplicate package installer %q", key)
			}
			registry.packageInstallers[key] = installer
		}
	}

	return registry, nil
}

func NewBuiltinRegistry() *Registry {
	registry, err := NewRegistry(
		pythonProvider{},
		gitProvider{},
		cmakeProvider{},
		nodejsProvider{},
		javaProvider{},
		goProvider{},
		mingwProvider{},
	)
	if err != nil {
		panic(err)
	}
	return registry
}

func (r *Registry) Provider(name string) (Provider, error) {
	if r == nil {
		return nil, fmt.Errorf("registry is nil")
	}

	key := strings.ToLower(strings.TrimSpace(name))
	canonical, ok := r.aliases[key]
	if !ok {
		return nil, fmt.Errorf("unsupported runtime %q", name)
	}

	provider, ok := r.providers[canonical]
	if !ok {
		return nil, fmt.Errorf("provider not found for runtime %q", name)
	}

	return provider, nil
}

func (r *Registry) Providers() []Provider {
	if r == nil {
		return nil
	}

	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)

	providers := make([]Provider, 0, len(names))
	for _, name := range names {
		providers = append(providers, r.providers[name])
	}

	return providers
}

func (r *Registry) PackageInstaller(name string) (PackageInstaller, error) {
	if r == nil {
		return nil, fmt.Errorf("registry is nil")
	}

	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return nil, fmt.Errorf("package installer name cannot be empty")
	}

	installer, ok := r.packageInstallers[key]
	if !ok {
		return nil, fmt.Errorf("unsupported package installer %q", name)
	}

	return installer, nil
}
