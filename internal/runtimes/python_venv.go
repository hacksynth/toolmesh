package runtimes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

const pythonVenvRunnerScript = "" +
	"import runpy, sys\n" +
	"sep = sys.argv.index('--')\n" +
	"wheel_paths = sys.argv[1:sep]\n" +
	"args = sys.argv[sep + 1:]\n" +
	"for wheel in reversed(wheel_paths):\n" +
	"    sys.path.insert(0, wheel)\n" +
	"sys.argv = ['virtualenv', '--no-pip', '--no-periodic-update'] + args\n" +
	"runpy.run_module('virtualenv', run_name='__main__', alter_sys=True)\n"

func (pythonProvider) CreateVenv(ctx context.Context, manager *Manager, current InstalledRuntime, workdir string, targetPath string) (string, error) {
	return manager.createPythonVenv(ctx, current, workdir, targetPath)
}

func (m *Manager) createPythonVenv(ctx context.Context, current InstalledRuntime, workdir string, targetPath string) (string, error) {
	baseExecutable := strings.TrimSpace(current.Executable)
	if baseExecutable == "" {
		return "", fmt.Errorf("python runtime executable is not configured")
	}
	if err := requireRegularFile(baseExecutable); err != nil {
		return "", fmt.Errorf("python runtime executable is unavailable: %w", err)
	}

	wheelPaths, err := m.ensurePythonVenvBootstrapWheels(ctx, current.Version)
	if err != nil {
		return "", err
	}

	command := buildPythonVenvCommand(baseExecutable, workdir, targetPath, wheelPaths)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output

	if err := m.runCommand(ctx, command); err != nil {
		return "", formatPythonVenvBootstrapError(err, output.String())
	}

	venv, err := loadPythonVenv(targetPath)
	if err != nil {
		return "", fmt.Errorf("created virtual environment is incomplete: %w", err)
	}
	return venv.Executable, nil
}

func (m *Manager) ensurePythonVenvBootstrapWheels(ctx context.Context, pythonVersion string) ([]string, error) {
	virtualenvRequirement := pypiWheelRequirement{Name: "virtualenv"}
	virtualenvMetadata, err := m.fetchPyPIPackageMetadata(ctx, virtualenvRequirement.Name)
	if err != nil {
		return nil, err
	}

	virtualenvPackage, err := selectPyPIWheelPackage(virtualenvRequirement, virtualenvMetadata)
	if err != nil {
		return nil, err
	}

	wheelPaths := make([]string, 0, len(virtualenvMetadata.Info.RequiresDist)+1)
	virtualenvWheelPath, err := m.download(ctx, virtualenvRequirement.Name, virtualenvPackage.Version, virtualenvPackage)
	if err != nil {
		return nil, err
	}
	wheelPaths = append(wheelPaths, virtualenvWheelPath)

	requirements, err := pythonVenvBootstrapRequirements(virtualenvMetadata.Info.RequiresDist, pythonVersion)
	if err != nil {
		return nil, err
	}

	for _, requirement := range requirements {
		wheelPath, err := m.ensurePyPIWheel(ctx, requirement)
		if err != nil {
			return nil, err
		}
		wheelPaths = append(wheelPaths, wheelPath)
	}

	return wheelPaths, nil
}

func buildPythonVenvCommand(baseExecutable string, workdir string, targetPath string, wheelPaths []string) commandSpec {
	args := make([]string, 0, len(wheelPaths)+4)
	args = append(args, "-c", pythonVenvRunnerScript)
	args = append(args, wheelPaths...)
	args = append(args, "--", targetPath)

	return commandSpec{
		Path: baseExecutable,
		Args: args,
		Dir:  workdir,
		Env:  setEnvValue(os.Environ(), "VIRTUAL_ENV", ""),
	}
}

func formatPythonVenvBootstrapError(err error, output string) error {
	message := strings.TrimSpace(output)
	if message == "" {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return fmt.Errorf("python venv bootstrap failed")
		}
		return fmt.Errorf("failed to execute python venv bootstrap: %w", err)
	}

	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return fmt.Errorf("%s", message)
	}
	return fmt.Errorf("failed to execute python venv bootstrap: %s", message)
}

func pythonVenvBootstrapRequirements(rawRequirements []string, pythonVersion string) ([]pypiWheelRequirement, error) {
	requirements := make([]pypiWheelRequirement, 0, len(rawRequirements))
	seen := make(map[string]struct{}, len(rawRequirements))

	for _, rawRequirement := range rawRequirements {
		requirement, include, err := parsePythonVenvBootstrapRequirement(rawRequirement, pythonVersion)
		if err != nil {
			return nil, err
		}
		if !include {
			continue
		}

		key := strings.ToLower(requirement.Name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		requirements = append(requirements, requirement)
	}

	return requirements, nil
}

func parsePythonVenvBootstrapRequirement(rawRequirement string, pythonVersion string) (pypiWheelRequirement, bool, error) {
	requirementPart, markerPart, _ := strings.Cut(rawRequirement, ";")
	applies, err := pythonRequirementMarkerApplies(markerPart, pythonVersion)
	if err != nil {
		return pypiWheelRequirement{}, false, err
	}
	if !applies {
		return pypiWheelRequirement{}, false, nil
	}

	requirementPart = strings.TrimSpace(requirementPart)
	if requirementPart == "" {
		return pypiWheelRequirement{}, false, fmt.Errorf("invalid requirement %q", rawRequirement)
	}

	name, specifiers, found := requirementNameAndSpecifiers(requirementPart)
	if !found {
		return pypiWheelRequirement{}, false, fmt.Errorf("invalid requirement %q", rawRequirement)
	}

	return pypiWheelRequirement{
		Name:       name,
		Specifiers: specifiers,
	}, true, nil
}

func requirementNameAndSpecifiers(value string) (string, string, bool) {
	indexes := requirementNameBoundaryPattern.FindStringIndex(value)
	if indexes == nil || indexes[0] != 0 {
		return "", "", false
	}

	name := value[:indexes[1]]
	specifiers := strings.TrimSpace(value[indexes[1]:])
	return name, specifiers, true
}

var requirementNameBoundaryPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+`)

func pythonRequirementMarkerApplies(marker string, pythonVersion string) (bool, error) {
	trimmed := strings.TrimSpace(marker)
	if trimmed == "" {
		return true, nil
	}

	for _, clause := range strings.Split(trimmed, " or ") {
		matches, err := pythonRequirementMarkerAll(strings.TrimSpace(clause), pythonVersion)
		if err != nil {
			return false, err
		}
		if matches {
			return true, nil
		}
	}

	return false, nil
}

func pythonRequirementMarkerAll(marker string, pythonVersion string) (bool, error) {
	for _, term := range strings.Split(marker, " and ") {
		matches, err := pythonRequirementMarkerTerm(strings.TrimSpace(term), pythonVersion)
		if err != nil {
			return false, err
		}
		if !matches {
			return false, nil
		}
	}
	return true, nil
}

func pythonRequirementMarkerTerm(marker string, pythonVersion string) (bool, error) {
	trimmed := strings.Trim(strings.TrimSpace(marker), "()")
	if trimmed == "" {
		return true, nil
	}

	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "extra ") || strings.HasPrefix(lower, "extra==") {
		return false, nil
	}
	if strings.HasPrefix(lower, "extra ==") {
		return false, nil
	}

	const prefix = "python_version"
	if !strings.HasPrefix(lower, prefix) {
		return false, fmt.Errorf("unsupported dependency marker %q", marker)
	}

	operator, want, err := parseVersionComparison(strings.TrimSpace(trimmed[len(prefix):]))
	if err != nil {
		return false, err
	}
	return evaluateVersionComparison(pythonVersion, operator, want), nil
}
