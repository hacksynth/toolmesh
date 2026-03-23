package runtimes

import (
	"strings"
	"testing"
)

func TestPythonVenvBootstrapRequirementsFilterMarkers(t *testing.T) {
	requirements, err := pythonVenvBootstrapRequirements([]string{
		"distlib<1,>=0.3.7",
		"filelock<4,>=3.24.2; python_version >= \"3.10\"",
		"filelock<=3.19.1,>=3.16.1; python_version < \"3.10\"",
		"platformdirs<5,>=3.9.1",
		"python-discovery>=1",
		"furo>=2025.12.19; extra == \"docs\"",
	}, "3.12.2")
	if err != nil {
		t.Fatalf("pythonVenvBootstrapRequirements failed: %v", err)
	}

	want := []pypiWheelRequirement{
		{Name: "distlib", Specifiers: "<1,>=0.3.7"},
		{Name: "filelock", Specifiers: "<4,>=3.24.2"},
		{Name: "platformdirs", Specifiers: "<5,>=3.9.1"},
		{Name: "python-discovery", Specifiers: ">=1"},
	}
	if len(requirements) != len(want) {
		t.Fatalf("unexpected requirement count: got %#v want %#v", requirements, want)
	}
	for i := range want {
		if requirements[i] != want[i] {
			t.Fatalf("unexpected requirement at %d: got %#v want %#v", i, requirements[i], want[i])
		}
	}
}

func TestValidatePyPIVersionSpecRejectsIncompatibleLatest(t *testing.T) {
	err := validatePyPIVersionSpec("5.0.0", "<5,>=3.9.1")
	if err == nil {
		t.Fatalf("expected incompatible version error")
	}
	if !strings.Contains(err.Error(), "does not satisfy <5,>=3.9.1") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSelectPyPIWheelPackageMatchesNormalizedFileName(t *testing.T) {
	payload := pypiPackageResponse{}
	payload.Info.Version = "1.2.0"
	payload.URLs = []pypiDistribution{
		{
			Filename:    "python_discovery-1.2.0-py3-none-any.whl",
			URL:         "https://files.pythonhosted.org/packages/python_discovery-1.2.0-py3-none-any.whl",
			Packagetype: "bdist_wheel",
		},
	}

	pkg, err := selectPyPIWheelPackage(pypiWheelRequirement{Name: "python-discovery", Specifiers: ">=1"}, payload)
	if err != nil {
		t.Fatalf("selectPyPIWheelPackage failed: %v", err)
	}
	if pkg.Version != "1.2.0" {
		t.Fatalf("unexpected package version: %#v", pkg)
	}
	if pkg.URL != "https://files.pythonhosted.org/packages/python_discovery-1.2.0-py3-none-any.whl" {
		t.Fatalf("unexpected package URL: %#v", pkg)
	}
}
