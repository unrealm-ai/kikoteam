# 三渠道文本适配与回执

状态：2026-11-18 用户批准迁移，**实施中**。范围为飞书、钉钉、企业微信文本收发及对应可信身份、队列和回执适配；下文明确区分已落地代码、建议接口与待验收范围。真实渠道验收须独立记录，不能以 mock 或配置保存代替。

图片和文件已接入会话项目上传、任务发布和回报投递。具体接口见[会话文件](conversation-files.md)。业务定时、Webhook、人工确认端到端留后续，不因为适配器预留能力而宣称已支持。本次按单 EventHub 工作模式设计，不新增多节点连接归属、连接路由或跨节点发送协议；既有选举基础设施不构成本次扩展范围。

## 分层

- Platform 保存带 provider 的版本化配置、加密凭据、可信身份和能力状态；各渠道保留各自凭据结构，不能都硬套 AppID/AppSecret。凭据泛化必须兼容既有飞书密文。配置与目录客户端不负责业务消息受理。
- EventHub 当前在 `internal/channel/adapter.go` 通过 `RunProvider` / `sendProviderDelivery` 显式按 provider 分派连接和发送；历史等能力沿对应 worker 适配，负责开启连接、规范化文本事件、发送、读取渠道支持的历史以及生命周期协议。连接相关发送允许委托同一 session；不要求所有渠道都有独立无状态 HTTP sender。
- Manager 共用持久 Inbox、去重、当前业务授权、Conversation/Session 映射、Input、历史边界和 Delivery。适配器不自行创建 Input 或 Task，不从外部事件指定业务授权。
- Engine 继续消费原生 Pi Input，不增加渠道专属模型循环或 Node 定制。

后续可整理的接口边界（伪接口，非当前 Go 接口；当前实现是显式 provider 分派，不宣称已交付通用注册表）：

```go
type Adapter interface {
    Open(ctx, binding, eventSink) (ChannelSession, error)
}
type ChannelSession interface {
    Send(ctx, authorizedDelivery) (SendResult, error)
    Close() error
}
type HistoryAdapter interface {
    Read(ctx, authorizedQuery) (HistoryPage, error)
}
// EventSink.Accept 返回 Manager 持久受理结果，不等于 provider ACK。
```

完整 Capabilities 模型是建议目标，当前通过 provider 分支与实际配置校验限制能力，尚未实现下述统一状态接口。目标分别表达私聊事件、机器人定向事件、可信 mention、普通群消息、主动发送、回复、群历史、话题历史、目录快照、联系人搜索等能力。每项区分 supported 与当前 available/authorization_required/temporarily_unavailable/unknown/unsupported；额外描述入站 ACK 模式、历史 ID 性质/顺序/窗口和发送幂等能力。能力支持不替代用户授权；不支持的能力明确拒绝，不能回退其他群或身份。

## 身份与队列

**最新配置目标（新平台、新数据库）**：业务规则归 AI 员工级，允许选择不同渠道的精确身份名单；渠道连接/凭据仍独立。当前绑定级名单是待改造实现，不是目标语义；本轮不要求历史配置兼容。关键词仅飞书与 Web 群开放。统一规则及能力限制见[专项决定](../../docs/product/identity-selection-and-group-sharing.md#ai-员工级统一配置最新决定取代旧绑定级名单目标)，不在本适配文档另定群权限。

provider 从可信 binding 确定，事件不自行选择员工或租户。人员、群、消息和历史游标始终绑定 workspace、provider、binding、identity epoch、tenant namespace/ID 及相应原生身份命名空间；同名或同样字符串不合并。配置代次改变后旧 worker/事件/发送仍需复验。

复用现有每 binding/epoch/tenant/chat/thread 的 Inbox 串行与不同会话并行；去重采用渠道真实稳定事件/消息身份，不能重试时生成新身份。某 provider 或能力尚未实现时，不得无限领取该类工作再等租约到期，阻塞其他渠道。群触发按该渠道实际能观察的事件启用，不能由历史可读推导普通群消息或关键词监听可用。

## 富文本入站

| 渠道 | 正文 | 路由依据 |
| --- | --- | --- |
| 飞书 | `post` 保留标题、段落、文本样式、Markdown、链接、提及和代码。实时、近期历史和共享历史共用解析器。历史文本保留图片占位，当前消息另按顺序提取附件引用。 | 回调的结构化 mentions 或配置的正文触发规则。[飞书消息内容](https://open.feishu.cn/document/server-docs/im-v1/message-content-description/message_content) |
| 钉钉 | `richText` 按回调顺序提取 `content.richText[].text`，保留换行，图片和文件子项单独进入附件列表。 | Stream 回调身份及群消息的 `isInAtList`。[钉钉机器人接收类型](https://open-dingtalk.github.io/developerpedia/docs/learn/bot/message/) |
| 企微 | `mixed` 按顺序提取 `mixed.msg_item[].text.content`。`text` 和 `mixed` 回调可携带已转为文字的引用，支持文本及语音转写。 | 智能机器人定向回调及当前绑定身份。[企微官方 SDK 消息类型](https://github.com/WecomTeam/aibot-node-sdk/blob/main/src/types/message.ts) |

当前消息有文字或可解析的附件即可进入 Inbox。Event Hub 加密资源定位信息，按 Manager 核准的原消息和附件序号下载，完成后清除定位信息。企微可选的 `quoted_text` 随 Inbox 元数据保存，写 Input 时加上用户引用标记。群历史作业仍从独立的 `text` 读取当前消息正文，联系人验证仍只接受纯文字私聊。新 Hub 开启渠道入站前，Manager 必须已能接收新增消息类型和字段。

## 各渠道与企微协议边界

飞书保留原生消息 ID、可信 mention 与 thread 历史机制。钉钉 Stream 入站、主动发送授权及身份验证分别处理；若后续接 HTTP 回调，不能把仅覆盖时间戳的签名当成 body 完整性保护，本次不扩展 Webhook 范围。

企微按智能机器人长连接机制实现，不冒充飞书协议：

- 同一个机器人 SDK session 收发；入站不宣称具有持久 ACK API。
- 机器人 app/bot 作用域与企业 corpid 区分；可选 corpid 元数据不能自动确认为租户身份。
- 回复依赖原 callback req_id 与消息/目标关联。未知结果的关联须隔离，迟到 ACK 不得确认后续发送；断线后不可复用旧临时关联。
- 历史使用最近授权时间窗口、北京时间秒级时间和倒序分页；query-local 历史记录 ID 不是 provider messageID，保留重复文本，不强行套飞书精确消息 ID 锚点。Kiko 已落地的具体策略见下节；无法证明时 unavailable，不猜测跨界去重。
- 不因定向机器人事件或群历史可用而宣称支持普通群监听、可信 mention 事件或话题读取。

### 企微群历史的当前实现

Hub `wecom_history.go` 经固定官方 CLI messages 服务读取授权近期群历史，并非仅本地消息缓存。策略名为 `wecom_recent`，不假装与飞书 `since_last_trigger` 相同：北京时间秒级时间，最近七天窗口（预留一分钟边界），整体五秒预算，最多八页。当前消息以完整文本相同、同一可识别 @ 名称及时间差 ±5 秒定位，锚点必须唯一且覆盖该时间窗口；多个候选或缺锚点均 unavailable。

从锚点之前向旧消息找上一条 @ 同名机器人的记录，或达到配置条数/读取耗尽为止；锚点与该上一条 @ 记录不加入历史，输出恢复时间顺序。此为基于文本锚点的截取策略，不是按授权人过滤，也不是用上一条 Manager Input 的原生消息 ID 定位。文本 @ 只用于历史截取，不据此确认用户或授予使用权。

企微 `mixed` 回调缺少可用于现有历史匹配的正文记录，Manager 直接记录 `wecom_mixed_history_anchor_unavailable` 并处理当前事件。该 Input 仍更新 Manager 的边界；后续文本历史查询仍采用同名 @ 截取。

记录使用本次快照内 `page:record` 类型的 `record_key`，不伪造 provider message ID，不跨请求以该键去重，重复正文保留。Manager 接受未知历史作者证据、不把历史 sender 当成可信实时授权主体；快照和当前事件仍按现有事务逐条写 Input。最多 100 条、32768 字符，Hub 另限 128 KiB 文本。私聊/话题不走此群历史策略；失败只处理当前事件，不借其他群/话题历史。相关 mock/PG 测试状态待本轮最终报告，未做真实企微账号验收。

## 连接展示与内部身份

用户侧连接正常显示「已连接」。内部仍分别保存/判断配置已保存、协议连接状态、可信身份确认、能力授权和真实收发结果；不得把「已连接」改写成所有身份/授权/收发均已验收。身份尚待确认时仍须阻止缺少可信主体的业务准入，不借展示状态绕过校验；已落地字段包括 `identity_confirmed`、`identity_verified_generation`、`tenant_namespace`、`user_namespace`。钉钉为 corp_id/user_id，企微为 app_id/user_id（bot 作用域，不是企业 corpid）；新凭据代次须由可信首事件重新确认。

## 投递与回执

所有渠道复用 Manager claim → authorize → adapter send → receipt。授权结果不得替换原领取目标、正文、操作身份或代次。受理、提供方接受、明确失败与结果 unknown 分别记录；不把一次网络成功当成用户已收到，也不把断线当成明确未发送。只有渠道实际支持的幂等/状态查询才能作为重试依据；unknown 不盲重发。

### 飞书与钉钉富文本发送

飞书和钉钉的主会话成功回复，无论由用户消息还是后台任务回报触发，只要有正文，Manager 都按同一规则保存 `format=markdown`。正文含 Markdown 图片标记（`![`）或 HTML 图片标签（`<img`，不区分大小写）时，整条按普通文本发送；失败、取消和无正文时的固定提示仍用 `text`。任务回报仍关联最初委派消息的 Inbox；已入队的投递不改格式、不重发。Hub 对同一已授权投递的目标、正文和格式执行一致性检查。

- 飞书使用现有 HTTP 创建或回复接口发送 `post` 的 `md` 节点。话题回复继续传 `reply_in_thread`，两种格式继续使用既有发送 UUID。[飞书发送结构](https://open.feishu.cn/document/server-docs/im-v1/message-content-description/create_json?lang=zh-CN)
- 钉钉普通文字回复优先使用原入站的临时会话入口，详见[行为基线](../../docs/spec/dingtalk-session-reply.md)。主动路径仍用 HTTP 单聊或群发接口，Markdown 用 `sampleMarkdown`，普通文本用 `sampleText`，按 `processQueryKey` 查询发送状态。[钉钉发送结构](https://dingtalk.apifox.cn/doc-3550110)
- Markdown 保留 Unicode 表情及组合字符，例如 `🎉`、`👩🏽‍💻`。飞书原生 `emotion` 节点仍在入站解析范围内。自定义表情包和贴纸留在后续范围。原生图片通过选中的产物独立投递。

迁移 59 由 Hub 执行，旧投递保留原正文、状态和 `text` 格式。合并发布富文本收发时，可以先让新 Hub 在未配置 `EVENT_HUB_BACKEND_URL` 的模式下当选并执行迁移，再升级 Manager，最后启用新 Hub 的渠道工作。旧 Hub 仍能领取普通文本，Markdown 仅由声明支持新格式的 Hub 领取。降级应用时保留数据库列和已记录格式。

**迁移 28 已落地，最终集成回归中**：状态为 pending → leased → sending → accepted/sent/failed/unknown，并保留 cancelled 授权终态。飞书确认 sent 需要供应商消息 ID，话题回复还须核对回执中的会话和话题 ID；钉钉主动路径的 processQueryKey 仅记 accepted，之后按 2/5/10/20/30 秒最多五次且五分钟预算查询原请求，SUCCESS 才 sent，耗尽为 unknown；企微同 session 匹配业务 ACK 才 sent，不伪造消息 ID。

迟到可信回执必须匹配原 attempt token、规范目标 digest 与 provider 证据，才可补齐 unknown；钉钉临时会话回复不支持补认 unknown。Hub 最多三次重试同一回执提交，不重发供应商请求。撤权后可审计真实已发生发送事实，但不能新发送；授权结论与 transport 事实分开。provider 返回与回执持久化之间崩溃仍可能 unknown，不能宣称已完全消除不确定结果。

### 升级迁移 28

建议切换前停止新派发并等待在途发送收尾，再整体切换匹配的 Manager/Hub；不滚动混用缺少 attempt digest 的旧 Hub 回执。升级前已处于 sending 的旧尝试没有 digest，不能绕过新校验补认成功，可能只能保守保留 unknown，且不得自动重发。不承诺恢复所有旧飞书迟到回执；迁移保留证据，不通过回滚抹除审计事实。

## 实施核对与验收

当前已确认的装配：`app/server.go` 注入钉钉注册 provider 与企微联系人搜索 provider；Hub 三 provider 连接/发送分派已落地。钉钉注册使用迁移 32，真实 UI 与 `/registration`、`poll`、`confirm`、`cancel` 契约对齐；ready 后明确确认才保存新绑定，默认空白名单，不覆盖手动配置，consumed 不表示连接已成功。企微搜索只返回候选，管理员转交码与可信私聊回调建立独立映射，显式保存 ACL 才配置使用范围，不按名字或搜索 ID 自动授权。

钉钉目录、注册客户端和企微搜索客户端 mock/race 与局部 lint 已通过；钉钉注册真实隔离 PG/race 生命周期、HTTP、取消/换配竞态/过期/撤权等回归已通过，OpenAPI 校验通过。前端组件及整体后端/Hub 最终集成测试由对应组件分别记录，不据局部通过宣称全量完成，三渠道真实租户收发尚未验收。

继续核验：飞书旧配置/密文兼容；三渠道相同 user/chat/message ID 不串线；重复事件仅一次 Input；旧代次与撤权拒绝；单渠道失败不阻塞其他队列；企微迟到回执及历史锚点；不支持的话题不错误降级；前端已连接与内部身份状态独立。统一群权限尚待用户决定，本页仅描述现有适配事实，不新增群授权、默认公开或跨渠道共享规则。

### 企微已验证联系人姓名与关键词搜索

当前群回调的 `user_id` 仅在同一机器人绑定、身份 epoch、连接 generation 和可信 tenant 内已有管理员转交验证映射时，才可用对应搜索联系人姓名展示说话人。展示不会替换 callback ID 或增加使用权限；私聊不增加说话人前缀。CLI 群历史仍按提供方姓名作为未知身份上下文，不用历史搜索 ID 反推可信 callback 身份。

未验证搜索候选仍为 10 分钟缓存；已有当前验证映射关联的姓名档案不因候选缓存到期或临时缓存容量清理而丢失。保留不刷新过期时间，不能凭过期档案重新发起验证；同一绑定轮换 Secret 或重新启用后，首条可信入站重新确认同一 BotID 时，迁移 76 的数据库触发器延续已验证映射和原姓名。不同 BotID 或身份 epoch 各自独立。已经被旧版本清除的档案需重新搜索恢复，不伪造历史姓名。

有关键词时，在提供方搜索成功后合并当前已验证本地档案的姓名、搜索 ID、部门路径匹配结果，按搜索 ID 去重排序，提供方新结果覆盖旧展示，最多返回 100 条并通过 `limited` 明示截断。提供方失败仍返回失败，不以本地命中伪装远程成功；不开放空关键词浏览、部门树或完整组织目录。所有选择和使用仍复验当前映射与权限。
