# 静态检查

适用于主要 Go / TypeScript 业务代码及其测试。辅助 Python、C、构建和生成脚本不新增静态检查规则；已有脚本基础检查和 C 编译告警保留。组件命令及运行环境见各组件 README。

## 入口与覆盖

本地与 CI 共用根目录 `make lint`，单组件使用 `make lint COMPONENT=<组件路径>`。检查不能修改源码、契约或已提交生成物；工具安装、依赖缓存及临时编译产物不属于源码修改。`make fmt` 显式格式化，`make check` 追加真实环境测试和数据库结构验收。

当前统一入口接入 backend、event-hub、agent-engine、sandbox-runner 和 sandbox-runner/builtin-mcp。Go 组件使用同一套规则；Runner 只扫描 cmd/internal，避免将其 TS 子项目 node_modules 中的第三方 Go 示例纳入自有代码检查。Engine 进程回归需先安装固定上游 npm 依赖并构建 builtin。Backend 纳入 OpenAPI 校验和真实 PostgreSQL 验收；实际 Engine/Runner/Pi 全链路还需构建 Runner。未接入的组件选择参数直接失败。

Go 检查器与工作流校验器固定在 `tools/versions.mk`，自动安装到忽略提交的 `.tools/`；TS 工具沿用组件 package.json 和 package-lock.json，使用 `npm ci`。Go 检查使用只读模块模式，不自动改写 go.mod/go.sum。升级工具时核对 Go/Node 兼容性及规则变化，不使用浮动 latest 或全部规则开关。

## 业务代码

| 范围 | 强制检查 |
| --- | --- |
| Go 格式 | gofmt；格式失败列出文件 |
| Go 正确性 | govet、staticcheck、unused、ineffassign |
| Go 错误与资源 | errcheck（含显式丢弃）、errorlint、bodyclose、sqlclosecheck、rowserrcheck |
| Go 取消传播 | contextcheck、noctx；仅覆盖工具支持的调用形式，不证明所有等待都有超时 |
| Go 复杂度 | gocyclo：圈复杂度不超过 35 |
| TS 类型 | strict、noUncheckedIndexedAccess、未使用变量/参数、switch 穿透检查 |
| TS 正确性 | recommendedTypeChecked、no-floating-promises（ignoreVoid=false）、no-misused-promises、no-explicit-any、switch-exhaustiveness-check |
| TS 复杂度 | 圈复杂度不超过 35，嵌套深度不超过 6 |
| TS 格式 | Prettier |

不限制函数行数，不要求为满足指标而拆散完整状态机。深度 6 对应 TS 的 max-depth；Go 未启用把认知复杂度当作嵌套层数的规则。权限、并发状态机、幂等和失锁收尾仍需行为测试与评审。

## 测试与豁免

Go 的宽松规则只匹配 `_test.go` 和根目录下明确登记的 `event-hub/internal/testpg/`，不匹配其他组件的同名目录。它们不限制圈复杂度，取消传播规则 contextcheck/noctx 不约束测试自管的进程与取消时序。错误处理和资源检查仍启用；测试清理允许用 `_ =` 显式忽略 Close/Kill 错误（故障测试可能已主动断连/杀进程），无意遗漏的 Close/Kill 返回值仍会报错。业务文件即使用 `_ =` 也不享受该豁免。其他普通调用的错误仍须处理，必要例外使用局部说明。

现有 .mjs 测试仅使用 ESLint 基础规则，不限制复杂度和嵌套，不增加 checkJs 或强制类型改造；豁免只匹配组件顶层 tests 目录，不影响 src 下的业务代码。构建/生成脚本维持原有规则。

Go 的 nolint 必须明确规则和理由，由 nolintlint 检查；只允许局部、必要的例外。TS 禁止 @ts-ignore 和 @ts-nocheck，@ts-expect-error 必须有说明；ESLint 豁免需在相邻注释解释原因，并报告失效的 disable 指令。不得通过扩大整目录排除或关闭正确性规则来消除告警。

vendor / third_party 不进行自有代码风格重排。保留 vendor 类型编译、Goose 来源和补丁完整性检查及本地适配文件的 gofmt；Go 主模块的 ./... 不包含独立 third_party 模块，不代表上游全量静态扫描。

## 契约与持续集成

保留 OpenAPI 校验、派生 Schema 一致性、Goose 补丁校验和迁移历史保护。数据库快照重放归完整 check，需要真实 PG；迁移历史使用 CI 提供的基线提交。

Static checks 工作流在每个 PR 和 main push 上执行根 make lint，无 PostgreSQL 或 root 依赖。actionlint 只检查工作流，关闭其可选 ShellCheck/Pyflakes 集成，避免新增辅助脚本检查。现有组件工作流继续完成数据库和 Linux 身份隔离验收。强制合并门禁需在仓库保护设置选择 Static checks 的 lint 状态；工作流文件本身不修改远程保护设置。

修改规则时验证失败路径：超过阈值、漏处理错误或 Promise、无效豁免、生成物漂移应使检查失败；测试复杂度豁免不能影响生产代码。检查前后核对源码和生成物不变。

## 本次规则调整

原 Go 入口仅包含 gofmt/go vet，本次增加上述显式正确性规则。TS 业务代码从复杂度 20、深度 4 放宽为 35、6，给状态机和协议处理保留空间；测试继续免除这两项限制。Python/C 不新增检查器，.mjs 测试不增加类型检查。类型、错误、异步和资源方面的防护保留。
