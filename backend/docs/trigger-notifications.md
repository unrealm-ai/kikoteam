# 定时 / Hook 的群通知与上下文记录

状态：2026-11-18，实施中，独立工作区集成验收尚未结束。本文记录已落实现与验证边界，不代表部署完成或真实供应商发送验收。

## 两种来源，不混为任务完成

- 任务执行结束通知对应真实 `source_input_id/source_turn_id`，以及来源 Task。Kiko 当前没有独立的永久业务完成信号；不能把 idle、Turn 结束或渠道送达记成整个 Task/事项成功。通知只发布最新触发输入的已稳定执行结果：无活动 Turn、排队/执行中输入、后台命令、未决工具结果及未完成 stop；文案明确“本次执行已结束（不代表整项任务完成）”。后续补充有独立输入身份。
- 固定定时提醒由独立 `reminder_event_id` 标识，不创建 Task、TaskSpace、执行 Session 或模型轮次。与任务结果共用投递设施，不共用业务完成判据。
- 执行通知只取公开结果摘要并限制长度，过滤 think 段、明显凭据和本地路径；不发布原始输入、完整日志、工具记录、原生 JSONL 或资源凭据。这不是能够识别所有敏感信息的通用 DLP 保证，配置者须对群受众授权负责。

## 持久账本和真实发送事实

迁移 40 保存每 Task 的目标快照、通知、发送尝试与回执；43 增加固定提醒事件来源。结果按 source input+目标会话唯一，固定提醒按 reminder event+目标会话唯一。业务 key 不是发送幂等键，通知失败不改写 Task 的执行事实。

成功执行的结果摘要在脱敏后保留换行、段落和缩进。超过 1000 个 Unicode 码点时优先保留完整行，再附加省略号。每个目标按普通回复的渠道规则入队：有正文时飞书、钉钉使用 Markdown；图片标记触发普通文本回退。空摘要、失败通知、固定提醒和既有通知保留纯文本格式。

迁移 88 将发送格式和正文一起保存在通知记录中。领取、授权、回执查询和飞书历史任务均读取该记录。飞书按原发送格式校验：纯文本逐字比对；Markdown 从富文本的单个 `md` 节点读取原文，优先使用 `content_v2`，并核对消息 ID、发送应用、群、话题及时间。部署时先由新版 Hub 完成迁移，升级全部 Backend 后再启用通知投递与回读，避免新通知被旧进程按纯文本处理。[飞书消息结构](https://open.feishu.cn/document/server-docs/im-v1/message-content-description/create_json?lang=zh-CN)

状态为 `pending/leased/sending/accepted/sent/failed/unknown/cancelled`。领取只获得临时租约；授权时重新核验触发器版本/启用、员工、目标身份及绑定代次。已进入 sending 的超时转 unknown，不重新发外部消息。未发送的 leased 过期可重新领取。发送许可与可信回执是不同事实：撤权后不能新发，但允许记录原尝试确实发生的发送结果。

- 飞书：供应商返回真实 message ID 才记 sent；历史锚点还须真实 create_time/chat/thread，不能用本地时间。
- 钉钉：processQueryKey 只证明 accepted；只查询原请求，SUCCESS 才 sent。2/5/10/20/30 秒、最多五次/五分钟，耗尽 unknown，不重发。
- 企微：匹配当前发送会话/请求的业务 ACK 才 sent；reqid 不是历史 message ID。
- Web：数据库提交单份群消息就是群内可见事实；不假造 IM receipt。

回执绑定原 attempt token、目标/内容摘要及供应商证据。相同回执可重复提交，迟到可信回执可修正 unknown；不能以补交回执重新发送。供应商已接收但进程在落库前退出仍可能留下 unknown，本轮不承诺消除此窗口。

## 目标选择与权限

公开配置使用服务端可验证的目标选择，不接受任意 provider/chat/tenant/凭据作为发送授权。候选/保存/发送均须校验工作区、目标员工、绑定 identity epoch、generation、tenant 和群生命周期；Web 核验员工当前群参与、群与员工参与代次；操作人须具备员工管理权限，不要求其加入目标群，也不据此获得群消息读取权限。展示名不能用于身份匹配。

保存配置以真实员工群 Conversation 为引用；迁移 44 提供可信候选、签名分页游标及不伪造 Inbox 的会话解析，允许未曾触发的已加入群建立系统群 Conversation/chat，不启动模型。旧候选查询保留 truncated 标志；候选解析与发送均须重新校验当前员工群身份。飞书官方 joined-chats provider 已实现主动分页；钉钉/企微候选依各自可信已发现/近期群能力，不能宣称供应商提供不存在的全量目录。

通知发送不要求创建虚假入站消息。Manager 有独立通知账本和 Hub 内部 claim/authorize/complete/query 入口；公开查询只返回任务/输入身份、投递与上下文状态，不暴露租约、原始 receipt 或密钥。

## 发送后先历史、再系统记录

投递状态和上下文状态独立：后者为 `pending/admitted/unavailable/suppressed`。只在可信 sent 后，按对应员工的群会话处理上下文；不主动向其他员工 Inbox/Input 广播。

1. 按该员工上次 Input 边界到本条已发送消息之间的真实群历史，逐条写 false Input。
2. 写当前正文 `[系统消息]…`，任务结果为 `source_kind=task_result`，固定提醒为 `source_kind=system`，均 `trigger_agent_loop=false`。
3. 以上 Input、上下文去重事实、边界推进在同一事务完成。标签只是显示文本，授权来自通知及当前目标身份。

`task_result` 与主 chat 内部任务回报 `task_report` 不同，客户端不可选择这些来源伪造系统权限。无可信锚点、区间顺序不明、边界已越过或目标撤销时，保留真实发送事实，明确 context pending/unavailable/suppressed，不假造历史或提前推进边界。

Web 群消息、历史 Input、结果 Input 和边界在同一事务提交；队列已满时整次发送回滚等待，不先发一条可见消息再遗漏上下文。飞书使用独立通知 history job，不伪造 Inbox；Provider 真实 message 取回及历史校验后再提交。钉钉 query key、企微 ACK 本身不足以作为群历史锚点，不把它们伪装成可推进边界的消息 ID。

## 本轮局部验证

- 通知/Web 联合真实 PostgreSQL race 专项通过（最终局部复跑 5.823 秒），覆盖稳定执行门禁、系统标签和已知敏感格式清理、单份 Web 消息与 admitted、unknown 不重发、迟到回执去重/撤权事实。eval 另覆盖 Web 事务回滚、并发及队列背压。
- 飞书 joined-chats provider mock race（1.622 秒）与 lint 通过：官方 origin、token/tenant_key、分页、游标异常、重复/外租户证据、响应预算、redirect 和错误脱敏。未真实查询外部飞书账号。
- 公开/内部 OpenAPI 校验通过。完整跨模块、前端及真实渠道发送另行验收，不由上述局部结果推断。
