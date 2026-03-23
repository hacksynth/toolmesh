package runtimes

import (
	"context"
	"fmt"
	"strings"
)

type pypiPackageResponse struct {
	Info struct {
		Version      string   `json:"version"`
		RequiresDist []string `json:"requires_dist"`
	} `json:"info"`
	URLs []pypiDistribution `json:"urls"`
}

type pypiDistribution struct {
	Filename    string `json:"filename"`
	URL         string `json:"url"`
	Size        int64  `json:"size"`
	Packagetype string `json:"packagetype"`
	Digests     struct {
		SHA256 string `json:"sha256"`
	} `json:"digests"`
}

type pypiWheelRequirement struct {
	Name       string
	Specifiers string
}

func (m *Manager) fetchPyPIPackageMetadata(ctx context.Context, packageName string) (pypiPackageResponse, error) {
	var payload pypiPackageResponse
	if err := m.FetchJSON(ctx, fmt.Sprintf("https://pypi.org/pypi/%s/json", packageName), remoteMetadataTTL, &payload); err != nil {
		return pypiPackageResponse{}, err
	}
	return payload, nil
}

func (m *Manager) ensurePyPIWheel(ctx context.Context, requirement pypiWheelRequirement) (string, error) {
	pkg, err := m.resolvePyPIWheelPackage(ctx, requirement)
	if err != nil {
		return "", err
	}
	return m.download(ctx, requirement.Name, pkg.Version, pkg)
}

func (m *Manager) resolvePyPIWheelPackage(ctx context.Context, requirement pypiWheelRequirement) (PackageSpec, error) {
	payload, err := m.fetchPyPIPackageMetadata(ctx, requirement.Name)
	if err != nil {
		return PackageSpec{}, err
	}
	return selectPyPIWheelPackage(requirement, payload)
}

func selectPyPIWheelPackage(requirement pypiWheelRequirement, payload pypiPackageResponse) (PackageSpec, error) {
	version := strings.TrimSpace(payload.Info.Version)
	if version == "" {
		return PackageSpec{}, fmt.Errorf("%s metadata does not include a version", requirement.Name)
	}
	if err := validatePyPIVersionSpec(version, requirement.Specifiers); err != nil {
		return PackageSpec{}, fmt.Errorf("%s %s", requirement.Name, err)
	}

	var fallback *pypiDistribution
	for i := range payload.URLs {
		item := &payload.URLs[i]
		if item.Packagetype != "bdist_wheel" || !strings.HasSuffix(strings.ToLower(item.Filename), ".whl") {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(item.Filename), "none-any.whl") {
			continue
		}
		if fallback == nil {
			fallback = item
		}
		if pyPIWheelFilenameMatches(requirement.Name, version, item.Filename) {
			return pyPIWheelPackageFromDistribution(requirement.Name, version, *item)
		}
	}

	if fallback != nil {
		return pyPIWheelPackageFromDistribution(requirement.Name, version, *fallback)
	}

	return PackageSpec{}, fmt.Errorf("%s %s has no universal wheel distribution in PyPI metadata", requirement.Name, version)
}

func pyPIWheelPackageFromDistribution(packageName string, version string, item pypiDistribution) (PackageSpec, error) {
	if strings.TrimSpace(item.URL) == "" {
		return PackageSpec{}, fmt.Errorf("%s %s wheel metadata is missing a download URL", packageName, version)
	}

	return PackageSpec{
		Version: version,
		URL:     item.URL,
		Archive: ArchiveFormatZip,
		SHA256:  item.Digests.SHA256,
		Size:    item.Size,
	}, nil
}

func pyPIWheelFilenameMatches(packageName string, version string, fileName string) bool {
	lowerName := strings.ToLower(strings.TrimSpace(fileName))
	versionMarker := "-" + strings.ToLower(strings.TrimSpace(version)) + "-"
	index := strings.Index(lowerName, versionMarker)
	if index <= 0 {
		return false
	}

	distributionName := lowerName[:index]
	return normalizePyPIDistributionName(distributionName) == normalizePyPIDistributionName(packageName)
}

func normalizePyPIDistributionName(value string) string {
	lower := strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer("-", "_", ".", "_")
	return replacer.Replace(lower)
}

func validatePyPIVersionSpec(version string, specifiers string) error {
	for _, rawPart := range strings.Split(specifiers, ",") {
		part := strings.TrimSpace(rawPart)
		if part == "" {
			continue
		}

		operator, want, err := parseVersionComparison(part)
		if err != nil {
			return err
		}
		if !evaluateVersionComparison(version, operator, want) {
			return fmt.Errorf("latest version %s does not satisfy %s", version, specifiers)
		}
	}
	return nil
}

func parseVersionComparison(value string) (string, string, error) {
	trimmed := strings.TrimSpace(value)
	for _, operator := range []string{"<=", ">=", "==", "!=", "~=", "<", ">"} {
		if !strings.HasPrefix(trimmed, operator) {
			continue
		}

		target := strings.TrimSpace(trimmed[len(operator):])
		target = strings.Trim(target, "\"'")
		if target == "" {
			return "", "", fmt.Errorf("invalid version specifier %q", value)
		}
		return operator, target, nil
	}
	return "", "", fmt.Errorf("unsupported version specifier %q", value)
}

func evaluateVersionComparison(actual string, operator string, want string) bool {
	comparison := compareVersionStrings(actual, want)

	switch operator {
	case "<":
		return comparison < 0
	case "<=":
		return comparison <= 0
	case ">":
		return comparison > 0
	case ">=":
		return comparison >= 0
	case "==":
		return comparison == 0
	case "!=":
		return comparison != 0
	case "~=":
		if comparison < 0 {
			return false
		}
		prefix := numericParts(want)
		if len(prefix) > 1 {
			prefix = prefix[:len(prefix)-1]
		}
		return len(prefix) == 0 || hasNumericPrefix(actual, prefix)
	default:
		return false
	}
}
