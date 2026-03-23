package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"toolmesh/internal/runtimes"
)

func TestRunInstallCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	app := &App{
		stdout: &stdout,
		stderr: &stderr,
		newService: func() (runtimes.Service, error) {
			return stubService{
				installFn: func(_ context.Context, runtimeName string, version string) (runtimes.InstalledRuntime, error) {
					if runtimeName != "python" || version != "3.12.2" {
						t.Fatalf("unexpected install arguments: %s %s", runtimeName, version)
					}
					return runtimes.InstalledRuntime{
						Runtime: "python",
						Version: "3.12.2",
						Home:    `C:\toolmesh\python\3.12.2`,
					}, nil
				},
			}, nil
		},
	}

	exitCode := app.Run(context.Background(), []string{"install", "python", "3.12.2"})
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	if got := stderr.String(); got != "" {
		t.Fatalf("expected empty stderr, got %q", got)
	}

	if !strings.Contains(stdout.String(), "installed python 3.12.2") {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestRunCurrentCommandWithoutRuntime(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	app := &App{
		stdout: &stdout,
		stderr: &stderr,
		newService: func() (runtimes.Service, error) {
			return stubService{
				currentAllFn: func() ([]runtimes.InstalledRuntime, error) {
					return []runtimes.InstalledRuntime{
						{
							Runtime:    "nodejs",
							Version:    "20.12.2",
							Executable: `C:\toolmesh\nodejs\20.12.2\node.exe`,
						},
					}, nil
				},
			}, nil
		},
	}

	exitCode := app.Run(context.Background(), []string{"current"})
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	if got := stderr.String(); got != "" {
		t.Fatalf("expected empty stderr, got %q", got)
	}

	if !strings.Contains(stdout.String(), "nodejs 20.12.2") {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestRunUseProjectCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	app := &App{
		stdout: &stdout,
		stderr: &stderr,
		newService: func() (runtimes.Service, error) {
			return stubService{
				useProjectFn: func(_ context.Context, runtimeName string, version string) (runtimes.InstalledRuntime, error) {
					if runtimeName != "node" || version != "20" {
						t.Fatalf("unexpected use --project arguments: %s %s", runtimeName, version)
					}
					return runtimes.InstalledRuntime{
						Runtime:    "nodejs",
						Version:    "20.12.2",
						Executable: `C:\toolmesh\nodejs\20.12.2\node.exe`,
					}, nil
				},
			}, nil
		},
	}

	exitCode := app.Run(context.Background(), []string{"use", "--project", "node", "20"})
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout.String(), "using nodejs 20.12.2") {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestRunLatestCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	app := &App{
		stdout: &stdout,
		stderr: &stderr,
		newService: func() (runtimes.Service, error) {
			return stubService{
				latestFn: func(_ context.Context, runtimeName string, selector string) (runtimes.RemoteVersion, error) {
					if runtimeName != "node" || selector != "lts" {
						t.Fatalf("unexpected latest arguments: %s %s", runtimeName, selector)
					}
					return runtimes.RemoteVersion{Version: "20.12.2", Stable: true, LTS: true}, nil
				},
			}, nil
		},
	}

	exitCode := app.Run(context.Background(), []string{"latest", "node", "lts"})
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout.String(), "node 20.12.2 [stable, lts]") {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestRunVersionCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	app := NewAppWithVersion(&stdout, &stderr, "v1.2.3")
	app.newService = func() (runtimes.Service, error) {
		return nil, errors.New("should not be called")
	}

	exitCode := app.Run(context.Background(), []string{"version"})
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	if stderr.String() != "" {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}

	if stdout.String() != "toolmesh v1.2.3\n" {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestRunAdditionalRuntimeCommands(t *testing.T) {
	testCases := []struct {
		name            string
		args            []string
		service         stubService
		wantStdout      []string
		wantEmptyStderr bool
	}{
		{
			name: "list runtime",
			args: []string{"list", "go"},
			service: stubService{
				listFn: func(runtimeName string) ([]runtimes.InstalledRuntime, error) {
					if runtimeName != "go" {
						t.Fatalf("unexpected list arguments: %s", runtimeName)
					}
					return []runtimes.InstalledRuntime{
						{
							Runtime: "go",
							Version: "1.22.2",
							Home:    `C:\toolmesh\go\1.22.2`,
							Active:  true,
						},
					}, nil
				},
			},
			wantStdout:      []string{"* go 1.22.2 C:\\toolmesh\\go\\1.22.2"},
			wantEmptyStderr: true,
		},
		{
			name: "list remote runtime",
			args: []string{"list-remote", "java", "lts"},
			service: stubService{
				listRemoteFn: func(_ context.Context, runtimeName string, selector string) ([]runtimes.RemoteVersion, error) {
					if runtimeName != "java" || selector != "lts" {
						t.Fatalf("unexpected list-remote arguments: %s %s", runtimeName, selector)
					}
					return []runtimes.RemoteVersion{
						{Version: "21.0.2+13", Stable: true, LTS: true},
						{Version: "17.0.10+7", Stable: true, LTS: true},
					}, nil
				},
			},
			wantStdout:      []string{"21.0.2+13 [stable, lts]", "17.0.10+7 [stable, lts]"},
			wantEmptyStderr: true,
		},
		{
			name: "use runtime",
			args: []string{"use", "python", "3.12.2"},
			service: stubService{
				useFn: func(runtimeName string, version string) (runtimes.InstalledRuntime, error) {
					if runtimeName != "python" || version != "3.12.2" {
						t.Fatalf("unexpected use arguments: %s %s", runtimeName, version)
					}
					return runtimes.InstalledRuntime{
						Runtime:    "python",
						Version:    "3.12.2",
						Executable: `C:\toolmesh\python\3.12.2\python.exe`,
					}, nil
				},
			},
			wantStdout:      []string{"using python 3.12.2 (C:\\toolmesh\\python\\3.12.2\\python.exe)"},
			wantEmptyStderr: true,
		},
		{
			name: "current runtime",
			args: []string{"current", "java"},
			service: stubService{
				currentFn: func(runtimeName string) (runtimes.InstalledRuntime, error) {
					if runtimeName != "java" {
						t.Fatalf("unexpected current arguments: %s", runtimeName)
					}
					return runtimes.InstalledRuntime{
						Runtime:    "java",
						Version:    "21.0.2+13",
						Executable: `C:\toolmesh\java\21.0.2+13\bin\java.exe`,
					}, nil
				},
			},
			wantStdout:      []string{"java 21.0.2+13 C:\\toolmesh\\java\\21.0.2+13\\bin\\java.exe"},
			wantEmptyStderr: true,
		},
		{
			name: "remove runtime",
			args: []string{"remove", "python", "3.12.2"},
			service: stubService{
				removeFn: func(runtimeName string, version string) error {
					if runtimeName != "python" || version != "3.12.2" {
						t.Fatalf("unexpected remove arguments: %s %s", runtimeName, version)
					}
					return nil
				},
			},
			wantStdout:      []string{"removed python 3.12.2"},
			wantEmptyStderr: true,
		},
		{
			name: "uninstall runtime",
			args: []string{"uninstall", "node", "20.12.2"},
			service: stubService{
				removeFn: func(runtimeName string, version string) error {
					if runtimeName != "node" || version != "20.12.2" {
						t.Fatalf("unexpected uninstall arguments: %s %s", runtimeName, version)
					}
					return nil
				},
			},
			wantStdout:      []string{"removed node 20.12.2"},
			wantEmptyStderr: true,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			app := &App{
				stdout: &stdout,
				stderr: &stderr,
				newService: func() (runtimes.Service, error) {
					return testCase.service, nil
				},
			}

			exitCode := app.Run(context.Background(), testCase.args)
			if exitCode != 0 {
				t.Fatalf("expected exit code 0, got %d", exitCode)
			}

			for _, want := range testCase.wantStdout {
				if !strings.Contains(stdout.String(), want) {
					t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
				}
			}

			if testCase.wantEmptyStderr && stderr.String() != "" {
				t.Fatalf("expected empty stderr, got %q", stderr.String())
			}
		})
	}
}

func TestRunVenvCommands(t *testing.T) {
	testCases := []struct {
		name        string
		args        []string
		wantRuntime string
		wantPath    string
	}{
		{
			name:        "runtime first default path",
			args:        []string{"python", "venv"},
			wantRuntime: "python",
			wantPath:    "",
		},
		{
			name:        "runtime first explicit path",
			args:        []string{"py", "venv", ".venv-dev"},
			wantRuntime: "py",
			wantPath:    ".venv-dev",
		},
		{
			name:        "action first explicit path",
			args:        []string{"venv", "python", "envs/demo"},
			wantRuntime: "python",
			wantPath:    "envs/demo",
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			app := &App{
				stdout: &stdout,
				stderr: &stderr,
				newService: func() (runtimes.Service, error) {
					return stubService{
						createVenvFn: func(_ context.Context, runtimeName string, path string) (runtimes.VenvResult, error) {
							if runtimeName != testCase.wantRuntime || path != testCase.wantPath {
								t.Fatalf("unexpected venv arguments: %s %s", runtimeName, path)
							}
							return runtimes.VenvResult{
								Runtime:    "python",
								Version:    "3.12.2",
								Path:       `D:\workspace\.venv`,
								Executable: `C:\toolmesh\python\3.12.2\python.exe`,
							}, nil
						},
					}, nil
				},
			}

			exitCode := app.Run(context.Background(), testCase.args)
			if exitCode != 0 {
				t.Fatalf("expected exit code 0, got %d", exitCode)
			}

			if stderr.String() != "" {
				t.Fatalf("expected empty stderr, got %q", stderr.String())
			}

			if !strings.Contains(stdout.String(), `created venv at D:\workspace\.venv using python 3.12.2`) {
				t.Fatalf("unexpected stdout: %q", stdout.String())
			}
		})
	}
}

func TestRunPackageInstallCommands(t *testing.T) {
	testCases := []struct {
		name     string
		args     []string
		wantTool string
		wantArgs []string
	}{
		{
			name:     "package name passthrough",
			args:     []string{"pip", "install", "requests"},
			wantTool: "pip",
			wantArgs: []string{"requests"},
		},
		{
			name:     "requirements passthrough",
			args:     []string{"pip", "install", "-r", "requirements.txt", "--upgrade"},
			wantTool: "pip",
			wantArgs: []string{"-r", "requirements.txt", "--upgrade"},
		},
		{
			name:     "npm passthrough",
			args:     []string{"npm", "install", "react", "--save-dev"},
			wantTool: "npm",
			wantArgs: []string{"react", "--save-dev"},
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			app := &App{
				stdout: &stdout,
				stderr: &stderr,
				newService: func() (runtimes.Service, error) {
					return stubService{
						installPackagesFn: func(_ context.Context, installerName string, options runtimes.PackageInstallOptions) error {
							if installerName != testCase.wantTool {
								t.Fatalf("unexpected package installer: %s", installerName)
							}
							if !equalStrings(options.Args, testCase.wantArgs) {
								t.Fatalf("unexpected install args: got %#v want %#v", options.Args, testCase.wantArgs)
							}
							if options.Stdout != &stdout {
								t.Fatalf("expected stdout writer to be forwarded")
							}
							if options.Stderr != &stderr {
								t.Fatalf("expected stderr writer to be forwarded")
							}
							return nil
						},
					}, nil
				},
			}

			exitCode := app.Run(context.Background(), testCase.args)
			if exitCode != 0 {
				t.Fatalf("expected exit code 0, got %d", exitCode)
			}
			if stdout.String() != "" {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
			if stderr.String() != "" {
				t.Fatalf("expected empty stderr, got %q", stderr.String())
			}
		})
	}
}

func TestRunHelpShowsTopLevelRuntimeCommands(t *testing.T) {
	testCases := [][]string{
		nil,
		{"--help"},
	}

	for _, args := range testCases {
		args := args
		t.Run(strings.Join(append([]string{"toolmesh"}, args...), " "), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			app := NewApp(&stdout, &stderr)
			app.newService = func() (runtimes.Service, error) {
				return nil, errors.New("should not be called")
			}

			exitCode := app.Run(context.Background(), args)
			if exitCode != 0 {
				t.Fatalf("expected exit code 0, got %d", exitCode)
			}

			if stderr.String() != "" {
				t.Fatalf("expected empty stderr, got %q", stderr.String())
			}

			output := stdout.String()
			if !strings.Contains(output, "toolmesh version") {
				t.Fatalf("expected version usage, got %q", output)
			}
			if !strings.Contains(output, "toolmesh pip install <pip-args...>") {
				t.Fatalf("expected pip usage, got %q", output)
			}
			if !strings.Contains(output, "toolmesh npm install <npm-args...>") {
				t.Fatalf("expected npm usage, got %q", output)
			}
			if !strings.Contains(output, "toolmesh install <runtime> <version>") {
				t.Fatalf("expected top-level install usage, got %q", output)
			}
			if !strings.Contains(output, "toolmesh <runtime> venv [path]") {
				t.Fatalf("expected runtime-first venv usage, got %q", output)
			}
			if !strings.Contains(output, "toolmesh venv <runtime> [path]") {
				t.Fatalf("expected action-first venv usage, got %q", output)
			}
			if !strings.Contains(output, "git/gitforwindows") || !strings.Contains(output, "cmake") || !strings.Contains(output, "mingw/gcc") {
				t.Fatalf("expected supported runtime list to include build tools, got %q", output)
			}
			if strings.Contains(output, "toolmesh runtime install <runtime> <version>") {
				t.Fatalf("expected help to prefer top-level syntax, got %q", output)
			}
			if strings.Contains(output, "toolmesh runtime <same-subcommand> ...") {
				t.Fatalf("expected help to omit legacy alias note, got %q", output)
			}
		})
	}
}

func TestRunExecUsage(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	app := &App{
		stdout: &stdout,
		stderr: &stderr,
		newService: func() (runtimes.Service, error) {
			return stubService{}, nil
		},
	}

	exitCode := app.Run(context.Background(), []string{"exec"})
	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}

	if stdout.String() != "" {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}

	if !strings.Contains(stderr.String(), "usage: toolmesh exec <command> [args...]") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestRunVenvUsage(t *testing.T) {
	testCases := [][]string{
		{"venv"},
		{"python", "venv", "one", "two"},
	}

	for _, args := range testCases {
		args := args
		t.Run(strings.Join(append([]string{"toolmesh"}, args...), " "), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			app := &App{
				stdout: &stdout,
				stderr: &stderr,
				newService: func() (runtimes.Service, error) {
					return stubService{}, nil
				},
			}

			exitCode := app.Run(context.Background(), args)
			if exitCode != 1 {
				t.Fatalf("expected exit code 1, got %d", exitCode)
			}

			if stdout.String() != "" {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}

			if !strings.Contains(stderr.String(), "usage: toolmesh <runtime> venv [path] (or: toolmesh venv <runtime> [path])") {
				t.Fatalf("unexpected stderr: %q", stderr.String())
			}
		})
	}
}

func TestRunPackageInstallerUsage(t *testing.T) {
	testCases := []struct {
		name  string
		args  []string
		usage string
	}{
		{
			name:  "pip help",
			args:  []string{"pip"},
			usage: "toolmesh pip install <pip-args...>",
		},
		{
			name:  "pip missing install args",
			args:  []string{"pip", "install"},
			usage: "usage: toolmesh pip install <pip-args...>",
		},
		{
			name:  "pip unsupported subcommand",
			args:  []string{"pip", "uninstall", "requests"},
			usage: "usage: toolmesh pip install <pip-args...>",
		},
		{
			name:  "npm help",
			args:  []string{"npm"},
			usage: "toolmesh npm install <npm-args...>",
		},
		{
			name:  "npm missing install args",
			args:  []string{"npm", "install"},
			usage: "usage: toolmesh npm install <npm-args...>",
		},
		{
			name:  "npm unsupported subcommand",
			args:  []string{"npm", "ci"},
			usage: "usage: toolmesh npm install <npm-args...>",
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			app := &App{
				stdout: &stdout,
				stderr: &stderr,
				newService: func() (runtimes.Service, error) {
					return stubService{}, nil
				},
			}

			exitCode := app.Run(context.Background(), testCase.args)
			if exitCode != 1 && !(len(testCase.args) == 1 && exitCode == 0) {
				t.Fatalf("unexpected exit code %d", exitCode)
			}

			if len(testCase.args) == 1 {
				if stderr.String() != "" {
					t.Fatalf("expected empty stderr, got %q", stderr.String())
				}
				if !strings.Contains(stdout.String(), testCase.usage) {
					t.Fatalf("unexpected stdout: %q", stdout.String())
				}
				return
			}

			if stdout.String() != "" {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
			if !strings.Contains(stderr.String(), testCase.usage) {
				t.Fatalf("unexpected stderr: %q", stderr.String())
			}
		})
	}
}

func TestRunLegacyRuntimeEntrypointIsUnsupported(t *testing.T) {
	testCases := [][]string{
		{"runtime"},
		{"runtime", "--help"},
		{"runtime", "current"},
	}

	for _, args := range testCases {
		args := args
		t.Run(strings.Join(append([]string{"toolmesh"}, args...), " "), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			app := NewApp(&stdout, &stderr)
			app.newService = func() (runtimes.Service, error) {
				return nil, errors.New("should not be called")
			}

			exitCode := app.Run(context.Background(), args)
			if exitCode != 1 {
				t.Fatalf("expected exit code 1, got %d", exitCode)
			}

			if !strings.Contains(stderr.String(), "unknown command: runtime") {
				t.Fatalf("unexpected stderr: %q", stderr.String())
			}

			output := stdout.String()
			if !strings.Contains(output, "toolmesh install <runtime> <version>") {
				t.Fatalf("expected root usage, got %q", output)
			}
			if !strings.Contains(output, "mingw/gcc") {
				t.Fatalf("expected root usage to include build tool runtimes, got %q", output)
			}
			if strings.Contains(output, "toolmesh runtime <same-subcommand> ...") {
				t.Fatalf("expected root usage without legacy alias note, got %q", output)
			}
		})
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	app := NewApp(&stdout, &stderr)
	app.newService = func() (runtimes.Service, error) {
		return nil, errors.New("should not be called")
	}

	exitCode := app.Run(context.Background(), []string{"unknown"})
	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}

	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

type stubService struct {
	installFn         func(context.Context, string, string) (runtimes.InstalledRuntime, error)
	listFn            func(string) ([]runtimes.InstalledRuntime, error)
	useFn             func(string, string) (runtimes.InstalledRuntime, error)
	useProjectFn      func(context.Context, string, string) (runtimes.InstalledRuntime, error)
	currentFn         func(string) (runtimes.InstalledRuntime, error)
	currentAllFn      func() ([]runtimes.InstalledRuntime, error)
	createVenvFn      func(context.Context, string, string) (runtimes.VenvResult, error)
	installPackagesFn func(context.Context, string, runtimes.PackageInstallOptions) error
	removeFn          func(string, string) error
	listRemoteFn      func(context.Context, string, string) ([]runtimes.RemoteVersion, error)
	latestFn          func(context.Context, string, string) (runtimes.RemoteVersion, error)
	execPathFn        func() ([]string, error)
}

func (s stubService) Install(ctx context.Context, runtimeName string, version string) (runtimes.InstalledRuntime, error) {
	if s.installFn == nil {
		return runtimes.InstalledRuntime{}, errors.New("install not implemented")
	}
	return s.installFn(ctx, runtimeName, version)
}

func (s stubService) List(runtimeName string) ([]runtimes.InstalledRuntime, error) {
	if s.listFn == nil {
		return nil, errors.New("list not implemented")
	}
	return s.listFn(runtimeName)
}

func (s stubService) Use(runtimeName string, version string) (runtimes.InstalledRuntime, error) {
	if s.useFn == nil {
		return runtimes.InstalledRuntime{}, errors.New("use not implemented")
	}
	return s.useFn(runtimeName, version)
}

func (s stubService) UseProject(ctx context.Context, runtimeName string, version string) (runtimes.InstalledRuntime, error) {
	if s.useProjectFn == nil {
		return runtimes.InstalledRuntime{}, errors.New("use project not implemented")
	}
	return s.useProjectFn(ctx, runtimeName, version)
}

func (s stubService) Current(runtimeName string) (runtimes.InstalledRuntime, error) {
	if s.currentFn == nil {
		return runtimes.InstalledRuntime{}, errors.New("current not implemented")
	}
	return s.currentFn(runtimeName)
}

func (s stubService) CurrentAll() ([]runtimes.InstalledRuntime, error) {
	if s.currentAllFn == nil {
		return nil, errors.New("current all not implemented")
	}
	return s.currentAllFn()
}

func (s stubService) CreateVenv(ctx context.Context, runtimeName string, path string) (runtimes.VenvResult, error) {
	if s.createVenvFn == nil {
		return runtimes.VenvResult{}, errors.New("create venv not implemented")
	}
	return s.createVenvFn(ctx, runtimeName, path)
}

func (s stubService) InstallPackages(ctx context.Context, installerName string, options runtimes.PackageInstallOptions) error {
	if s.installPackagesFn == nil {
		return errors.New("package install not implemented")
	}
	return s.installPackagesFn(ctx, installerName, options)
}

func (s stubService) Remove(runtimeName string, version string) error {
	if s.removeFn == nil {
		return errors.New("remove not implemented")
	}
	return s.removeFn(runtimeName, version)
}

func (s stubService) ListRemote(ctx context.Context, runtimeName string, selector string) ([]runtimes.RemoteVersion, error) {
	if s.listRemoteFn == nil {
		return nil, errors.New("list remote not implemented")
	}
	return s.listRemoteFn(ctx, runtimeName, selector)
}

func (s stubService) Latest(ctx context.Context, runtimeName string, selector string) (runtimes.RemoteVersion, error) {
	if s.latestFn == nil {
		return runtimes.RemoteVersion{}, errors.New("latest not implemented")
	}
	return s.latestFn(ctx, runtimeName, selector)
}

func (s stubService) ExecPath() ([]string, error) {
	if s.execPathFn == nil {
		return nil, nil
	}
	return s.execPathFn()
}

func equalStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
