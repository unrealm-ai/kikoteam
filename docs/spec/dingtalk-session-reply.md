# 钉钉普通回复与主动投递

| 编号 | 可观察行为 |
| --- | --- |
| DING-REPLY-01 | 钉钉私聊和群聊的普通文本/Markdown 回复（包括该轮的失败、取消、无正文等结果提示），Manager 明确授权该发送方式且原消息的临时入口仍有效时，优先由 Event Hub 发回原会话。入口只来自与该入站消息对应的钉钉 Stream 回调；不取正文链接，也不借用同会话的其他消息。 |
| DING-REPLY-02 | 找不到符合条件且未过期的原入口时，且尚未发起 Webhook 请求，沿用既有主动投递。定时、Hook、人工确认、后台任务回告、附件和其他投递类型始终沿用原方式。Webhook 一旦尝试，不因超时、结果不明或明确业务拒绝改走另一条路径。 |
| DING-REPLY-03 | Webhook 仅在 HTTP 成功且钉钉明确返回 `errcode=0` 时确认。回执关联原入站消息和当前投递尝试，不产生出站消息 ID 或主动投递查询标识，也不表示已读。首个成功回执须仍在发送租约内；相同回执可幂等确认，迟到回执不把 `unknown` 改为成功。主动投递仍按原 `processQueryKey` 核对。 |
| DING-REPLY-04 | 当前启用的钉钉绑定在主动发送收到明确的应用权限拒绝后显示「主动发送权限」，私聊和群聊共用。悬停或键盘聚焦可查看「企业内机器人发送消息权限」。目标不可用、限流、上传错误、超时等不触发权限提示。后续获确认的主动发送成功清除提示；仅受理、查询未确认、SessionWebhook 回复不清除。未显示不等于已经确认授权。不预检、不自动发送测试消息。 |

临时 URL 仅在 Event Hub 内存保存，不随入站上传 Backend，不写数据库、日志或模型上下文。缓存限定绑定、代次、配置版本、规则版本、租户、机器人、会话类型、会话及原消息；私聊再比对发送人。最大 2048 项，有效期取供应商期限与本地两小时上限的较早值。重放不续期，Hub 接管不继承，目标使用固定官方 HTTPS 路径且不跟随重定向。收到 Backend 持久入站受理后才记录，由现有 outbox 授权流程发送。

部署时先更新全部 Backend，再更新 Event Hub；旧 Hub 不消费新授权提示。只看到旧 Backend 给的原消息 ID 不会启用临时入口。新回执仅由新版 Backend 解释。

依据：[渠道实现](../../event-hub/internal/channel/dingtalk_session_reply.go)、[Manager 授权](../../backend/internal/manager/repository/channel_delivery.go)、[回执约束](../../backend/internal/manager/repository/channel_delivery_receipt.go)、[钉钉官方说明](https://open-dingtalk.github.io/developerpedia/docs/learn/bot/appbot/reply/)。

回归：[Event Hub](../../event-hub/internal/channel/dingtalk_session_reply_test.go) 验证原消息/目标匹配、过期和主动降级、URL 与重定向限制、HTTP 和业务回执及新旧授权提示；[Backend](../../backend/internal/platform/repository/dingtalk_session_reply_test.go) 验证持久回执与租约、错误锚点、主动链路及重复回执。本轮不接真实钉钉账号。


**权限提示。** [Event Hub](../../event-hub/internal/channel/dingtalk_delivery.go) 仅对主动发送接口返回的明确缺权错误分类，不向渠道页回传供应商响应。普通投递（含任务回报和文件）、人工确认通知和触发器通知复用各自已经授权的回执，经 [Manager](../../backend/internal/manager/repository/dingtalk_proactive_send.go) 更新绑定观测记录。记录按当前绑定代次、原发送授权时间排序，成功也留水位，旧失败迟到不复现提示。不迁回历史投递，不改变重试、回执状态或发消息的方式。

已连接的钉钉渠道详情可见时，页面使用既有只读状态查询每 30 秒同步该提示；临时读取失败保留最近成功读到的值。通讯录权限错误继续独立显示，悬停或聚焦可查看部门成员、成员信息读权限及授权人员/部门范围，并保留钉钉开发者后台入口。

自动化回归：[Manager 三条账本与迟到回执](../../backend/internal/platform/repository/dingtalk_proactive_send_test.go)、[供应商响应分类](../../event-hub/internal/channel/dingtalk_delivery_test.go)、[页面提示与状态刷新](../../web/test/workforce-original-channels.test.mjs)及[状态轮询](../../web/test/workforce-dingtalk-permission-poll.test.mjs)。夹具验证不代表真实钉钉租户已实测撤权。
