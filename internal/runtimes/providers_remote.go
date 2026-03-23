package runtimes

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

const remoteMetadataTTL = 6 * time.Hour

const (
	pythonWindowsDownloadsURL = "https://www.python.org/downloads/windows/"
	gitForWindowsReleasesURL  = "https://api.github.com/repos/git-for-windows/git/releases?per_page=100"
	cmakeReleasesURL          = "https://api.github.com/repos/Kitware/CMake/releases?per_page=100"
	winlibsReleasesURL        = "https://api.github.com/repos/brechtsanders/winlibs_mingw/releases?per_page=100"
)

var (
	gitForWindowsTagPattern = regexp.MustCompile(`^v?([0-9]+\.[0-9]+\.[0-9]+)\.windows\.([0-9]+)$`)
	cmakeTagPattern         = regexp.MustCompile(`^v?([0-9]+\.[0-9]+\.[0-9]+)$`)
)

func (p pythonProvider) ResolvePackage(ctx context.Context, version string, platform Platform, source MetadataSource) (PackageSpec, error) {
	arch, err := platform.pythonArchToken()
	if err != nil {
		return PackageSpec{}, err
	}

	var payload pythonWindowsRelease
	rawURL := fmt.Sprintf("https://www.python.org/ftp/python/%s/windows-%s.json", version, version)
	if err := source.FetchJSON(ctx, rawURL, remoteMetadataTTL, &payload); err != nil {
		return PackageSpec{}, err
	}

	expectedSuffixes := pythonEmbeddableArchiveSuffixes(arch)
	for _, item := range payload.Versions {
		if item.Company != "PythonEmbed" {
			continue
		}
		if !hasAnySuffix(strings.ToLower(item.URL), expectedSuffixes) {
			continue
		}
		return PackageSpec{
			Version: version,
			URL:     item.URL,
			Archive: ArchiveFormatZip,
			SHA256:  item.Hash.SHA256,
		}, nil
	}

	return PackageSpec{}, fmt.Errorf("python %s has no embeddable package for %s", version, arch)
}

func (pythonProvider) ListRemoteVersions(ctx context.Context, platform Platform, source MetadataSource) ([]RemoteVersion, error) {
	arch, err := platform.pythonArchToken()
	if err != nil {
		return nil, err
	}

	content, err := source.FetchText(ctx, pythonWindowsDownloadsURL, remoteMetadataTTL)
	if err != nil {
		return nil, err
	}

	pattern := regexp.MustCompile(fmt.Sprintf(`href="https://www\.python\.org/ftp/python/([0-9]+\.[0-9]+\.[0-9]+)/python-([0-9]+\.[0-9]+\.[0-9]+)-(?:embed|embeddable)-%s\.zip"`, regexp.QuoteMeta(arch)))
	matches := pattern.FindAllStringSubmatch(content, -1)
	seen := make(map[string]struct{}, len(matches))
	versions := make([]RemoteVersion, 0, len(matches))
	for _, match := range matches {
		if match[1] != match[2] {
			continue
		}
		version := match[1]
		if _, exists := seen[version]; exists {
			continue
		}
		seen[version] = struct{}{}
		versions = append(versions, RemoteVersion{
			Version: version,
			Stable:  true,
		})
	}

	sortRemoteVersions(versions)
	return versions, nil
}

func (p gitProvider) ResolvePackage(ctx context.Context, version string, platform Platform, source MetadataSource) (PackageSpec, error) {
	archiveName, err := gitArchiveName(version, platform)
	if err != nil {
		return PackageSpec{}, err
	}

	releases, err := fetchGitHubReleases(ctx, source, gitForWindowsReleasesURL)
	if err != nil {
		return PackageSpec{}, err
	}

	for _, release := range releases {
		releaseVersion, ok := gitReleaseVersion(release)
		if !ok || releaseVersion != version {
			continue
		}
		asset, ok := findGitHubReleaseAsset(release.Assets, archiveName)
		if !ok {
			return PackageSpec{}, fmt.Errorf("git version %s does not include %s", version, archiveName)
		}
		return packageSpecFromGitHubAsset(version, asset, ArchiveFormatZip), nil
	}

	return PackageSpec{}, fmt.Errorf("git version %s not found in official metadata", version)
}

func (gitProvider) ListRemoteVersions(ctx context.Context, platform Platform, source MetadataSource) ([]RemoteVersion, error) {
	releases, err := fetchGitHubReleases(ctx, source, gitForWindowsReleasesURL)
	if err != nil {
		return nil, err
	}

	versions := make([]RemoteVersion, 0, len(releases))
	seen := make(map[string]struct{}, len(releases))
	for _, release := range releases {
		if release.Draft || release.Prerelease {
			continue
		}

		version, ok := gitReleaseVersion(release)
		if !ok {
			continue
		}
		archiveName, err := gitArchiveName(version, platform)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[version]; exists || !releaseHasAsset(release.Assets, archiveName) {
			continue
		}

		seen[version] = struct{}{}
		versions = append(versions, RemoteVersion{
			Version: version,
			Stable:  true,
		})
	}

	sortRemoteVersions(versions)
	return versions, nil
}

func (p cmakeProvider) ResolvePackage(ctx context.Context, version string, platform Platform, source MetadataSource) (PackageSpec, error) {
	archiveName, err := cmakeArchiveName(version, platform)
	if err != nil {
		return PackageSpec{}, err
	}

	releases, err := fetchGitHubReleases(ctx, source, cmakeReleasesURL)
	if err != nil {
		return PackageSpec{}, err
	}

	for _, release := range releases {
		releaseVersion, ok := cmakeReleaseVersion(release)
		if !ok || releaseVersion != version {
			continue
		}
		asset, ok := findGitHubReleaseAsset(release.Assets, archiveName)
		if !ok {
			return PackageSpec{}, fmt.Errorf("cmake version %s does not include %s", version, archiveName)
		}
		return packageSpecFromGitHubAsset(version, asset, ArchiveFormatZip), nil
	}

	return PackageSpec{}, fmt.Errorf("cmake version %s not found in official metadata", version)
}

func (cmakeProvider) ListRemoteVersions(ctx context.Context, platform Platform, source MetadataSource) ([]RemoteVersion, error) {
	releases, err := fetchGitHubReleases(ctx, source, cmakeReleasesURL)
	if err != nil {
		return nil, err
	}

	versions := make([]RemoteVersion, 0, len(releases))
	seen := make(map[string]struct{}, len(releases))
	for _, release := range releases {
		if release.Draft || release.Prerelease {
			continue
		}

		version, ok := cmakeReleaseVersion(release)
		if !ok {
			continue
		}
		archiveName, err := cmakeArchiveName(version, platform)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[version]; exists || !releaseHasAsset(release.Assets, archiveName) {
			continue
		}

		seen[version] = struct{}{}
		versions = append(versions, RemoteVersion{
			Version: version,
			Stable:  true,
		})
	}

	sortRemoteVersions(versions)
	return versions, nil
}

func (p nodejsProvider) ResolvePackage(ctx context.Context, version string, platform Platform, source MetadataSource) (PackageSpec, error) {
	fileName, err := nodeArchiveName(version, platform)
	if err != nil {
		return PackageSpec{}, err
	}

	shasumsURL := fmt.Sprintf("https://nodejs.org/dist/v%s/SHASUMS256.txt", version)
	content, err := source.FetchText(ctx, shasumsURL, remoteMetadataTTL)
	if err != nil {
		return PackageSpec{}, err
	}

	checksum, err := checksumFromSHASUMS(content, fileName)
	if err != nil {
		return PackageSpec{}, err
	}

	return PackageSpec{
		Version: version,
		URL:     fmt.Sprintf("https://nodejs.org/dist/v%s/%s", version, fileName),
		Archive: ArchiveFormatZip,
		SHA256:  checksum,
	}, nil
}

func (nodejsProvider) ListRemoteVersions(ctx context.Context, platform Platform, source MetadataSource) ([]RemoteVersion, error) {
	var payload []nodeRemoteVersion
	if err := source.FetchJSON(ctx, "https://nodejs.org/dist/index.json", remoteMetadataTTL, &payload); err != nil {
		return nil, err
	}

	requiredFile, err := nodeRemoteFileToken(platform)
	if err != nil {
		return nil, err
	}

	versions := make([]RemoteVersion, 0, len(payload))
	for _, item := range payload {
		if !containsString(item.Files, requiredFile) {
			continue
		}
		versions = append(versions, RemoteVersion{
			Version: strings.TrimPrefix(item.Version, "v"),
			Stable:  true,
			LTS:     item.LTSString() != "",
		})
	}

	sortRemoteVersions(versions)
	return versions, nil
}

func (p javaProvider) ResolvePackage(ctx context.Context, version string, platform Platform, source MetadataSource) (PackageSpec, error) {
	majorParts := numericParts(version)
	if len(majorParts) == 0 {
		return PackageSpec{}, fmt.Errorf("invalid java version %q", version)
	}

	releases, err := fetchJavaMajorVersions(ctx, source, majorParts[0], platform)
	if err != nil {
		return PackageSpec{}, err
	}

	for _, release := range releases {
		if release.Version != version {
			continue
		}
		pkg := release.Package
		pkg.Version = version
		return pkg, nil
	}

	return PackageSpec{}, fmt.Errorf("java version %s not found in official metadata", version)
}

func (javaProvider) ListRemoteVersions(ctx context.Context, platform Platform, source MetadataSource) ([]RemoteVersion, error) {
	var info adoptiumAvailableReleases
	if err := source.FetchJSON(ctx, "https://api.adoptium.net/v3/info/available_releases", remoteMetadataTTL, &info); err != nil {
		return nil, err
	}

	versions := make([]RemoteVersion, 0)
	ltsMajors := make(map[int]struct{}, len(info.AvailableLTSReleases))
	for _, release := range info.AvailableLTSReleases {
		ltsMajors[release] = struct{}{}
	}

	majors := append([]int(nil), info.AvailableReleases...)
	sortIntsDescending(majors)
	for _, major := range majors {
		releases, err := fetchJavaMajorVersions(ctx, source, major, platform)
		if err != nil {
			return nil, err
		}
		for _, release := range releases {
			_, isLTS := ltsMajors[major]
			versions = append(versions, RemoteVersion{
				Version: release.Version,
				Stable:  true,
				LTS:     isLTS,
			})
		}
	}

	sortRemoteVersions(versions)
	return versions, nil
}

func (p goProvider) ResolvePackage(ctx context.Context, version string, platform Platform, source MetadataSource) (PackageSpec, error) {
	payload, err := fetchGoVersions(ctx, source)
	if err != nil {
		return PackageSpec{}, err
	}

	for _, release := range payload {
		if strings.TrimPrefix(release.Version, "go") != version {
			continue
		}
		for _, file := range release.Files {
			if file.OS == "windows" && file.Arch == platform.Arch && file.Kind == "archive" {
				return PackageSpec{
					Version: version,
					URL:     "https://go.dev/dl/" + file.Filename,
					Archive: ArchiveFormatZip,
					SHA256:  file.SHA256,
					Size:    file.Size,
				}, nil
			}
		}
	}

	return PackageSpec{}, fmt.Errorf("go version %s not found in official metadata", version)
}

func (goProvider) ListRemoteVersions(ctx context.Context, platform Platform, source MetadataSource) ([]RemoteVersion, error) {
	payload, err := fetchGoVersions(ctx, source)
	if err != nil {
		return nil, err
	}

	versions := make([]RemoteVersion, 0, len(payload))
	for _, release := range payload {
		if !goReleaseSupportsPlatform(release, platform) {
			continue
		}
		versions = append(versions, RemoteVersion{
			Version: strings.TrimPrefix(release.Version, "go"),
			Stable:  release.Stable,
		})
	}

	sortRemoteVersions(versions)
	return versions, nil
}

func (p mingwProvider) ResolvePackage(ctx context.Context, version string, platform Platform, source MetadataSource) (PackageSpec, error) {
	archivePrefix, err := mingwArchivePrefix(platform)
	if err != nil {
		return PackageSpec{}, err
	}

	releases, err := fetchGitHubReleases(ctx, source, winlibsReleasesURL)
	if err != nil {
		return PackageSpec{}, err
	}

	for _, release := range releases {
		if !strings.EqualFold(release.TagName, version) {
			continue
		}
		asset, ok := findWinlibsArchiveAsset(release.Assets, archivePrefix)
		if !ok {
			return PackageSpec{}, fmt.Errorf("mingw version %s does not include a supported zip archive", version)
		}
		return packageSpecFromGitHubAsset(version, asset, ArchiveFormatZip), nil
	}

	return PackageSpec{}, fmt.Errorf("mingw version %s not found in official metadata", version)
}

func (mingwProvider) ListRemoteVersions(ctx context.Context, platform Platform, source MetadataSource) ([]RemoteVersion, error) {
	archivePrefix, err := mingwArchivePrefix(platform)
	if err != nil {
		return nil, err
	}

	releases, err := fetchGitHubReleases(ctx, source, winlibsReleasesURL)
	if err != nil {
		return nil, err
	}

	versions := make([]RemoteVersion, 0, len(releases))
	seen := make(map[string]struct{}, len(releases))
	for _, release := range releases {
		if release.Draft {
			continue
		}
		version, stable, ok := mingwReleaseVersion(release)
		if !ok {
			continue
		}
		if _, exists := seen[version]; exists || !releaseHasWinlibsArchive(release.Assets, archivePrefix) {
			continue
		}

		seen[version] = struct{}{}
		versions = append(versions, RemoteVersion{
			Version: version,
			Stable:  stable,
		})
	}

	sortRemoteVersions(versions)
	return versions, nil
}

type pythonWindowsRelease struct {
	Versions []struct {
		Company string `json:"company"`
		Tag     string `json:"tag"`
		URL     string `json:"url"`
		Hash    struct {
			SHA256 string `json:"sha256"`
		} `json:"hash"`
	} `json:"versions"`
}

type nodeRemoteVersion struct {
	Version string   `json:"version"`
	Files   []string `json:"files"`
	LTS     any      `json:"lts"`
}

func (n nodeRemoteVersion) LTSString() string {
	switch value := n.LTS.(type) {
	case string:
		return value
	case bool:
		if value {
			return "true"
		}
		return ""
	default:
		return ""
	}
}

type githubRelease struct {
	TagName    string               `json:"tag_name"`
	Name       string               `json:"name"`
	Draft      bool                 `json:"draft"`
	Prerelease bool                 `json:"prerelease"`
	Assets     []githubReleaseAsset `json:"assets"`
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
	Size               int64  `json:"size"`
}

type adoptiumAvailableReleases struct {
	AvailableReleases    []int `json:"available_releases"`
	AvailableLTSReleases []int `json:"available_lts_releases"`
}

type adoptiumRelease struct {
	ReleaseName string `json:"release_name"`
	Binaries    []struct {
		Package struct {
			Checksum string `json:"checksum"`
			Link     string `json:"link"`
			Size     int64  `json:"size"`
		} `json:"package"`
	} `json:"binaries"`
}

type javaRemoteRelease struct {
	Version string
	Package PackageSpec
}

type goRelease struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
	Files   []struct {
		Filename string `json:"filename"`
		OS       string `json:"os"`
		Arch     string `json:"arch"`
		Kind     string `json:"kind"`
		SHA256   string `json:"sha256"`
		Size     int64  `json:"size"`
	} `json:"files"`
}

func fetchGoVersions(ctx context.Context, source MetadataSource) ([]goRelease, error) {
	var payload []goRelease
	if err := source.FetchJSON(ctx, "https://go.dev/dl/?mode=json&include=all", remoteMetadataTTL, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func fetchJavaMajorVersions(ctx context.Context, source MetadataSource, major int, platform Platform) ([]javaRemoteRelease, error) {
	arch, err := platform.javaArchToken()
	if err != nil {
		return nil, err
	}

	query := url.Values{}
	query.Set("architecture", arch)
	query.Set("heap_size", "normal")
	query.Set("image_type", "jdk")
	query.Set("jvm_impl", "hotspot")
	query.Set("os", platform.OS)
	query.Set("vendor", "eclipse")

	rawURL := fmt.Sprintf("https://api.adoptium.net/v3/assets/feature_releases/%d/ga?%s", major, query.Encode())
	var payload []adoptiumRelease
	if err := source.FetchJSON(ctx, rawURL, remoteMetadataTTL, &payload); err != nil {
		return nil, err
	}

	releases := make([]javaRemoteRelease, 0, len(payload))
	for _, item := range payload {
		if len(item.Binaries) == 0 {
			continue
		}
		version := strings.TrimPrefix(item.ReleaseName, "jdk-")
		releases = append(releases, javaRemoteRelease{
			Version: version,
			Package: PackageSpec{
				URL:     item.Binaries[0].Package.Link,
				Archive: ArchiveFormatZip,
				SHA256:  item.Binaries[0].Package.Checksum,
				Size:    item.Binaries[0].Package.Size,
			},
		})
	}

	return releases, nil
}

func fetchGitHubReleases(ctx context.Context, source MetadataSource, rawURL string) ([]githubRelease, error) {
	var payload []githubRelease
	if err := source.FetchJSON(ctx, rawURL, remoteMetadataTTL, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func gitReleaseVersion(release githubRelease) (string, bool) {
	match := gitForWindowsTagPattern.FindStringSubmatch(strings.TrimSpace(release.TagName))
	if len(match) != 3 {
		return "", false
	}
	return match[1] + "." + match[2], true
}

func cmakeReleaseVersion(release githubRelease) (string, bool) {
	match := cmakeTagPattern.FindStringSubmatch(strings.TrimSpace(release.TagName))
	if len(match) != 2 {
		return "", false
	}
	return match[1], true
}

func mingwReleaseVersion(release githubRelease) (string, bool, bool) {
	version := strings.ToLower(strings.TrimSpace(release.TagName))
	if version == "" {
		return "", false, false
	}
	if !strings.Contains(version, "posix") || !strings.Contains(version, "-ucrt-") {
		return "", false, false
	}
	stable := !release.Prerelease && !strings.Contains(version, "snapshot")
	return version, stable, true
}

func nodeRemoteFileToken(platform Platform) (string, error) {
	arch, err := platform.nodeArchToken()
	if err != nil {
		return "", err
	}
	return "win-" + arch + "-zip", nil
}

func nodeArchiveName(version string, platform Platform) (string, error) {
	arch, err := platform.nodeArchToken()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("node-v%s-win-%s.zip", version, arch), nil
}

func gitArchiveName(version string, platform Platform) (string, error) {
	arch, err := platform.gitArchToken()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("MinGit-%s-%s.zip", version, arch), nil
}

func cmakeArchiveName(version string, platform Platform) (string, error) {
	arch, err := platform.cmakeArchToken()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("cmake-%s-windows-%s.zip", version, arch), nil
}

func mingwArchivePrefix(platform Platform) (string, error) {
	assetToken, err := platform.mingwWinlibsAssetToken()
	if err != nil {
		return "", err
	}
	return "winlibs-" + assetToken + "-gcc-", nil
}

func pythonEmbeddableArchiveSuffixes(arch string) []string {
	switch arch {
	case "arm64":
		return []string{"-embed-arm64.zip", "-embeddable-arm64.zip"}
	case "amd64":
		return []string{"-embed-amd64.zip", "-embeddable-amd64.zip"}
	case "x86":
		return []string{"-embed-win32.zip", "-embeddable-win32.zip"}
	}
	return nil
}

func checksumFromSHASUMS(content string, fileName string) (string, error) {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[1] == fileName {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksum not found for %s", fileName)
}

func goReleaseSupportsPlatform(release goRelease, platform Platform) bool {
	for _, file := range release.Files {
		if file.OS == platform.OS && file.Arch == platform.Arch && file.Kind == "archive" {
			return true
		}
	}
	return false
}

func packageSpecFromGitHubAsset(version string, asset githubReleaseAsset, archive ArchiveFormat) PackageSpec {
	return PackageSpec{
		Version: version,
		URL:     asset.BrowserDownloadURL,
		Archive: archive,
		SHA256:  normalizeGitHubAssetDigest(asset.Digest),
		Size:    asset.Size,
	}
}

func normalizeGitHubAssetDigest(digest string) string {
	return strings.TrimPrefix(strings.TrimSpace(digest), "sha256:")
}

func findGitHubReleaseAsset(assets []githubReleaseAsset, targetName string) (githubReleaseAsset, bool) {
	for _, asset := range assets {
		if strings.EqualFold(asset.Name, targetName) {
			return asset, true
		}
	}
	return githubReleaseAsset{}, false
}

func releaseHasAsset(assets []githubReleaseAsset, targetName string) bool {
	_, ok := findGitHubReleaseAsset(assets, targetName)
	return ok
}

func findWinlibsArchiveAsset(assets []githubReleaseAsset, archivePrefix string) (githubReleaseAsset, bool) {
	for _, asset := range assets {
		name := strings.ToLower(strings.TrimSpace(asset.Name))
		if strings.HasPrefix(name, archivePrefix) && strings.HasSuffix(name, ".zip") {
			return asset, true
		}
	}
	return githubReleaseAsset{}, false
}

func releaseHasWinlibsArchive(assets []githubReleaseAsset, archivePrefix string) bool {
	_, ok := findWinlibsArchiveAsset(assets, archivePrefix)
	return ok
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func hasAnySuffix(value string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}
	return false
}

func sortIntsDescending(values []int) {
	sort.SliceStable(values, func(i int, j int) bool {
		return values[i] > values[j]
	})
}
