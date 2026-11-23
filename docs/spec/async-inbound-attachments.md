# 入站附件异步准备与按需读取

适用于 Web 私聊，以及经现有触发和使用权限受理的飞书、钉钉、企微消息。Web 群不新增附件入口；不改变群历史、群共享或产物发布和发送。

| 编号 | 可观察行为 |
| --- | --- |
| ATT-01 | 有正文或有效附件引用的授权消息可先进入 Input，纯附件也可提交。附件上传或下载未完成、重试或失败，不再作为主对话受理和处理下一条消息的前置条件；仍遵循原有会话队列与触发规则。仅更新附件状态不会增写 Input、唤醒员工或重复回复。 |
| ATT-02 | 渠道附件按原消息和序号生成稳定引用；Web 私聊先预留附件 ID。Input 记录名称与当时状态的有序快照；Web 可保存声明的文件大小。主对话只看到用户上传了文件、文件名（有才显示）及供委派使用的引用 ID；不接收下载状态、不自动读取原文，也不把图片自动送给主对话模型。用 `[附件 名称="文件名" attachment_id=引用ID]` 表示，每个附件一行，缺名时省略名称。缺名附件仍可委派，且存储兜底名不作为发送者文件名注入主对话。传统项目文件仍按路径引用。 |
| ATT-03 | 主对话可将用户已引用、与当前来源主体相符的附件 ID 显式交给同会话任务（创建、补充或待办）。任务用 Manager MCP 的 `attachment_get` 查询实时状态：`downloading` 与 `failed` 不返回路径；`ready` 仅在目标文件仍可打开时返回项目相对路径。工具只检查状态和本地文件，不等待或发起下载，不读取文件内容。 |
| ATT-04 | Web 草稿在取得 ID 前不能带该文件发送；取得 ID 后不等上传完成。页面区分上传进度、已就绪、失败；上传结果不明时可按原 ID 查询，已终止的上传须重新添加文件。已发送的历史消息没有路径时可主动查询当前状态，就绪后才按现有权限下载。没有连续的服务端进度推送。发布前重新校验成员、员工、会话和预留状态；归档、撤权或过期阻止发布。 |
| ATT-05 | 未经授权的附件 ID 不授予 `attachment_get` 访问。工具要求当前会话任务、有效 Engine 分配及运行轮次，还核对同主体的持久 Input 引用和当前权限；渠道附件继续核对原入站及当前绑定。主对话和没有会话项目的独立任务不装配此查询工具。下载用的 URL、下载码、解密材料和内部凭据不作为消息元数据交给前端或模型。 |
| ATT-06 | 钉钉按官方换链结果的原 HTTP/HTTPS 协议下载，允许从 HTTPS 跳转到 HTTP。企微的初始地址和跳转地址均须使用 HTTPS。钉钉和企微按 DNS 主机名校验地址，无固定域名名单，每次下载最多跟随 5 次重定向。两条链路仅接受协议默认端口。默认仅连接 DNS 返回的公网 IP，平台管理员可在[附件下载设置](platform-service-settings.md#附件下载)中允许连接 DNS 返回的内网 IP。飞书使用固定 API，附件重试预算为三次。 |
| ATT-07 | 内置普通 `read` 在文件开头识别到 PDF 文件头时返回 `pdftotext` 使用提示，不把原始字节当文本。Engine 持久化事件和轮次结果摘要时将实际 NUL 替换为 U+FFFD，保留普通的字面量 `\u0000` 和工具原始输出。 |
| ATT-08 | 飞书、钉钉、企微的入站附件由下载到项目存储统一限制为每件 100 MiB（104857600 字节，含等于上限）；企微以解密后的文件大小为准。超过上限不能发布为就绪。不改变 Web 上传、事项交接、出站附件及读取工具各自的限制。渠道自身限制及既有下载、保存超时也不变。 |
| ATT-09 | 飞书富文本保留原有正文及内联图片；同一条消息可同时接收内联图片、内联文件和根级 `files[]` 文件，也支持正文段落为空但包含有效文件的消息。按原内联顺序后接根数组顺序；同一条消息中同一文件键只生成一条文件引用，根数组中的目录或无效条目不影响正文及其他附件。群触发、单独发送的图片和文件不变。 |
| ATT-10 | `attachment_get` 仍使用现有工具和错误码。输入 ID 无效或不可访问时，仅在当前运行任务的授权通过后，附最多 32 个已授权附件的完整 ID 与名称（超出标记截断）。不返回其他任务附件、路径或供应商凭据，不从候选自动换文件。下载中提示稍后用同一 ID 查询；最终失败或路径不可用时不返回路径、可让用户重发。 |

## 实现和兼容

- [Manager Inbox 与媒体状态](../../backend/internal/manager/repository/channel_media.go) 分开推进。同一消息重放复用原记录；Event Hub 通过两个有界后台下载 worker 消费租约，每附件最多三次领取，完成后按原会话项目保存，已就绪或耗尽尝试时清除加密定位数据。飞书按消息 ID 和资源键取附件，钉钉用下载码换地址，企微解密回调所指的内容；下载失败不改发其他路径。钉钉地址校验拒绝时，Event Hub 仅记录初始地址或跳转阶段、失败原因、可安全显示的主机名及跳数，不记录签名地址、下载码或查询参数；诊断本身不改变放行规则。钉钉换链 API 返回的初始地址必须是绝对 HTTP/HTTPS URL，按原协议、主机、路径和签名请求。钉钉和企微逐跳校验协议、DNS 主机名和端口，下载请求只携带当前地址中的签名，省略机器人凭据和上一跳的 Referer。附件名只当数据使用。
- [Web 预留和上传](../../backend/internal/manager/controller/web_attachments.go) 使用私聊会话下的预留、查询及上传接口；单附件上限 32 MiB，预留最长 10 分钟，同一主体和会话内复用原去重键。迁移 92 只增加此预留表，不改历史附件路径，不给旧记录补造附件 ID。
- [任务查询授权](../../backend/internal/manager/repository/input_attachments.go) 与[工具实现](../../backend/internal/manager/mcp/inputattachments/http.go)独立于旧有项目文件读取。Engine 对普通输入和运行中追加按 ID、可选名称投影附件快照，主对话的任务委派工具回执也只展示引用 ID 和可选名称，旧项目路径仍单独保留；任务查询状态不回写原 Input 快照。由会话公开接口或模型输入看到一个 ID，并不授予跨会话、跨群话题、跨工作区或跨身份读取权限。

## 验证方式

| 契约 | 回归入口 |
| --- | --- |
| ATT-01、02 | [渠道 Inbox/下载回归](../../backend/internal/platform/repository/channel_media_retry_test.go)、[Web 预留回归](../../backend/internal/platform/repository/web_attachment_reservations_test.go)、[Engine Prompt 测试](../../agent-engine/internal/repository/input_attachments_test.go)、[主对话工具返回投影](../../backend/internal/manager/mcp/taskdelegation/attachments_test.go) |
| ATT-03、05 | [Manager MCP 状态回归](../../backend/internal/platform/repository/input_attachment_status_test.go)、[委派授权回归](../../backend/internal/platform/repository/manager_attachment_delegation_test.go) |
| ATT-04 | [Web 组件用例](../../web/test/manager-conversations.test.mjs) |
| ATT-06 | [DNS、TLS 与逐跳防护](../../event-hub/internal/channel/media_download_transport_test.go)、[钉钉签名地址与协议跳转](../../event-hub/internal/channel/media_inbound_ding_url_test.go)、[企微跨域下载与两渠道跳数边界](../../event-hub/internal/channel/media_inbound_protocol_test.go)、[共享重试预算](../../backend/internal/platform/repository/channel_media_retry_test.go) |
| ATT-07 | [真实 stdio MCP 读取回归](../../sandbox-runner/builtin-mcp/tests/stdio.test.mjs)、[隔离 PostgreSQL 事件与摘要回归](../../agent-engine/internal/repository/event_encoding_test.go) |
| ATT-08 | [三渠道体积及企微解密边界](../../event-hub/internal/channel/media_inbound_protocol_test.go)、[Manager 入口至真实项目持久化](../../backend/internal/platform/repository/channel_media_size_test.go)：声明 100 MiB + 1 字节返回 413；恰好 100 MiB 的文件成功落盘。 |
| ATT-09 | [飞书入站消息夹具](../../event-hub/internal/channel/media_inbound_feishu_post_test.go)：正文、内联图片、多份文件、语言和 `content_v2` 选择、空正文、去重、目录及畸形元数据、群触发和原消息资源身份。 |
| ATT-10 | [工具结果测试](../../backend/internal/manager/mcp/inputattachments/http_test.go)、[授权数据回归](../../backend/internal/platform/repository/input_attachment_status_test.go)：候选投影及状态引导，错 ID 和失效身份的授权边界。 |
| 厂商协议和后台限制 | [Event Hub 夹具](../../event-hub/internal/channel/media_inbound_protocol_test.go)，覆盖钉钉下载码、企微文件名、错误归类及独立 worker 限制 |

已通过隔离 PostgreSQL 和[实际 Engine/Pi 的附件端到端用例](../../backend/internal/platform/repository/async_attachment_runtime_test.go)：附件未传输时状态无路径，后续文字可以应答；上传完成后同一任务可查询到路径，并用执行工具读取文件内容。真实供应商账号另行验收，夹具及隔离数据库不代表真实渠道收件验证。
