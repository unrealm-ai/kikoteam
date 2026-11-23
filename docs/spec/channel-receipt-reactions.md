# 飞书、钉钉原消息接收反应

**行为 REC-1。** AI 员工收到飞书或钉钉的真人私聊、符合触发规则的群聊消息后，只有当前消息通过去重、权限、附件准备及其他校验并成功写成会触发员工的 Input，才可以在**原消息**上添加一次原生接收反应。飞书显示 OK，钉钉用机器人文字反应显示“已收到”。这是已受理，不表示模型已启动、成功或完成。没有额外回复消息，不撤回反应。

**边界 REC-2。** 群历史、未触发、无权限、控制专用、未受理或重复消息不添加反应；企微和 Web 不变。飞书新扫码应用会请求可选的 `im:message.reactions:write_only`。已有或手动应用不会自动扩权；同一绑定代次的权限诊断须在近 10 分钟内确认该权限或已有的 `im:message`，无结果、结果过期或权限缺失时暂不添加，不影响正常聊天；供应商拒绝也不补发提示。钉钉用应用机器人权限，权限缺失时同样只跳过。观察到权限前已经跳过的消息不会补贴。

**职责 REC-3。** Manager 在当前 Input 的事务内标记资格，不给历史记录回填。Event Hub 在独立 worker 领取两分钟内的待尝试项，领取在对外发送前即消费；发送前重新检查实际渠道绑定、原消息和当前权限。钉钉使用 Stream 回调业务 `msgId`，不用传输层 `messageId`。一次性尝试失败、领取响应丢失、进程退出或队列超时可能漏贴，不重投、不设置模型或普通回复的阻塞条件。错误日志不包含消息正文、凭据、供应商原始报错。

| 验证 | 场景与预期 | 测试依据 |
| --- | --- | --- |
| REC-1 / REC-2 | 回调或附件待准备时没有接收反应；成功提交后并发最多领取一次；无权限、未触发、旧权限证明、企微和重复回调不会外发 | [Manager PostgreSQL 回归](../../backend/internal/platform/repository/manager_channel_reactions_test.go) |
| REC-2 | 新扫码权限属于可选分组；缺少它不阻挡原有必需权限检查 | [飞书配置测试](../../backend/internal/platform/service/feishu_setup_permissions_test.go) |
| REC-3 | 原始消息目标、机器人凭据及协议格式；撤权和领取响应丢失不发送，Provider 拒绝不重试 | [Event Hub HTTP 夹具](../../event-hub/internal/channel/receipt_reactions_test.go)・[内部接口](../../backend/contracts/internal/channel-reactions.yaml) |

钉钉已用本地 SRE 实际账号验证接收反应；飞书实际账号已验证缺少明确授权时消息仍正常处理，实际贴表情待确认授权后验收。
