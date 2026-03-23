package runtimes

import (
	"fmt"
	goruntime "runtime"
)

type Platform struct {
	OS   string
	Arch string
}

func CurrentPlatform() Platform {
	return Platform{
		OS:   goruntime.GOOS,
		Arch: goruntime.GOARCH,
	}
}

func (p Platform) pythonArchToken() (string, error) {
	if p.OS != "windows" {
		return "", fmt.Errorf("python provider currently supports windows only")
	}

	switch p.Arch {
	case "amd64":
		return "amd64", nil
	case "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("python provider does not support arch %q", p.Arch)
	}
}

func (p Platform) gitArchToken() (string, error) {
	if p.OS != "windows" {
		return "", fmt.Errorf("git provider currently supports windows only")
	}

	switch p.Arch {
	case "amd64":
		return "64-bit", nil
	case "arm64":
		return "arm64", nil
	case "386":
		return "32-bit", nil
	default:
		return "", fmt.Errorf("git provider does not support arch %q", p.Arch)
	}
}

func (p Platform) cmakeArchToken() (string, error) {
	if p.OS != "windows" {
		return "", fmt.Errorf("cmake provider currently supports windows only")
	}

	switch p.Arch {
	case "amd64":
		return "x86_64", nil
	case "arm64":
		return "arm64", nil
	case "386":
		return "i386", nil
	default:
		return "", fmt.Errorf("cmake provider does not support arch %q", p.Arch)
	}
}

func (p Platform) nodeArchToken() (string, error) {
	if p.OS != "windows" {
		return "", fmt.Errorf("nodejs provider currently supports windows only")
	}

	switch p.Arch {
	case "amd64":
		return "x64", nil
	case "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("nodejs provider does not support arch %q", p.Arch)
	}
}

func (p Platform) mingwWinlibsAssetToken() (string, error) {
	if p.OS != "windows" {
		return "", fmt.Errorf("mingw provider currently supports windows only")
	}

	switch p.Arch {
	case "amd64":
		return "x86_64-posix-seh", nil
	case "386":
		return "i686-posix-dwarf", nil
	default:
		return "", fmt.Errorf("mingw provider does not support arch %q", p.Arch)
	}
}

func (p Platform) goArchToken() (string, error) {
	if p.OS != "windows" {
		return "", fmt.Errorf("go provider currently supports windows only")
	}

	switch p.Arch {
	case "amd64":
		return "amd64", nil
	case "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("go provider does not support arch %q", p.Arch)
	}
}

func (p Platform) javaArchToken() (string, error) {
	if p.OS != "windows" {
		return "", fmt.Errorf("java provider currently supports windows only")
	}

	switch p.Arch {
	case "amd64":
		return "x64", nil
	case "arm64":
		return "aarch64", nil
	default:
		return "", fmt.Errorf("java provider does not support arch %q", p.Arch)
	}
}
