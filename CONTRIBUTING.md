# 贡献指南

感谢你关注 `toolmesh`。

这个仓库目前仍处于早期阶段，贡献方式以小范围、可验证、易审查的改动为主。提交前请先确认你的改动直接服务于当前目标，避免顺手进行无关重构。

## 开始之前

- Bug 报告请优先使用现有的 Issue 模板
- 新功能、provider 支持或行为调整，建议先开 Issue 对齐范围
- 涉及安全问题时，请不要公开披露，改走 [`SECURITY.md`](SECURITY.md) 中的流程

## 本地开发

仓库当前的真实工具链是 Go 模块项目，`go.mod` 声明版本为 `1.22`。

如果 `go` 不在 `PATH` 中，可以直接使用完整路径：

```powershell
& 'D:\Env\tools\go\bin\go.exe' test ./...
& 'D:\Env\tools\go\bin\go.exe' build ./cmd/toolmesh
```

如果你本机已经配置好了 `go`，也可以使用等价命令：

```powershell
go test ./...
go build ./cmd/toolmesh
```

## 提交约定

- 提交信息请遵循 Conventional Commits，例如 `feat(cli): add runtime aliases`
- 行为变更应尽量附带测试
- 用户可见的命令、约束或目录变化，需要同步更新 README
- 请保留现有目录布局，不要把 `cmd/`、`internal/` 改造成另一套结构

## Pull Request

- 保持 PR 聚焦单一主题
- 在 PR 描述中写清楚问题、改动内容和验证方式
- 当前仓库已经提供 PR 模板，请按模板补全摘要、验证结果和自检项

## 沟通方式

- 技术讨论请尽量基于代码和复现事实
- 如果你做了取舍，请把风险和替代方案一并写出来，方便后续维护
