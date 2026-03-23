package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"toolmesh/internal/runtimes"
)

type serviceFactory func() (runtimes.Service, error)

const defaultAppVersion = "dev"

type App struct {
	stdout     io.Writer
	stderr     io.Writer
	newService serviceFactory
	version    string
}

func NewApp(stdout io.Writer, stderr io.Writer) *App {
	return newApp(stdout, stderr, defaultAppVersion)
}

func NewAppWithVersion(stdout io.Writer, stderr io.Writer, version string) *App {
	return newApp(stdout, stderr, version)
}

func newApp(stdout io.Writer, stderr io.Writer, version string) *App {
	app := &App{
		stdout:  stdout,
		stderr:  stderr,
		version: normalizeAppVersion(version),
	}

	app.newService = func() (runtimes.Service, error) {
		manager, err := runtimes.DefaultManager()
		if err != nil {
			return nil, err
		}
		manager.SetDownloadProgressObserver(newInstallProgressReporter(stderr))
		return manager, nil
	}

	return app
}

func (a *App) Run(ctx context.Context, args []string) int {
	if len(args) == 0 || isHelp(args[0]) {
		a.printRootUsage()
		return 0
	}

	switch {
	case args[0] == "version":
		if len(args) != 1 {
			a.printError("usage: toolmesh version")
			return 1
		}
		fmt.Fprintf(a.stdout, "toolmesh %s\n", a.version)
		return 0
	case isPackageInstallerCommand(args[0]):
		return a.runPackageInstaller(ctx, args[0], args[1:])
	case isRuntimeVenvCommand(args):
		return a.runRuntime(ctx, rewriteRuntimeVenvArgs(args))
	case isRuntimeCommand(args[0]):
		return a.runRuntime(ctx, args)
	default:
		a.printError("unknown command: %s", args[0])
		a.printRootUsage()
		return 1
	}
}

func (a *App) runPackageInstaller(ctx context.Context, installerName string, args []string) int {
	if len(args) == 0 || isHelp(args[0]) {
		a.printPackageInstallerUsage(installerName)
		return 0
	}
	if args[0] != "install" || len(args) < 2 {
		a.printError("usage: toolmesh %s install <%s-args...>", installerName, installerName)
		return 1
	}

	service, err := a.newService()
	if err != nil {
		a.printError("failed to initialize runtime manager: %v", err)
		return 1
	}

	if err := service.InstallPackages(ctx, installerName, runtimes.PackageInstallOptions{
		Args:   args[1:],
		Stdin:  os.Stdin,
		Stdout: a.stdout,
		Stderr: a.stderr,
	}); err != nil {
		a.printError("%v", err)
		return 1
	}

	return 0
}

func (a *App) runRuntime(ctx context.Context, args []string) int {
	if len(args) == 0 || isHelp(args[0]) {
		a.printRuntimeUsage()
		return 0
	}

	service, err := a.newService()
	if err != nil {
		a.printError("failed to initialize runtime manager: %v", err)
		return 1
	}

	switch args[0] {
	case "install":
		if len(args) != 3 {
			a.printError("usage: toolmesh install <runtime> <version>")
			return 1
		}

		installed, err := service.Install(ctx, args[1], args[2])
		if err != nil {
			a.printError("%v", err)
			return 1
		}

		fmt.Fprintf(a.stdout, "installed %s %s at %s\n", installed.Runtime, installed.Version, installed.Home)
		return 0
	case "list":
		runtimeName := ""
		if len(args) > 2 {
			a.printError("usage: toolmesh list [runtime]")
			return 1
		}
		if len(args) == 2 {
			runtimeName = args[1]
		}

		items, err := service.List(runtimeName)
		if err != nil {
			a.printError("%v", err)
			return 1
		}
		if len(items) == 0 {
			fmt.Fprintln(a.stdout, "no installed runtimes")
			return 0
		}

		for _, item := range items {
			marker := " "
			if item.Active {
				marker = "*"
			}
			fmt.Fprintf(a.stdout, "%s %s %s %s\n", marker, item.Runtime, item.Version, item.Home)
		}
		return 0
	case "list-remote":
		if len(args) < 2 || len(args) > 3 {
			a.printError("usage: toolmesh list-remote <runtime> [selector]")
			return 1
		}

		selector := ""
		if len(args) == 3 {
			selector = args[2]
		}

		versions, err := service.ListRemote(ctx, args[1], selector)
		if err != nil {
			a.printError("%v", err)
			return 1
		}

		for _, version := range versions {
			fmt.Fprintf(a.stdout, "%s%s\n", version.Version, formatRemoteFlags(version))
		}
		return 0
	case "latest":
		if len(args) < 2 || len(args) > 3 {
			a.printError("usage: toolmesh latest <runtime> [selector]")
			return 1
		}

		selector := ""
		if len(args) == 3 {
			selector = args[2]
		}

		version, err := service.Latest(ctx, args[1], selector)
		if err != nil {
			a.printError("%v", err)
			return 1
		}

		fmt.Fprintf(a.stdout, "%s %s%s\n", args[1], version.Version, formatRemoteFlags(version))
		return 0
	case "use":
		projectScope, runtimeName, version, ok := parseUseArgs(args[1:])
		if !ok {
			a.printError("usage: toolmesh use [--project] <runtime> <version>")
			return 1
		}

		var current runtimes.InstalledRuntime
		if projectScope {
			current, err = service.UseProject(ctx, runtimeName, version)
		} else {
			current, err = service.Use(runtimeName, version)
		}
		if err != nil {
			a.printError("%v", err)
			return 1
		}

		fmt.Fprintf(a.stdout, "using %s %s (%s)\n", current.Runtime, current.Version, current.Executable)
		if !projectScope {
			fmt.Fprintln(a.stdout, "open a new shell to use runtime commands directly, or run them with toolmesh exec in the current shell")
		}
		return 0
	case "current":
		if len(args) > 2 {
			a.printError("usage: toolmesh current [runtime]")
			return 1
		}

		if len(args) == 2 {
			current, err := service.Current(args[1])
			if err != nil {
				a.printError("%v", err)
				return 1
			}

			fmt.Fprintf(a.stdout, "%s %s %s\n", current.Runtime, current.Version, current.Executable)
			return 0
		}

		items, err := service.CurrentAll()
		if err != nil {
			a.printError("%v", err)
			return 1
		}
		if len(items) == 0 {
			fmt.Fprintln(a.stdout, "no active runtimes")
			return 0
		}

		for _, item := range items {
			fmt.Fprintf(a.stdout, "%s %s %s\n", item.Runtime, item.Version, item.Executable)
		}
		return 0
	case "exec":
		if len(args) < 2 {
			a.printError("usage: toolmesh exec <command> [args...]")
			return 1
		}
		return a.runExec(ctx, service, args[1:])
	case "venv":
		runtimeName, targetPath, ok := parseVenvArgs(args[1:])
		if !ok {
			a.printError("usage: toolmesh <runtime> venv [path] (or: toolmesh venv <runtime> [path])")
			return 1
		}

		created, err := service.CreateVenv(ctx, runtimeName, targetPath)
		if err != nil {
			a.printError("%v", err)
			return 1
		}

		fmt.Fprintf(a.stdout, "created venv at %s using %s %s\n", created.Path, created.Runtime, created.Version)
		return 0
	case "remove", "uninstall":
		if len(args) != 3 {
			a.printError("usage: toolmesh %s <runtime> <version>", args[0])
			return 1
		}

		if err := service.Remove(args[1], args[2]); err != nil {
			a.printError("%v", err)
			return 1
		}

		fmt.Fprintf(a.stdout, "removed %s %s\n", args[1], args[2])
		return 0
	default:
		a.printError("unknown runtime subcommand: %s", args[0])
		a.printRuntimeUsage()
		return 1
	}
}

func (a *App) runExec(ctx context.Context, service runtimes.Service, commandArgs []string) int {
	pathEntries, err := service.ExecPath()
	if err != nil {
		a.printError("%v", err)
		return 1
	}

	command := exec.CommandContext(ctx, commandArgs[0], commandArgs[1:]...)
	command.Stdout = a.stdout
	command.Stderr = a.stderr
	command.Stdin = os.Stdin
	command.Env = prependPathEnv(os.Environ(), pathEntries)

	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return exitError.ExitCode()
		}
		a.printError("failed to execute %s: %v", commandArgs[0], err)
		return 1
	}

	return 0
}

func parseUseArgs(args []string) (bool, string, string, bool) {
	projectScope := false
	if len(args) > 0 && args[0] == "--project" {
		projectScope = true
		args = args[1:]
	}
	if len(args) != 2 {
		return false, "", "", false
	}
	return projectScope, args[0], args[1], true
}

func parseVenvArgs(args []string) (string, string, bool) {
	if len(args) < 1 || len(args) > 2 {
		return "", "", false
	}
	if isHelp(args[0]) {
		return "", "", false
	}

	targetPath := ""
	if len(args) == 2 {
		if isHelp(args[1]) {
			return "", "", false
		}
		targetPath = args[1]
	}
	return args[0], targetPath, true
}

func prependPathEnv(environment []string, pathEntries []string) []string {
	if len(pathEntries) == 0 {
		return environment
	}

	pathKey := "PATH"
	currentValue := ""
	index := -1
	for i, item := range environment {
		key, value, found := strings.Cut(item, "=")
		if !found {
			continue
		}
		if strings.EqualFold(key, "PATH") {
			pathKey = key
			currentValue = value
			index = i
			break
		}
	}

	merged := strings.Join(pathEntries, string(os.PathListSeparator))
	if currentValue != "" {
		merged += string(os.PathListSeparator) + currentValue
	}

	entry := pathKey + "=" + merged
	if index >= 0 {
		environment[index] = entry
		return environment
	}

	return append(environment, entry)
}

func formatRemoteFlags(version runtimes.RemoteVersion) string {
	flags := make([]string, 0, 2)
	if version.Stable {
		flags = append(flags, "stable")
	}
	if version.LTS {
		flags = append(flags, "lts")
	}
	if len(flags) == 0 {
		return ""
	}
	return " [" + strings.Join(flags, ", ") + "]"
}

func (a *App) printRootUsage() {
	a.printCommandUsage()
}

func (a *App) printRuntimeUsage() {
	a.printCommandUsage()
}

func (a *App) printCommandUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  toolmesh version")
	fmt.Fprintln(a.stdout, "  toolmesh pip install <pip-args...>")
	fmt.Fprintln(a.stdout, "  toolmesh npm install <npm-args...>")
	fmt.Fprintln(a.stdout, "  toolmesh install <runtime> <version>")
	fmt.Fprintln(a.stdout, "  toolmesh list [runtime]")
	fmt.Fprintln(a.stdout, "  toolmesh list-remote <runtime> [selector]")
	fmt.Fprintln(a.stdout, "  toolmesh latest <runtime> [selector]")
	fmt.Fprintln(a.stdout, "  toolmesh use [--project] <runtime> <version>")
	fmt.Fprintln(a.stdout, "  toolmesh current [runtime]")
	fmt.Fprintln(a.stdout, "  toolmesh <runtime> venv [path]")
	fmt.Fprintln(a.stdout, "  toolmesh venv <runtime> [path]")
	fmt.Fprintln(a.stdout, "  toolmesh exec <command> [args...]")
	fmt.Fprintln(a.stdout, "  toolmesh remove <runtime> <version>")
	fmt.Fprintln(a.stdout, "  toolmesh uninstall <runtime> <version>")
	fmt.Fprintln(a.stdout, "Supported runtimes: python/py, nodejs/node, java, go/golang, git/gitforwindows, cmake, mingw/gcc")
}

func (a *App) printPackageInstallerUsage(installerName string) {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintf(a.stdout, "  toolmesh %s install <%s-args...>\n", installerName, installerName)
}

func (a *App) printError(format string, args ...any) {
	fmt.Fprintf(a.stderr, "error: "+format+"\n", args...)
}

func isHelp(value string) bool {
	switch strings.ToLower(value) {
	case "help", "-h", "--help":
		return true
	default:
		return false
	}
}

func isRuntimeCommand(value string) bool {
	switch value {
	case "install", "list", "list-remote", "latest", "use", "current", "venv", "exec", "remove", "uninstall":
		return true
	default:
		return false
	}
}

func isPackageInstallerCommand(value string) bool {
	switch value {
	case "pip", "npm":
		return true
	default:
		return false
	}
}

func isRuntimeVenvCommand(args []string) bool {
	return len(args) >= 2 && args[1] == "venv" && args[0] != "version" && !isRuntimeCommand(args[0])
}

func rewriteRuntimeVenvArgs(args []string) []string {
	rewritten := []string{"venv", args[0]}
	return append(rewritten, args[2:]...)
}

func normalizeAppVersion(version string) string {
	trimmed := strings.TrimSpace(version)
	if trimmed == "" {
		return defaultAppVersion
	}
	return trimmed
}
