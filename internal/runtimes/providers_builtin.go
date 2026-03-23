package runtimes

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var exactVersionPattern = regexp.MustCompile(`^[0-9A-Za-z.+_-]+$`)

type pythonProvider struct{}

func (pythonProvider) Name() string {
	return "python"
}

func (pythonProvider) Aliases() []string {
	return []string{"py"}
}

func (pythonProvider) NormalizeVersion(version string) (string, error) {
	return normalizeVersion(version, "python")
}

func (pythonProvider) FinalizeInstall(installDir string, _ PackageSpec) (InstallMetadata, error) {
	return finalizeByExecutable(installDir, "python.exe", 0)
}

type gitProvider struct{}

func (gitProvider) Name() string {
	return "git"
}

func (gitProvider) Aliases() []string {
	return []string{"gitforwindows"}
}

func (gitProvider) NormalizeVersion(version string) (string, error) {
	return normalizeGitVersion(version)
}

func (gitProvider) FinalizeInstall(installDir string, _ PackageSpec) (InstallMetadata, error) {
	return finalizeByExecutable(installDir, "git.exe", 1)
}

type cmakeProvider struct{}

func (cmakeProvider) Name() string {
	return "cmake"
}

func (cmakeProvider) Aliases() []string {
	return nil
}

func (cmakeProvider) NormalizeVersion(version string) (string, error) {
	return normalizeCMakeVersion(version)
}

func (cmakeProvider) FinalizeInstall(installDir string, _ PackageSpec) (InstallMetadata, error) {
	return finalizeByExecutable(installDir, "cmake.exe", 1)
}

type nodejsProvider struct{}

func (nodejsProvider) Name() string {
	return "nodejs"
}

func (nodejsProvider) Aliases() []string {
	return []string{"node"}
}

func (nodejsProvider) NormalizeVersion(version string) (string, error) {
	normalized, err := normalizeVersion(version, "nodejs")
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(normalized, "v"), nil
}

func (nodejsProvider) FinalizeInstall(installDir string, _ PackageSpec) (InstallMetadata, error) {
	return finalizeByExecutable(installDir, "node.exe", 0)
}

type javaProvider struct{}

func (javaProvider) Name() string {
	return "java"
}

func (javaProvider) Aliases() []string {
	return nil
}

func (javaProvider) NormalizeVersion(version string) (string, error) {
	normalized, err := normalizeVersion(version, "java")
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(normalized, "jdk-"), nil
}

func (javaProvider) FinalizeInstall(installDir string, _ PackageSpec) (InstallMetadata, error) {
	return finalizeByExecutable(installDir, "java.exe", 1)
}

type goProvider struct{}

func (goProvider) Name() string {
	return "go"
}

func (goProvider) Aliases() []string {
	return []string{"golang"}
}

func (goProvider) NormalizeVersion(version string) (string, error) {
	normalized, err := normalizeVersion(version, "go")
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(normalized, "go"), nil
}

func (goProvider) FinalizeInstall(installDir string, _ PackageSpec) (InstallMetadata, error) {
	return finalizeByExecutable(installDir, "go.exe", 1)
}

type mingwProvider struct{}

func (mingwProvider) Name() string {
	return "mingw"
}

func (mingwProvider) Aliases() []string {
	return []string{"gcc", "mingw-w64"}
}

func (mingwProvider) NormalizeVersion(version string) (string, error) {
	normalized, err := normalizeVersion(version, "mingw")
	if err != nil {
		return "", err
	}
	return strings.ToLower(normalized), nil
}

func (mingwProvider) FinalizeInstall(installDir string, _ PackageSpec) (InstallMetadata, error) {
	return finalizeByExecutable(installDir, "gcc.exe", 1)
}

func normalizeVersion(version string, runtimeName string) (string, error) {
	normalized := strings.TrimSpace(version)
	if normalized == "" {
		return "", fmt.Errorf("%s version cannot be empty", runtimeName)
	}
	if !exactVersionPattern.MatchString(normalized) {
		return "", fmt.Errorf("invalid %s version %q", runtimeName, version)
	}
	return normalized, nil
}

func normalizeGitVersion(version string) (string, error) {
	normalized, err := normalizeVersion(version, "git")
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(strings.ToLower(normalized), "v") {
		normalized = normalized[1:]
	}
	return strings.ReplaceAll(normalized, ".windows.", "."), nil
}

func normalizeCMakeVersion(version string) (string, error) {
	normalized, err := normalizeVersion(version, "cmake")
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(strings.ToLower(normalized), "v") {
		normalized = normalized[1:]
	}
	return normalized, nil
}

func finalizeByExecutable(installDir string, executableName string, homeParentSteps int) (InstallMetadata, error) {
	relativeExecutable, err := findShortestMatchingFile(installDir, executableName)
	if err != nil {
		return InstallMetadata{}, err
	}

	home := filepath.Dir(relativeExecutable)
	for i := 0; i < homeParentSteps; i++ {
		home = filepath.Dir(home)
	}

	relativeHome, err := normalizeRelativePath(home)
	if err != nil {
		return InstallMetadata{}, err
	}
	relativeExecutable, err = normalizeRelativePath(relativeExecutable)
	if err != nil {
		return InstallMetadata{}, err
	}

	return InstallMetadata{
		Home:       relativeHome,
		Executable: relativeExecutable,
	}, nil
}

func findShortestMatchingFile(rootDir string, fileName string) (string, error) {
	var bestMatch string
	err := filepath.WalkDir(rootDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.EqualFold(entry.Name(), fileName) {
			return nil
		}

		relativePath, err := filepath.Rel(rootDir, path)
		if err != nil {
			return err
		}

		if bestMatch == "" || len(relativePath) < len(bestMatch) {
			bestMatch = relativePath
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if bestMatch == "" {
		return "", fmt.Errorf("failed to locate %s under %s", fileName, rootDir)
	}
	return bestMatch, nil
}

func normalizeRelativePath(value string) (string, error) {
	cleaned := filepath.Clean(value)
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("path must be relative: %s", value)
	}
	if cleaned == "." {
		return ".", nil
	}
	if strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("path escapes install root: %s", value)
	}
	return filepath.ToSlash(cleaned), nil
}
