package runtimes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	pipMetadataURL         = "https://pypi.org/pypi/pip/json"
	pipInstallRunnerScript = "import sys; wheel = sys.argv[1]; args = sys.argv[3:] if len(sys.argv) > 2 and sys.argv[2] == '--' else sys.argv[2:]; sys.path.insert(0, wheel); from pip._internal.cli.main import main as pip_main; raise SystemExit(pip_main(args))"
)

type PipInstallOptions = PackageInstallOptions

type commandSpec struct {
	Path   string
	Args   []string
	Dir    string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type pythonVenv struct {
	Root       string
	Executable string
}

type pythonPipInstaller struct{}

func (pythonProvider) PackageInstallers() []PackageInstaller {
	return []PackageInstaller{pythonPipInstaller{}}
}

func (pythonPipInstaller) Name() string {
	return "pip"
}

func (m *Manager) InstallPackages(ctx context.Context, packageInstallerName string, options PackageInstallOptions) error {
	installer, err := m.registry.PackageInstaller(packageInstallerName)
	if err != nil {
		return err
	}
	return installer.Install(ctx, m, options)
}

func (m *Manager) PipInstall(ctx context.Context, options PipInstallOptions) error {
	return m.InstallPackages(ctx, "pip", options)
}

func (pythonPipInstaller) Install(ctx context.Context, manager *Manager, options PackageInstallOptions) error {
	if len(options.Args) == 0 {
		return fmt.Errorf("pip install arguments are required")
	}

	workdir, err := manager.getwd()
	if err != nil {
		return err
	}

	venv, err := manager.discoverPythonVenv(workdir)
	if err != nil {
		return err
	}

	pipWheelPath, err := manager.ensurePipWheel(ctx)
	if err != nil {
		return err
	}

	command := buildPipInstallCommand(venv, workdir, pipWheelPath, options)
	if err := manager.runCommand(ctx, command); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return fmt.Errorf("pip install failed")
		}
		return fmt.Errorf("failed to execute pip install: %w", err)
	}

	return nil
}

func (m *Manager) ensurePipWheel(ctx context.Context) (string, error) {
	pkg, err := m.resolvePipWheelPackage(ctx)
	if err != nil {
		return "", err
	}

	path, err := m.download(ctx, "pip", pkg.Version, pkg)
	if err != nil {
		return "", err
	}
	return path, nil
}

func (m *Manager) resolvePipWheelPackage(ctx context.Context) (PackageSpec, error) {
	payload, err := m.fetchPyPIPackageMetadata(ctx, "pip")
	if err != nil {
		return PackageSpec{}, err
	}
	return selectPipWheelPackage(payload)
}

func selectPipWheelPackage(payload pypiPackageResponse) (PackageSpec, error) {
	version := strings.TrimSpace(payload.Info.Version)
	if version == "" {
		return PackageSpec{}, fmt.Errorf("pip metadata does not include a version")
	}

	var fallback *pypiDistribution
	for i := range payload.URLs {
		item := &payload.URLs[i]
		if item.Packagetype != "bdist_wheel" || !strings.HasSuffix(strings.ToLower(item.Filename), ".whl") {
			continue
		}
		if fallback == nil {
			fallback = item
		}

		lowerName := strings.ToLower(item.Filename)
		if strings.HasPrefix(item.Filename, "pip-"+version+"-") && strings.HasSuffix(lowerName, "py3-none-any.whl") {
			return pipPackageFromDistribution(version, *item)
		}
	}

	if fallback != nil {
		return pipPackageFromDistribution(version, *fallback)
	}

	return PackageSpec{}, fmt.Errorf("pip %s has no wheel distribution in PyPI metadata", version)
}

func pipPackageFromDistribution(version string, item pypiDistribution) (PackageSpec, error) {
	if strings.TrimSpace(item.URL) == "" {
		return PackageSpec{}, fmt.Errorf("pip %s wheel metadata is missing a download URL", version)
	}

	return PackageSpec{
		Version: version,
		URL:     item.URL,
		Archive: ArchiveFormatZip,
		SHA256:  item.Digests.SHA256,
		Size:    item.Size,
	}, nil
}

func buildPipInstallCommand(venv pythonVenv, workdir string, pipWheelPath string, options PackageInstallOptions) commandSpec {
	args := make([]string, 0, len(options.Args)+5)
	args = append(args, "-c", pipInstallRunnerScript, pipWheelPath, "--", "install")
	args = append(args, options.Args...)

	return commandSpec{
		Path:   venv.Executable,
		Args:   args,
		Dir:    workdir,
		Env:    setEnvValue(os.Environ(), "VIRTUAL_ENV", venv.Root),
		Stdin:  options.Stdin,
		Stdout: options.Stdout,
		Stderr: options.Stderr,
	}
}

func (m *Manager) discoverPythonVenv(workdir string) (pythonVenv, error) {
	if rawVenv, ok := os.LookupEnv("VIRTUAL_ENV"); ok && strings.TrimSpace(rawVenv) != "" {
		root, err := resolveVirtualEnvRoot(workdir, rawVenv)
		if err != nil {
			return pythonVenv{}, err
		}

		venv, err := loadPythonVenv(root)
		if err != nil {
			return pythonVenv{}, fmt.Errorf("VIRTUAL_ENV points to %s, but it is not a usable Python virtual environment: %w", root, err)
		}
		return venv, nil
	}

	root, found, err := findNearestProjectVenv(workdir)
	if err != nil {
		return pythonVenv{}, err
	}
	if !found {
		return pythonVenv{}, fmt.Errorf("toolmesh pip install requires an existing Python virtual environment. Set VIRTUAL_ENV or create one with `toolmesh python venv` or `toolmesh venv python`")
	}

	venv, err := loadPythonVenv(root)
	if err != nil {
		return pythonVenv{}, fmt.Errorf("found %s, but it is not a usable Python virtual environment: %w", root, err)
	}
	return venv, nil
}

func resolveVirtualEnvRoot(workdir string, rawVenv string) (string, error) {
	root := strings.TrimSpace(rawVenv)
	if root == "" {
		return "", fmt.Errorf("VIRTUAL_ENV is empty")
	}
	if filepath.IsAbs(root) {
		return filepath.Clean(root), nil
	}
	if strings.TrimSpace(workdir) == "" {
		return "", fmt.Errorf("working directory is not available")
	}
	return filepath.Clean(filepath.Join(workdir, root)), nil
}

func findNearestProjectVenv(workdir string) (string, bool, error) {
	if strings.TrimSpace(workdir) == "" {
		return "", false, fmt.Errorf("working directory is not available")
	}

	currentDir, err := filepath.Abs(workdir)
	if err != nil {
		return "", false, err
	}

	for {
		candidate := filepath.Join(currentDir, ".venv")
		info, err := os.Stat(candidate)
		if err == nil {
			if !info.IsDir() {
				return candidate, true, fmt.Errorf("%s is not a directory", candidate)
			}
			return candidate, true, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", false, err
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			return "", false, nil
		}
		currentDir = parentDir
	}
}

func loadPythonVenv(root string) (pythonVenv, error) {
	cleanRoot := filepath.Clean(root)
	info, err := os.Stat(cleanRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return pythonVenv{}, fmt.Errorf("%s does not exist", cleanRoot)
		}
		return pythonVenv{}, err
	}
	if !info.IsDir() {
		return pythonVenv{}, fmt.Errorf("%s is not a directory", cleanRoot)
	}

	venvConfigPath := filepath.Join(cleanRoot, "pyvenv.cfg")
	if err := requireRegularFile(venvConfigPath); err != nil {
		return pythonVenv{}, err
	}

	pythonPath := filepath.Join(cleanRoot, "Scripts", "python.exe")
	if err := requireRegularFile(pythonPath); err != nil {
		return pythonVenv{}, err
	}

	return pythonVenv{
		Root:       cleanRoot,
		Executable: pythonPath,
	}, nil
}

func requireRegularFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s does not exist", path)
		}
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	return nil
}

func setEnvValue(environment []string, key string, value string) []string {
	entry := key + "=" + value
	for i, item := range environment {
		currentKey, _, found := strings.Cut(item, "=")
		if !found {
			continue
		}
		if strings.EqualFold(currentKey, key) {
			updated := append([]string(nil), environment...)
			updated[i] = entry
			return updated
		}
	}

	updated := append([]string(nil), environment...)
	return append(updated, entry)
}

func (m *Manager) runCommand(ctx context.Context, spec commandSpec) error {
	if m != nil && m.runCommandFunc != nil {
		return m.runCommandFunc(ctx, spec)
	}
	return defaultRunCommand(ctx, spec)
}

func defaultRunCommand(ctx context.Context, spec commandSpec) error {
	command := exec.CommandContext(ctx, spec.Path, spec.Args...)
	command.Dir = spec.Dir
	command.Env = spec.Env
	command.Stdin = spec.Stdin
	command.Stdout = spec.Stdout
	command.Stderr = spec.Stderr
	return command.Run()
}
