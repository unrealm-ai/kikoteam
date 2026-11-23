# 钉钉普通回复与主动投递

| 编号 | 可观察行为 |
| --- | --- |
| DING-REPLY-01 | 钉钉私聊和群聊的普通文本/Markdown 回复（包括该轮的失败、取消、无正文等结果提示），Manager 明确授权该发送方式且原消息的临时入口仍有效时，优先由 Event Hub 发回原会话。入口只来自与该入站消息对应的钉钉 Stream 回调；不取正文链接，也不借用同会话的其他消息。 |
| DING-REPLY-02 | 找不到符合条件且未过期的原入口时，且尚未发起 Webhook 请求，沿用既有主动投递。定时、Hook、人工确认、后台任务回告、附件和其他投递类型始终沿用原方式。Webhook 一旦尝试，不因超时、结果不明或明确业务拒绝改走另一条路径。 |
| DING-REPLY-03 | Webhook 仅在 HTTP 成功且钉钉明确返回 `errcode=0` 时确认。回执关联原入站消息和当前投递尝试，不产生出站消息 ID 或主动投递查询标识，也不表示已读。首个成功回执须仍在发送租约内；相同回执可幂等确认，迟到回执不把 `unknown` 改为成功。主动投递仍按原 `processQueryKey` 核对。 |

临时 URL 仅在 Event Hub 内存保存，不随入站上传 Backend，不写数据库、日志或模型上下文。缓存限定绑定、代次、配置版本、规则版本、租户、机器人、会话类型、会话及原消息；私聊再比对发送人。最大 2048 项，有效期取供应商期限与本地两小时上限的较早值。重放不续期，Hub 接管不继承，目标使用固定官方 HTTPS 路径且不跟随重定向。收到 Backend 持久入站受理后才记录，由现有 outbox 授权流程发送。

部署时先更新全部 Backend，再更新 Event Hub；旧 Hub 不消费新授权提示。只看到旧 Backend 给的原消息 ID 不会启用临时入口。新回执仅由新版 Backend 解释。

依据：[渠道实现](../../event-hub/internal/channel/dingtalk_session_reply.go)、[Manager 授权](../../backend/internal/manager/repository/channel_delivery.go)、[回执约束](../../backend/internal/manager/repository/channel_delivery_receipt.go)、[钉钉官方说明](https://open-dingtalk.github.io/developerpedia/docs/learn/bot/appbot/reply/)。

回归：[Event Hub](../../event-hub/internal/channel/dingtalk_session_reply_test.go) 验证原消息/目标匹配、过期和主动降级、URL 与重定向限制、HTTP 和业务回执及新旧授权提示；[Backend](../../backend/internal/platform/repository/dingtalk_session_reply_test.go) 验证持久回执与租约、错误锚点、主动链路及重复回执。本轮不接真实钉钉账号。
