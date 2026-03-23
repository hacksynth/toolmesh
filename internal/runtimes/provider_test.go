package runtimes

import "testing"

func TestBuiltinRegistrySupportsCommonBuildToolAliases(t *testing.T) {
	registry := NewBuiltinRegistry()

	testCases := map[string]string{
		"git":           "git",
		"gitforwindows": "git",
		"cmake":         "cmake",
		"mingw":         "mingw",
		"mingw-w64":     "mingw",
		"gcc":           "mingw",
	}

	for input, want := range testCases {
		input := input
		want := want
		t.Run(input, func(t *testing.T) {
			provider, err := registry.Provider(input)
			if err != nil {
				t.Fatalf("expected provider for %s: %v", input, err)
			}
			if provider.Name() != want {
				t.Fatalf("unexpected provider for %s: got %s want %s", input, provider.Name(), want)
			}
		})
	}
}
