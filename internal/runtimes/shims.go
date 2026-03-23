package runtimes

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
)

const shimLauncherName = "toolmesh.exe"

func (m *Manager) syncGlobalShims() error {
	if err := ensurePaths(m.paths); err != nil {
		return err
	}
	if m.paths.ShimsDir == "" {
		return nil
	}

	if err := os.RemoveAll(m.paths.ShimsDir); err != nil {
		return err
	}
	if err := os.MkdirAll(m.paths.ShimsDir, 0o755); err != nil {
		return err
	}

	executablePath, err := m.executablePath()
	if err != nil {
		return err
	}
	if err := copyFile(executablePath, filepath.Join(m.paths.ShimsDir, shimLauncherName)); err != nil {
		return err
	}

	state, err := loadState(m.paths.StatePath)
	if err != nil {
		return err
	}

	runtimeNames := make([]string, 0, len(state.Active))
	for runtimeName := range state.Active {
		runtimeNames = append(runtimeNames, runtimeName)
	}
	sort.Strings(runtimeNames)

	written := make(map[string]struct{})
	stateChanged := false
	for _, runtimeName := range runtimeNames {
		record, installDir, err := m.loadInstallation(runtimeName, state.Active[runtimeName])
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				delete(state.Active, runtimeName)
				stateChanged = true
				continue
			}
			return err
		}
		item := installationFromRecord(record, installDir, true)
		if err := m.writeCommandShims(filepath.Dir(item.Executable), written); err != nil {
			return err
		}
	}

	if stateChanged {
		if err := saveState(m.paths.StatePath, state); err != nil {
			return err
		}
	}

	if err := m.ensureUserPathEntry(m.paths.ShimsDir); err != nil {
		return err
	}
	return nil
}

func (m *Manager) writeCommandShims(commandDir string, written map[string]struct{}) error {
	entries, err := os.ReadDir(commandDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		commandName, ok := shimCommandName(entry.Name())
		if !ok {
			continue
		}

		key := strings.ToLower(commandName)
		if _, exists := written[key]; exists {
			continue
		}
		written[key] = struct{}{}

		shimPath := filepath.Join(m.paths.ShimsDir, commandName+".cmd")
		if err := os.WriteFile(shimPath, []byte(buildCommandShim(commandName)), 0o755); err != nil {
			return err
		}
	}

	return nil
}

func shimCommandName(fileName string) (string, bool) {
	extension := strings.ToLower(filepath.Ext(fileName))
	switch extension {
	case ".exe", ".cmd", ".bat", ".ps1":
		commandName := strings.TrimSuffix(fileName, filepath.Ext(fileName))
		if commandName == "" || strings.EqualFold(commandName, "toolmesh") {
			return "", false
		}
		return commandName, true
	default:
		return "", false
	}
}

func buildCommandShim(commandName string) string {
	return "@echo off\r\n" +
		"\"%~dp0" + shimLauncherName + "\" exec \"" + commandName + "\" %*\r\n" +
		"exit /b %ERRORLEVEL%\r\n"
}

func (m *Manager) executablePath() (string, error) {
	if m.executablePathFunc == nil {
		return os.Executable()
	}
	return m.executablePathFunc()
}

func (m *Manager) ensureUserPathEntry(path string) error {
	if m.ensureUserPathEntryFunc == nil {
		return ensureUserPathContains(path)
	}
	return m.ensureUserPathEntryFunc(path)
}

func ensureUserPathContains(path string) error {
	if goruntime.GOOS != "windows" {
		return nil
	}

	script := "$ErrorActionPreference='Stop';" +
		"$target=$env:TOOLMESH_SHIMS_DIR;" +
		"$current=[Environment]::GetEnvironmentVariable('Path','User');" +
		"$parts=@();" +
		"if($current){$parts+=$current -split ';' | Where-Object { $_ -and $_.Trim() -ne '' }};" +
		"foreach($item in $parts){if($item -ieq $target){exit 0}};" +
		"$newPath=if($current -and $current.Trim() -ne ''){\"$target;$current\"}else{$target};" +
		"[Environment]::SetEnvironmentVariable('Path',$newPath,'User')"

	command := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	command.Env = append(os.Environ(), "TOOLMESH_SHIMS_DIR="+path)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to add %s to user PATH: %v (%s)", path, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func copyFile(sourcePath string, destinationPath string) error {
	if sameFilePath(sourcePath, destinationPath) {
		return nil
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(destinationPath)
	if err != nil {
		return err
	}

	if _, err := destination.ReadFrom(source); err != nil {
		destination.Close()
		_ = os.Remove(destinationPath)
		return err
	}
	if err := destination.Close(); err != nil {
		_ = os.Remove(destinationPath)
		return err
	}
	return nil
}

func sameFilePath(left string, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
