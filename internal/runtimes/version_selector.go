package runtimes

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	numericPrefixPattern = regexp.MustCompile(`^\d+(?:\.\d+)?$`)
	numericTokenPattern  = regexp.MustCompile(`\d+`)
)

type versionSelectorKind string

const (
	versionSelectorAll    versionSelectorKind = "all"
	versionSelectorExact  versionSelectorKind = "exact"
	versionSelectorPrefix versionSelectorKind = "prefix"
	versionSelectorAlias  versionSelectorKind = "alias"
)

type versionSelector struct {
	kind        versionSelectorKind
	raw         string
	exact       string
	alias       string
	numericBits []int
}

func newVersionSelector(provider Provider, raw string) (versionSelector, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return versionSelector{kind: versionSelectorAll}, nil
	}

	lower := strings.ToLower(trimmed)
	switch lower {
	case "latest", "stable", "lts":
		return versionSelector{
			kind:  versionSelectorAlias,
			raw:   trimmed,
			alias: lower,
		}, nil
	}

	if numericPrefixPattern.MatchString(trimmed) {
		return versionSelector{
			kind:        versionSelectorPrefix,
			raw:         trimmed,
			numericBits: numericParts(trimmed),
		}, nil
	}

	exact, err := provider.NormalizeVersion(trimmed)
	if err != nil {
		return versionSelector{}, err
	}

	return versionSelector{
		kind:  versionSelectorExact,
		raw:   trimmed,
		exact: exact,
	}, nil
}

func (s versionSelector) MatchRemote(version RemoteVersion) bool {
	switch s.kind {
	case versionSelectorAll:
		return true
	case versionSelectorExact:
		return version.Version == s.exact
	case versionSelectorPrefix:
		return hasNumericPrefix(version.Version, s.numericBits)
	case versionSelectorAlias:
		switch s.alias {
		case "latest", "stable":
			return version.Stable
		case "lts":
			return version.LTS
		default:
			return false
		}
	default:
		return false
	}
}

func (s versionSelector) MatchVersion(version string) bool {
	switch s.kind {
	case versionSelectorAll:
		return true
	case versionSelectorExact:
		return version == s.exact
	case versionSelectorPrefix:
		return hasNumericPrefix(version, s.numericBits)
	case versionSelectorAlias:
		return false
	default:
		return false
	}
}

func (s versionSelector) SelectRemote(versions []RemoteVersion) (RemoteVersion, error) {
	for _, version := range versions {
		if s.MatchRemote(version) {
			return version, nil
		}
	}
	return RemoteVersion{}, fmt.Errorf("no remote version matches %q", s.raw)
}

func sortRemoteVersions(versions []RemoteVersion) {
	sort.SliceStable(versions, func(i int, j int) bool {
		return compareVersionStrings(versions[i].Version, versions[j].Version) > 0
	})
}

func sortVersionsDescending(versions []string) {
	sort.SliceStable(versions, func(i int, j int) bool {
		return compareVersionStrings(versions[i], versions[j]) > 0
	})
}

func compareVersionStrings(left string, right string) int {
	leftParts := numericParts(left)
	rightParts := numericParts(right)

	limit := len(leftParts)
	if len(rightParts) < limit {
		limit = len(rightParts)
	}

	for index := 0; index < limit; index++ {
		if leftParts[index] > rightParts[index] {
			return 1
		}
		if leftParts[index] < rightParts[index] {
			return -1
		}
	}

	if len(leftParts) > len(rightParts) {
		return 1
	}
	if len(leftParts) < len(rightParts) {
		return -1
	}

	if left > right {
		return 1
	}
	if left < right {
		return -1
	}
	return 0
}

func numericParts(value string) []int {
	matches := numericTokenPattern.FindAllString(value, -1)
	parts := make([]int, 0, len(matches))
	for _, match := range matches {
		number, err := strconv.Atoi(match)
		if err != nil {
			continue
		}
		parts = append(parts, number)
	}
	return parts
}

func hasNumericPrefix(version string, prefix []int) bool {
	parts := numericParts(version)
	if len(parts) < len(prefix) {
		return false
	}

	for index, part := range prefix {
		if parts[index] != part {
			return false
		}
	}
	return true
}
