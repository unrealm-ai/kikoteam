# builtin-mcp

Sandbox 内独立运行的 TypeScript MCP 服务。提供六个 Pi 文件工具和五个受管 Bash 工具，由 Go sandbox-runner 启动和管理，Pi 在 Engine 内通过 Streamable HTTP 直连。本包也可独立启动、测试和打包；Runner 本期范围见[运行说明](../../docs/operations/minimal-agent-runtime.md)。

工具与复用依据见[专项设计](../docs/builtin-mcp.md)，源码归档基线见[source-archive.json](source-archive.json)，Pi 1.5.2 移植清单与差异见[provenance.json](vendor/pi-tools/provenance.json)，许可见[LICENSE](vendor/pi-tools/LICENSE)。

## 工具

| 文件工具            | 用途                                                     |
| ------------------- | -------------------------------------------------------- |
| read / write / edit | 读取、创建或覆盖、精确替换文件内容；edit 使用 edits 数组 |
| grep / find / ls    | 搜索内容、按 glob 查找文件、列出目录                     |

| Bash 工具      | 用途                                                                 |
| -------------- | -------------------------------------------------------------------- |
| bash_start     | 启动命令，返回 UUID 句柄、当前状态和首批输出                         |
| bash_poll      | 使用句柄和不透明游标查询状态及后续输出                               |
| bash_write     | 向 pipe / PTY 写入输入，保留 accepted / partial / indeterminate 结果 |
| bash_terminate | 请求终止进程组，TERM 后按固定宽限期升级 KILL                         |
| bash_list      | 列出本 Session 的受管进程元数据和保留的退出记录                      |

文件工具保留 Pi schema 和执行行为，去掉终端 UI 渲染；Bash、文件保护和 SQLite 复用用户归档。运行代码不加载完整 Pi Harness，Pi 包仅作为构建类型和回归对照的开发依赖。模型文本、参数、文件内容及认证材料不进入普通日志。

## 环境与检查

**当前实现**：支持 Mac/Linux 的[裸 Sandbox](../docs/local-sandbox.md)，与 Engine 同宿主同账号。`npm run test:process` 执行通用与真实进程/PTY 验收，test:linux 保留 Linux 专有入口。macOS 普通账号已验证文件、HTTP、pipe/PTY、停止和进程组收尾，以及 Go Runner 的实际编排链；Linux 专有 /proc 竞争测试只在 Linux 执行，本批结果待 CI。

完整运行和验收使用 **Linux、Node 24.18.1、Bash、Python 3、procps、rg、fd**。builtin、FileWorker、命令监督进程和 Bash 全部继承 sandbox-runner 的 UID/GID，不切换账号，也不要求 root。容器默认使用 10001:10001，部署时由 Runner 的运行账号统一决定。裸目录不提供容器隔离，同 UID 的 Bash 可以访问该账号有权限访问的管理文件。

```sh
npm ci
npm rebuild node-pty
python3 -m venv ../../event-hub/.venv
../../event-hub/.venv/bin/pip install -r ../../event-hub/requirements-dev.txt
make lint  # 可在 macOS 运行，无需 Linux/root
make check
```

`make lint` 校验管理 OpenAPI，并通过 `npm run check:static` 在临时副本编译 vendor 类型、检查业务类型、ESLint、格式和派生 Schema，不修改工作区或依赖旧 lib/dist。`npm run typecheck` 同样使用临时副本。OpenAPI 校验器可通过 `OPENAPI_VALIDATOR=<绝对路径>` 指定；独立分发时可自行按 event-hub/requirements-dev.txt 的固定版本安装。

业务 TS 的圈复杂度上限 35、嵌套深度上限 6；.mjs 测试免除这两项限制，仅保持基础 lint，不增加 checkJs。辅助脚本规则维持现状。根目录可用 `make lint COMPONENT=sandbox-runner/builtin-mcp`，完整约定见[静态检查](../../docs/rules/static-checks.md)。

`npm run check` 在静态检查之外执行普通回归、HTTP 和本机真实进程验收，支持普通账号。`npm test` 为普通回归入口，Linux 专有用例在其他系统显式跳过；Mac 和 Linux 验收分别记录。`make fmt` 显式格式化，`make build` 清理旧产物后编译 TS 并打包 schema，`npm run catalog` 输出文件工具目录。

移植的 Pi 源码在 vendor 下单独以 strict 模式编译，沿用上游的数组索引检查设置，并保留来源摘要。平台实现继续开启 noUncheckedIndexedAccess。第三方源码不套用本包的风格重排或复杂度阈值。项目不再维护或编译自有 C 辅助程序；PTY 仍使用 node-pty，其第三方原生依赖在没有预构建包时仍需 C++ 构建工具。

也可构建独立容器，Docker 或 Podman 均可：

```sh
podman build -t builtin-mcp .
podman run --rm builtin-mcp npm run check
```

镜像包含编译和测试依赖，默认启动 HTTP 服务；后续可单独缩减发布镜像。CI 使用同一 `npm run check`，见[工作流](../../.github/workflows/builtin-mcp.yml)。

## HTTP 启动

```sh
node --disable-warning=ExperimentalWarning dist/http-main.js --config /run/builtin-mcp.json
```

启动文件必须由管理身份拥有，权限 0600，最大 64 KiB。示意配置如下；UUID、凭据和目录由启动者实际生成，不能使用示例凭据部署。

```json
{
  "host": "127.0.0.1",
  "port": 9000,
  "allowed_hosts": ["127.0.0.1", "sandbox.internal"],
  "discovery_token": "replace-with-a-random-secret-at-least-32-chars",
  "launch_args": [
    "--cwd",
    "/workspace",
    "--workspace-id",
    "11111111-1111-4111-8111-111111111111",
    "--session-id",
    "22222222-2222-4222-8222-222222222222",
    "--sandbox-instance-id",
    "33333333-3333-4333-8333-333333333333",
    "--runner-boot-id",
    "44444444-4444-4444-8444-444444444444",
    "--state-dir",
    "/run/builtin-state",
    "--runtime-dir",
    "/tmp/kr-example/b-example",
    "--connection-id",
    "55555555-5555-4555-8555-555555555555",
    "--binding-id",
    "builtin",
    "--config-revision",
    "1"
  ]
}
```

工作目录必须已存在并允许 Runner 的运行账号使用。状态目录归当前账号所有，保持 0700，文件为 0600，并与工作目录分开；这些权限用于排除其他账号，不隔离同 UID 的 Bash。文件工具仍拒绝直接操作状态目录，以避免误修改。Node、包代码、搜索工具及依赖须对该账号可读/执行。旧 `--child-uid / --child-gid` 参数已删除，继续传入会导致配置校验失败。当前工作目录省略时仍默认 `/workspace`；后续本地编排必须显式传入稳定 project 绝对路径，示例不是 Mac/Linux 的目标默认布局。工具省略 cwd 时使用实际启动目录。

唯一 HTTP MCP 路径是 `/mcp`，要求 `Authorization: Bearer <token>`，只接受 allowlist 中的 Host，拒绝浏览器 Origin，默认 loopback。HTTP 同时支持 2026-07-28 和 Pi 1.5.2 使用的 2025-11-25，通过 SDK 自带 stateless HTTP 兼容层执行相同工具；stdio/FileWorker 仍固定 2026-07-28。实际 Pi ACP/Pi、Go Runner 与 Backend 回归已通过，供应商为确定性协议服务，真实供应商仍待配置验收。

服务启动后只允许发现工具。Runner 同步执行窗口后，HTTP 入口在准入时绑定可信 Session、运行实例及 Turn，生成内部操作与追踪身份；模型提供的 `_meta` 不能覆盖它们。内部操作记录不能为新 HTTP 请求提供跨重试 exactly-once 保证，断线后不自动重放命令或 stdin。

**固定 Token 与 Session 绑定**：同一绑定后续窗口必须使用原 Token，管理修订、前轮排空和停止核对独立生效。Go Runner 已将 Token 持久化，为每个 task Session 启动独立 builtin，复用 project 并使用独立 Token/账本。HTTP 入口原生兼容 Pi 使用的 MCP 2025-11-25，stdio/私有通道仍使用 2026-07-28。

## 私有管理协议

启用受管 Bash 的 HTTP 和 stdio 入口都要求 `--runtime-dir`。启动者为每个实例创建独占的本机临时目录（0700），传入绝对路径，在实例退出后回收。Go Runner 使用 `/tmp/kr-*/b-*/`。管理 HTTP 监听目录下的 `m.sock`（0600），每个 Bash 使用同目录下的 `c-<8 位十六进制数>`。完整 socket 路径最长 100 字节。SQLite 保存在 `--state-dir`。契约见[management.openapi.yaml](contracts/management.openapi.yaml)。

| 方法与路径             | 作用                                                                                       |
| ---------------------- | ------------------------------------------------------------------------------------------ |
| GET /state             | 核对完整活动账本，返回 source、门禁、已应用修订、verified、active、unknown、pending_writes |
| POST /execution-window | 设置当前可信执行窗口，200 表示已应用且空闲，202 表示仍有活动事实                           |
| POST /stop             | 关闭窗口并终止受管进程组；200/202 后继续查询 state，不按 HTTP 响应推断进程已退出           |

两个 POST 使用相同请求结构，stop 必须 `open=false`：

```json
{
  "control_revision": 2,
  "assignment_id": "66666666-6666-4666-8666-666666666666",
  "generation": 1,
  "harness_instance_id": "77777777-7777-4777-8777-777777777777",
  "turn_id": "88888888-8888-4888-8888-888888888888",
  "open": true,
  "access_token": "replace-with-a-fixed-random-binding-token-32-chars"
}
```

初始关闭修订为 1，首次设置窗口从 2 开始，之后单调递增。相同修订同内容可重复同步；相同修订不同内容或旧修订返回 409。切轮前排空前轮 RPC、在途调用及未知结果；同一归属、代次和 Harness 可在后续 Turn 继续管理已确认的后台进程，origin_turn_id 保留创建时的 Turn。更换运行实例或停止后重开须确认原进程收尾，否则返回 409。stop 使用更大的修订关闭窗口，后续开启窗口再增加修订，继续使用固定 Token；不增加平台公开 control_id 或幂等 key。

```sh
curl --unix-socket /tmp/kr-example/b-example/m.sock http://localhost/state
```

SIGTERM / SIGINT 同样先关闭准入、终止进程组和排空，再退出；若 12 秒内不能完成则失败退出并保留未决账本。直接 SIGKILL 无法确认收尾，重启保持 lost / unknown，不能清库或重复启动原命令来“恢复”。

## TS 启动与停止

每条命令由 TS supervisor 监督。builtin 先写入 starting 记录，启动尚未运行 Bash 的 supervisor；收到 ready 后保存监督身份，再经受控连接发送 start。supervisor 使用 Node spawn 启动 Bash，分别报告启动错误、启动成功和实际退出结果。记录失败或放行前取消不会执行命令；start 已送达但结果未知时不自动重试。

创建命令控制 socket 失败时，`bash_start` 返回 `control_socket_unavailable` 并释放预占的进程名额。操作回执保留该错误，后续新调用可在目录恢复后执行。此前已经记录的 `lost` 保留原状。

supervisor 与 Bash 使用同一进程组。停止时 supervisor 向自身进程组发 TERM，并保持运行以执行固定 5 秒宽限期；强制停止先通过存活的直接子进程句柄取得 Bash 实际退出结果，再向自身进程组发 KILL 收尾，包括终止 supervisor 自身。无法确认退出时保留未知，不能从“信号已发送”猜测命令结果。管理连接断开时 supervisor 同样自行收尾。

不再使用 pidfd 或遍历旧 PID 发信号。supervisor 异常死亡后，原执行保持 lost / unknown，后续由 Runner 核对或环境回收；本机制覆盖留在受管进程组的普通子进程，不承诺回收主动 setsid 等脱离组的进程。容器销毁可作为环境级兜底，裸目录不能凭清空记录宣告停稳。

## 契约与验收记录

六个文件工具的 schema 从移植源码派生，`fixtures/tools.json` 与上游 1.5.2 工厂做目录对照。五个 Bash 工具及旧 stdio 私有管理方法使用 `contracts/json-schema/builtin/` 的归档 JSON Schema，构建原样复制，运行时由同一份 schema 校验输入/输出。`activity-result.json` 是为 OpenAPI 引用提取的派生文件，使用 `npm run generate` 更新，检查入口校验其与来源一致。原 TypeSpec 放在 references 中作为来源参考；本包当前不提供 TypeSpec 生成器，不把两份格式同时当作可编辑契约。

2026-11-16 更新为同账号、无自有 C 的实现。Linux arm64、Node 24.18.1、普通账号 10001:10001 下，`npm run check` 通过：59 项普通/HTTP/握手回归和 4 项 Linux 进程验收，共 63 项，零失败、零跳过。覆盖全部 11 个工具、记录失败与放行前取消、执行门禁竞争、账号继承、文件保护、stdin/PTY、输出分页、TERM/KILL、管理断连收尾、存储损坏和重启未知结果。类型、ESLint、格式和派生 Schema 检查通过，私有 OpenAPI 校验通过；打包产物不含已删除的 C 辅助程序或身份切换入口。

除本模块的验收外，Backend runtime e2e 已通过独立 Engine/Runner、原版 Pi ACP/Pi、实际 HTTP MCP 与文件/Bash/PTY；模型为确定性协议服务。Mac/Linux 的本批运行结果分别见实施计划，不将本地 Mac 结果当作 Linux 已通过。
