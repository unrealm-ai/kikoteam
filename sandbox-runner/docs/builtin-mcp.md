# builtin MCP：工具复用与接入设计

状态：2026-11-17 同账号、无自有 C 的实现，保留 SQLite。已完成独立 builtin MCP 的源码移植、HTTP 与私有管理适配；运行、构建和测试入口见[模块 README](../builtin-mcp/README.md)。Go Runner 的配置领取与管理接口已实现，stdio Gateway 仍待接入。本文维护内置工具，环境控制见[运行时设计](runtime-and-mcp.md)，Pi 装配和 Skill 执行见[Engine 运行时](../../agent-engine/docs/session-runtime.md)。

## 1. 实现选择与边界

**首版环境目标已更新为 Mac/Linux 本地运行**：按[裸 Sandbox](local-sandbox.md)，Engine、Runner、builtin 和命令同宿主同账号，默认工作位置为稳定 project 目录，不做安全隔离，容器仅保留扩展。当前 builtin 生产进程入口仍限制 Linux；下面已有实现与历史验收不代表 Mac 已可用。跨平台适配应保留 11 工具契约，并核验进程观察、信号、PTY 和原生依赖。

builtin MCP 保留 **TypeScript / Node.js**，作为 sandbox-runner 组件内的独立包和子进程，运行在 Sandbox 中。Runner 及其 stdio MCP Gateway 仍用 Go；不为统一语言重写已有工具，也不增加第六个部署组件。

首版采用 11 个工具：六个文件工具从固定版本 Pi 源码移植，五个 Bash 工具及进程管理复用归档与已有实现。适配 HTTP、可信调用上下文、身份、组件装配，以及 Mac/Linux 的进程和依赖差异；不因跨平台另造一套工具语义。Pi 继续使用未修改上游版；独立 MCP 的工具代码适配不修改 Harness。

```text
agent-engine
  Pi --mode rpc
             ├─ Streamable HTTP → builtin MCP（TS，Sandbox 内）
             ├─ Streamable HTTP → Runner Gateway（Go）→ stdio 插件
             └─ Streamable HTTP → 远程 MCP

sandbox-runner（Go，Sandbox 内）
  ├─ 启动 / 管理 builtin MCP（TS）
  ├─ 启动 / 管理 stdio 插件
  └─ 私有管理通道 → builtin MCP 的门禁、活动核对和退出收尾
```

这里的文件工具只操作执行环境中的文件，不建立平台 File、Artifact、上传下载或共享目录资源。builtin MCP 是平台执行环境的固定能力，不要求用户为它创建 Plugin / PluginAuth；其访问凭据由运行时装配。chat 通常没有 Sandbox，也不因本设计自动获得这些工具。

## 2. 源码基线与复用清单

用户归档日期为 2026-11-15，源码提交为 `ff8544165cd414bc587abda92c6810e923e10cb9`，收录 54 个文件及各自的字节数、SHA-256。本轮已核对 54 份正文与清单一致。归档引用但没有附带原仓库根 lockfile；六个 Pi 文件工具的完整实现也不在归档正文中，须另外取得。

| 部分 | 采用方式 | 需要保留或调整的内容 |
| --- | --- | --- |
| 六个文件工具 | 从 Pi 固定 tag / commit 移植源码及必需辅助依赖 | 保留上游 schema、描述、参数预处理、截断及错误语义；不自行另写一套文件算法 |
| 文件工具包装 | 复用归档 `files.ts`、FileWorker、StateGuard 等 | FileWorker 继承 Runner 身份；保留文件工具的状态目录防误操作、普通文件检查、读取及图片大小限制 |
| 五个 Bash 工具 | 复用 `process/`、契约和测试 | 保留 PTY / pipe、进程身份、进程组停止、输出游标、SQLite 记录及不确定结果处理 |
| 启动与停止 | TS supervisor 握手及进程组管理 | 删除 exec-gate、identity-signal 和账号切换；只通过存活的受管连接控制进程，不按历史 PID 补发信号 |
| MCP 服务层 | 复用工具注册与结果转换，新增薄 HTTP 入口 | 归档入口是 stdio；生产接入改为本服务直接提供 Streamable HTTP |
| 私有管理协议 | 复用活动核对和门禁逻辑，接入 Runner 管理通道 | 不把原私有方法直接暴露到 Pi 可访问的 MCP 端点 |
| 旧项目装配 | 在边界适配 | 旧包名前缀、产品域名、ID 正则、默认路径、启动参数和测试样例不原样沿用 |

归档依赖 `@earendil-works/pi-coding-agent@1.4.0`。本项目拟以已核验的 [Pi v1.5.2](https://github.com/earendil-works/pi/releases/tag/v1.5.2) 作为文件工具移植基线，与当前 Harness 候选一致；先对照归档包装层验证 1.4.0 → 1.5.2 的工厂接口、schema、错误和依赖差异。两者版本不同，不能把归档测试通过当成新版已经兼容。

源码位置为 [Pi 文件工具目录](https://github.com/earendil-works/pi/tree/v1.5.2/packages/coding-agent/src/core/tools)。除六个入口文件，还需核对路径解析、截断、edit diff、文件写入队列、图片处理、工具包装及搜索程序管理等依赖；不假设只复制六个文件就能独立编译。保留 [Pi MIT 许可及版权声明](https://github.com/earendil-works/pi/blob/v1.5.2/LICENSE)，记录来源版本、commit、移植文件清单及本地适配差异。后续升级显式更新并回归，不在运行时拉取 latest。

## 3. 工具清单

以下为设计摘要，机器可读 schema 在移植时从对应源码或原契约生成。Pi 工具保留原字段大小写，不因平台 REST 使用 snake_case 而改写上游 MCP 字段。

### 六个文件工具

| 工具 | 用途 | 主要入参 |
| --- | --- | --- |
| `read` | 读取文本或支持的图片，文本支持按行读取与截断 | `path`；可选 `offset`、`limit` |
| `write` | 创建或完整覆盖文件，按上游行为准备父目录 | `path`、`content` |
| `edit` | 精确文本替换，匹配原文件中的目标片段 | `path`、`edits: [{oldText, newText}]`，采用 v1.5.2 结构 |
| `grep` | 搜索文件内容 | `pattern`；可选 `path`、`glob`、`ignoreCase`、`literal`、`context`、`limit` |
| `find` | 按 glob 查找文件 | `pattern`；可选 `path`、`limit` |
| `ls` | 列出目录内容 | 可选 `path`、`limit` |

默认搜索范围和相对路径以 Runner 注入的实际工作目录为准。文件工具保留 Pi 输出结构，经现有适配器转换为 MCP 结果，不统一包装成 Bash 的进程结果。工具失败、正常结果截断和没有匹配项分别处理。

保留归档的文件访问包装：普通文件读取上限 16 MiB，图片上限 32 MiB、4000 万像素。`read` 按传入的相对路径原样打开项目文件，保留文件名开头的 `@` 和 Unicode 空格。Pi 在内存中缩放和编码图片。文件工具拒绝读取设备、管道等特殊文件，写入前完成目标核验，不先截断再检查。文件工具拒绝直接操作状态目录；同 UID 的 Bash 仍可访问该账号有权限访问的管理文件，不宣称账号隔离。上述文件工具限制随移植回归。

工作目录不是强制文件系统根。首版绝对路径、链接和搜索程序的可达范围使用本机账号权限，不提供安全隔离。工具默认 cwd 为稳定 project，Engine/Pi 与工具可共用同一固定版本 Skill 绝对路径；自动读取正文用 read，脚本用 bash_start，不另行开启 Pi 本地工作工具。状态目录保护等包装用于防误操作，不承诺阻止同账号 Bash 访问。

### 五个 Bash 工具

| 工具 | 用途 | 入参与主要返回 |
| --- | --- | --- |
| `bash_start` | 启动受管理命令，短暂等待后返回句柄及首批输出 | 必填 `command`；可选 `cwd`、`tty`、`stdin_open`、`yield_time_ms`、`max_output_bytes`、`max_runtime_ms`；返回进程快照和输出页 |
| `bash_poll` | 查询原进程状态并继续读取输出 | `process_id`；可选 `cursor`、`yield_time_ms`、`max_output_bytes`；返回进程快照和输出页 |
| `bash_write` | 向原进程写 UTF-8 输入或关闭普通 stdin | `process_id`，以及非空 `text` 或 `close_stdin=true`；返回已接受字节数及输入结果 |
| `bash_terminate` | 请求终止原进程组 | `process_id`；可选 `mode=graceful/force`；返回终止受理与当前进程证据 |
| `bash_list` | 查询本 Session 管理的进程与保留的退出记录 | 可选 `cursor`、`limit`；返回元数据列表，输出通过 poll 获取 |

这些能力由 Harness 自主调用，平台不把一次 poll 或进程子步骤另建为 Turn。CLI 插件也可通过 `bash_start` 执行已安装命令，无需再加一个通用 `exec` 工具。builtin 不移植 Pi 原生 `bash`，避免出现两套命令执行语义。

沿用归档参数基线：

| 参数 | 默认值与限制 |
| --- | --- |
| `command` | 非空、不能全为空白、无 NUL，最多 65536 UTF-8 字节 |
| `cwd` | 省略时使用可信启动配置的实际工作目录；不硬编码原项目的 `/workspace`；显式值为绝对路径 |
| `tty` / `stdin_open` | tty 默认 false；stdin_open 在 tty=true 时默认 true，否则 false；tty=true 且 stdin_open=false 拒绝 |
| `yield_time_ms` | 默认 5000，范围 0–20000；只限制本次等待，不等于命令超时 |
| `max_output_bytes` | 默认 16384，范围 1024–65536；限制本次输出文本的 UTF-8 字节数 |
| `max_runtime_ms` | 默认 600000，范围 1000–86400000，另受可信启动策略上限约束；poll / write 不延长截止时间 |
| `bash_write.text` | 最多 16384 UTF-8 字节、无 NUL；关闭普通 stdin 与 PTY 控制字符按原实现分别处理 |
| `bash_terminate.mode` | 默认 graceful；TERM 后按 5 秒宽限期升级 KILL；force 还受启动策略限制 |
| `bash_list.limit` | 默认 50，范围 1–200；只查询当前 Session |
| 本地保留策略 | 同一管理实例最多 4 个尚未确认进程组及输出均收尾的记录，未决记录仍占用额度；单进程输出预算 64 MiB，退出且完成收尾后的输出保留基线为 24 小时 |

运行时限制由平台默认环境配置管理，不增加 Agent / Session 的特殊 Sandbox 参数。

## 4. 进程状态、结果与持久化

进程状态采用 `starting / running / terminating / exited / lost`。正常启动后从 starting 到 running，再自然退出或经 terminating 到 exited；无法核实运行现场时记录 lost。lost 不是“肯定没执行”，exited 也不能代替对子进程组和输出收尾的确认。

- 进程快照保留 `process_id`、`state`、`exit_code`、`termination_signal`、`deadline_at`、`group_quiescent`，未知字段按原契约为 null，不推测退出结果。
- start / poll 的输出页包含 `chunks`、`next_cursor`、`first_available_cursor`、`has_more`、`output_complete`、`output_truncated`；区分 stdout、stderr、pty，按 UTF-8 字节预算分页，保留编码替换标记。
- write 返回 `input_state=accepted/partial/indeterminate` 及 `accepted_bytes`；结果未知时该值为 null，不能自动重发原文本。
- terminate 返回请求已受理及当前证据，不把“已发信号”当成进程已退出。poll 的取消只停止观察，不终止原命令；终止必须走明确的停止路径。
- 工具执行失败使用标准 MCP 错误结果及稳定错误码；running 是有效结果。非零退出、命令超时、输出截断、游标过期和管理状态未知按原契约区分，不统一写成平台 HTTP 500。

复用归档的 SQLite `processes.sqlite`，保存进程、操作阶段和结果、输出位置及活动修订。Bash 操作保留 `prepared → effect_pending → complete` 的核对信息。状态目录与工作目录分开，保持当前账号拥有的 0700/0600 权限。FileWorker 与命令继承 Runner 的 UID/GID，不做 Sandbox 内账号隔离；目录权限不阻止同账号 Bash 访问管理状态。

SQLite 只保存 builtin 的本地执行事实，不替代 PostgreSQL 的 Session / Input / Turn、Pi 原生工具事件或 Pi 原生历史，也不由 event-hub 的 PostgreSQL 迁移器维护。保留原独占访问、存储身份及完整性检查；确认已有记录却缺库、损坏或来源不符时明确失败，不创建空库假装第一次执行。

重启后核对原进程身份和现场，不仅凭 PID 发送信号，不因未收到 start 响应重启同一命令。进程输出有界保留，不承诺永久查询；清理遵守原保留条件，不提前删掉尚在运行或仍未决的证据。环境销毁后不宣称本地进程账本仍可查询，正式可观察历史以 Engine 已提交的事件为准。

## 5. HTTP 入口与可信调用上下文

**2026-11-16 目标设计更新**：MCP 工具 Token 在 Sandbox 首次授权绑定时生成并持久保存，换 Turn、Pi/builtin/Runner 重启不轮换，环境销毁后失效；同 Session/同授权范围的 builtin 和 Runner MCP 代理可共用。管理和配置领取凭据独立。共享 Sandbox 中不同执行 Session/授权范围分别绑定固定工具凭据及活动账本，不能通过请求自填 Session 切换身份。现有单绑定 HTTP 实现仍强制切轮换 Token，及其私有 OpenAPI、测试是旧实现契约，需在后续实现中一起适配；本轮未修改工具代码，历史验收不覆盖这项新设计。

归档 `main.ts` 使用 stdio，Bash 上下文由旧项目的可信对端经私有 `_meta` 提供。当前架构中 Pi 直接调用 HTTP MCP，未修改的 Pi 不保证发送旧项目元数据，因此不能把原启动入口直接上线。

采用一个薄的 **Streamable HTTP 入口适配器**，继续调用相同的工具注册、FileWorker 和 ProcessManager。Runner 启动服务、完成管理同步与实际可用性检查后公布 `<builtin-mcp-base>/mcp`；Engine 将该地址及专用访问凭据写入 Pi 原生配置。可保留 stdio 入口用于原测试及本地调试，但部署拓扑仍是 Pi 直连 builtin HTTP，不经 Runner 的 stdio Gateway。

| 上下文 | 当前项目来源 |
| --- | --- |
| `workspace_id / session_id / sandbox_instance_id` | Runner 的可信启动配置，与 MCP 访问凭据共同核验 |
| `assignment_id / generation / harness_instance_id` | Engine 经 Runner 管理链路绑定的当前执行身份 |
| `turn_id` | 管理链路绑定的当前执行窗口；在调用被准入时固定，不能执行完再取“当前轮” |
| `operation_id` | builtin 入口为被准入调用建立的内部操作身份，交给原管理器 |
| `trace_id` | 可信追踪上下文或入口生成；不作为业务去重键 |
| `runner_boot_id / config_revision / control_revision` | Runner 当前实例及已应用管理修订，用于拒绝旧身份与迟到控制 |

模型提供的参数或 `_meta` 不能覆盖可信身份。内部可映射归档的 `environment_instance_id`、`executor_boot_id`、`runtime_id`、`origin_turn_id` 到当前项目术语，不引入另一套业务资源。资源 ID 改用本项目 UUID；同步修改旧 ULID 正则、生成器、schema 和测试，游标保持不透明。进程句柄同样使用 UUID，不将系统 PID 作为可管理身份。

**调用身份不等于跨重试去重。** 复用内部 operation 记录，能核对已登记的同一次操作，不等于未经修改的 Pi 会跨网络重试携带稳定业务 key。MCP 连接内请求编号、Pi 原生 toolCallId、[公开事件](../../backend/docs/session-events.md)的 tool_use_id 和本地 operation_id 分别关联，不能直接互换；新请求不得只按参数相同判为重复。适配层不自动重放结果未知的副作用；有句柄时使用 poll / list 核对，没有可靠关联时明确保留未知。六个文件工具也不因为接入 HTTP 就获得“恰好一次”保证。

这里的内部 operation_id 不增加公开 API 参数、control_id 或统一幂等 key；平台公开接口仍只在 Input 提交上做专门去重。

## 6. Runner 管理、活动核对与停止

原归档的私有活动方法依赖“只有可信启动者持有 stdio 管道”。HTTP 接入后，Runner 与 builtin 使用独立本地 Unix socket，权限排除其他系统账号；同 UID 的 Bash 不在该账号权限边界之外。普通 MCP 端点不开放门禁修改、活动管理方法或凭据查询。管理同步沿用 Runner 的 `control_revision`，不照搬旧项目的公开/内部控制资源模型。

首次完成来源和门禁同步前拒绝工具执行。更新核验实例、启动身份和单调修订；低修订以及同修订不同内容均拒绝。Runner 已确定的执行窗口及停止边界同步到 builtin，由 HTTP 入口在准入时绑定；切换窗口前排空前轮请求，不能把迟到请求重新归到下一轮。

复用完整账本的活动核对，保留以下证据：

| 字段 | 含义 |
| --- | --- |
| `revision` | 持久活动修订，忙过又归零也会改变 |
| `verified` | 当前证据是否完整可信；false 时即使各计数为零也不表示空闲 |
| `active` | 已准入启动、运行及终止中的进程组 |
| `unknown` | 不能确认存活、结果或清理的执行，包括取消后的文件操作不确定尾部 |
| `pending_writes` | 沿用原字段名，包含 stdin、输出持久化及所有在途 FileWorker 调用，包含读操作 |

管理检查不能用有分页上限的 bash_list 代替。Runner 收到响应后再次核对本地身份、修订和子进程存活，避免旧采样覆盖新控制。通过这种完整快照给 Engine 提供 builtin 的停止/回收证据，不要求 Engine 转发每次工具调用。

一次 bash_start 返回 running 表示 MCP 调用已返回、命令仍在运行。命令归执行 Session，origin_turn_id 保留创建来源；后续 Turn 可以 poll/write/terminate。正常 Turn 在本轮请求排空、结果可判定后可以完成，不必等已确认后台进程退出。换轮允许同一运行实例保留后台输出流，不能把 pending_writes 中的后台输出一律当成未结束工具 RPC。与[Engine 收尾协议](../../agent-engine/docs/session-runtime.md#7-停止撤权与回收)衔接时分别核对这些事实，不清空活动记录，也不新增并行 Turn 管理后台命令。

执行 Session 的 stop / 归档先关闭该 Session 新执行入口，再终止其受管进程组、排空文件调用和输出，取得该范围 `verified=true` 且无 active、unknown、pending_writes 的证据后才报告收尾；没有活动 Turn 也要执行。正常 Turn 完成不关闭这些后台进程，环境回收则核对所有参与 Session，不能因一个 Session 停止误杀其他任务。Runner 的停止适配经私有管理通道驱动进程组终止，不依赖模型主动逐项调用工具。归档的 ProcessManager.close 会保留 lost 并断开管理，不等于终止所有子进程，不能把调用 close 当作完整停止实现。

环境回收由 Backend 按[Conversation 生命周期](../../docs/architecture/conversation-lifecycle.md#三层回收)裁决，管理 Engine 协调 Runner 执行。活跃业务会话不自动空闲回收，单个任务完成/归档不退出整个共享 MCP 服务。父会话归档且全部参与者停稳后才关闭运行资源，工作目录和私有账本先保留；builtin 不自行推断业务归档或删除文件。这些共享控制仍属目标设计，当前单 Session 实现待适配。

无法确认则保持未决并阻塞不安全的后续执行；关闭连接或终止 TS 主进程本身都不是子进程已停止的证明。远程 MCP 和第三方插件仍按各自可观察能力核对，不能套用 builtin 的本地账本保证。

TS supervisor 在运行 Bash 前先报告 ready；builtin 持久化监督身份后发送 start，supervisor 用 Node spawn 事件报告启动成功/失败和实际退出结果。记录失败、放行前取消不启动 Bash，放行后回执丢失保留未知且不重放。省去原 SIGSTOP / SIGCONT 与专用 exec 失败文件描述符。

每条命令的 supervisor 与 Bash 属于独立受管进程组。TERM 后 supervisor 保持运行，按固定 5 秒宽限期升级；强制收尾先通过直接子进程句柄取得 Bash 的实际退出结果，再向自身进程组发送 KILL，包括终止 supervisor 本身。不能取得实际结果时不伪造退出码。管理连接断开也触发该收尾流程。

不再使用 pidfd 或遍历旧 PID 精确回收。supervisor 异常死亡后保持 lost / unknown，交 Runner 核对或环境回收；进程组能力覆盖普通同组后代，不保证处理主动 setsid 等脱离组的程序。容器销毁可作为环境级兜底，裸目录不能以清空账本宣告停稳。

## 7. 打包与目录示意

独立包已放在 Runner 组件下，具体文件和构建入口见[模块 README](../builtin-mcp/README.md)。主要布局如下：

```text
sandbox-runner/
  README.md
  docs/
    runtime-and-mcp.md
    builtin-mcp.md
  builtin-mcp/                    # 独立 TS 包，随 Runner 发布
    package.json
    src/
      process/                    # 复用 Bash 管理
      ...                         # 文件包装、HTTP/stdio 入口及管理适配
    vendor/pi-tools/              # 固定版本 Pi 源码、辅助代码及许可
    contracts/                    # 工具 schema 源及生成配置
    tests/                        # 迁移原测试及新增接入用例
```

该包独立编译、类型检查和测试，安装包使用 `builtin-mcp` 等中性名称，不带产品展示名。依赖锁定 Node、TypeScript、MCP SDK、node:sqlite、rg、fd 和 node-pty；自有 C 辅助程序及编译步骤已删除。PTY 的第三方原生依赖仍存在，没有预构建包时需相应构建工具。

五个 Bash 工具的目标运行平台为 Mac/Linux，继承 Runner 账号，不要求 root 或独立用户。现有完整验收只有 Linux；后续适配通用子进程/进程组及薄平台差异，基本运行不强制依赖 /proc。普通命令默认 pipe，PTY 能力按需加载并单独报告/验收，现有打包依赖还需对应调整。两平台分别验收，不能互相替代；不增加 Session 的 Sandbox 配置项。

MCP 工具契约不使用平台 REST 的 OpenAPI 外形。六个 Pi 工具以固定源码定义生成目录并与上游工厂对照；五个 Bash 工具采用归档 JSON Schema，适配 UUID、路径默认值和项目命名，运行时直接校验同一份 schema。原归档未提供 TypeSpec 生成器，本次将 TypeSpec 保留为 references 来源参考，不将其作为第二份可编辑契约。私有 Unix socket 管理 HTTP 有独立 OpenAPI；Runner 对 Engine 的管理 HTTP 仍采用既有约定。

## 8. 移植与验收顺序

1. 固定归档和 Pi 源码基线，记录文件清单、许可、摘要与本地适配差异，建立本项目依赖锁。先迁移已有测试，再验证移植结果。
2. 复用五个 Bash 工具、文件保护和状态存储，启动/停止改用 TS supervisor；取得六个 Pi 文件工具，验证 1.5.2 的参数、包装和依赖兼容性。
3. 增加 HTTP 与私有管理适配，切换身份格式和真实工作目录；HTTP、工具核心及原 stdio 测试共用同一注册定义。
4. 在 Mac/Linux 普通账号分别验证身份继承、启动握手、放行前取消、pipe、非零退出、长命令、stdin 部分写入及未知结果、超时与 TERM/KILL、断连收尾、UTF-8 分页、截断及过期游标；PTY 单独核验，旧 PID 不参与恢复后的信号发送。增加中文/空格路径、动态端口、稳定状态目录及依赖缺失诊断。
5. 验证文件工具的匹配和编辑错误、大小限制、特殊文件、私有目录保护、取消后的未知写入和 SQLite 损坏/丢失。旧记录不能被空状态覆盖。
6. 联调未修改的 Pi：tools/list 恰好包含这 11 个 builtin 工具；能通过 HTTP 读写 Sandbox 文件及运行命令，原始工具名称与 Harness 展示名不混淆；Skill 自动和显式加载均可用。
7. 验证身份伪造、跨 Session 句柄、旧执行窗口、管理接口越权和停止竞态；活动采样必须覆盖完整账本。HTTP 断线不能重启命令或重发未知 stdin。
8. 验证注册失败和不兼容协议明确报告。归档 SDK / 协议版本与候选 Pi 的实际协商需要联调，不因文档或单元测试存在就认定全链路已打通。

独立模块已经实现并完成基本验收，精确依赖见模块 lockfile。当前验收为真实 MCP 客户端到 builtin 的链路；Pi 的模型执行及 Go Runner / backend 编排需在后续组件实现时联调，不据此宣称整个平台已打通。
