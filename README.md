# toolmesh

toolmesh 是一个面向 Windows 的命令行工具，用来安装、切换和在当前 shell 或项目目录中使用常见开发时运行时与工具链。

当前内置支持的 runtime/provider：

- Python (`python`, `py`)
- Node.js (`nodejs`, `node`)
- Java
- Go (`go`, `golang`)
- Git for Windows (`git`, `gitforwindows`)
- CMake
- MinGW (`mingw`, `gcc`, `mingw-w64`)

也支持在合适的上下文中透传常见包管理器命令：

- `toolmesh pip install <pip-args...>`
- `toolmesh npm install <npm-args...>`

## 当前状态

- 模块声明位于 [`go.mod`](go.mod)，目标 Go 版本为 `1.22`
- 当前实现和测试均以 Windows 为主，provider 平台判断目前也仅对 Windows 开放
- 仓库已经包含 CLI 入口、runtime 管理逻辑和一组覆盖核心行为的单元测试

## 仓库结构

- [`cmd/toolmesh`](cmd/toolmesh): CLI 入口
- [`internal/cli`](internal/cli): 参数解析、命令分发、终端输出
- [`internal/runtimes`](internal/runtimes): runtime/provider、下载、安装、状态与项目选择逻辑
- [`.github`](.github): Issue / PR 模板

## 构建、运行与测试

下面的命令已经在当前仓库环境中验证过。如果你的 `go` 已经在 `PATH` 中，可以将 `D:\Env\tools\go\bin\go.exe` 替换为 `go`。

```powershell
& 'D:\Env\tools\go\bin\go.exe' test ./...
& 'D:\Env\tools\go\bin\go.exe' build ./cmd/toolmesh
.\toolmesh.exe --help
.\toolmesh.exe version
```

## 常用命令

```powershell
toolmesh list-remote python
toolmesh latest node lts
toolmesh install python 3.12.10
toolmesh use python 3.12.10
toolmesh use --project node 20
toolmesh current
toolmesh python venv
toolmesh exec python --version
```

包管理器透传命令需要满足对应上下文：

- `toolmesh pip install ...` 需要当前已经存在 Python 虚拟环境；可先执行 `toolmesh python venv` 或 `toolmesh venv python`
- `toolmesh npm install ...` 需要当前存在已激活或项目选中的 Node.js runtime

## 配置与状态

toolmesh 会维护两类状态：

- 项目级选择：`toolmesh use --project ...` 会在当前目录或最近的祖先目录写入 `.toolmesh.json`
- 用户级状态：默认使用系统用户目录保存 `state.json`、下载缓存、安装目录和 shims

可用环境变量：

- `TOOLMESH_HOME`: 同时覆盖配置目录；如果未设置 `TOOLMESH_DATA_DIR`，也会作为数据目录
- `TOOLMESH_DATA_DIR`: 单独覆盖 runtime、下载缓存和 shims 的数据目录

## 开发说明

- 当前仓库未配置 CI 工作流；提交前请至少在本地运行 `go test ./...`
- 新增或调整用户可见行为时，请同步补充测试，并更新 README 或相关社区文档
- 仓库采用小步提交的方式维护；请尽量保持补丁聚焦，避免无关重构
