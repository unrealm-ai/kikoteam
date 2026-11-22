# 手工渠道凭据验证与换绑

手工飞书、钉钉先验证后保存；企业微信只做格式检查，保存后等待唯一正式连接认证。扫码登记仍由其已授权提供方流程认证，不能把格式检查或 token 验证当成机器人连接/权限全部就绪。

## API 与裁决点

- `POST /api/v1/workspaces/{workspace_id}/agents/{agent_id}/channels/{provider}/test-connection` 接收 `version`、`app_id`、`app_secret`。飞书调用官方 tenant token HTTP 接口、钉钉调用官方 accessToken HTTP 接口；不创建长连接、不发送消息、不保存绑定。验证前后核验工作区管理员权限、员工归属及当前绑定版本。
- 飞书/钉钉成功返回 `credential_status=verified`、`receipt`、`tested_at`、`expires_at`。回执有效 5 分钟，以平台秘钥派生 HMAC 绑定操作者、工作区、员工、渠道、版本、完整凭据；不包含明文凭据或提供方 token。无内存会话依赖，滚动重启可继续使用；平台秘钥改变会使回执失效。
- 企微返回 `credential_status=format_only`，没有认证回执，不建立临时 WS，避免新连接踢掉已有连接。
- 现有渠道 `PUT` 增加 `test_receipt`。飞书/钉钉提交新 `app_secret` 必须提供对应回执；无新凭据的启停更新无需重新认证。企微凭据保存仍只做格式校验。
- 更改已保存 `app_id` 需 `confirmed_impact=true`。复用已有凭据加密、身份代次递增、配置版本、统一策略投影逻辑，不新增解绑接口。
- 渠道 GET 携带 `include_rebind_impact=true` 时，同一只读快照返回当前代次受影响的使用范围条目数、能力包、群共享和触发器通知规则，供换绑确认展示。工作区 Web 确认人使用独立身份。
- 保存事务再次检查当前管理权限及原配置版本。重复回执/同版本并发只能一个成功，其余 409，不用最新版本自动覆盖。候选验证失败不改变旧绑定。过期、改动凭据/作用域的回执返回 `channel_test_required`。

保存响应保持 `applied.status=pending`；不将 token 验证写成 connected 或 permissions ready。由实际 EventHub 状态和权限诊断独立提供连接/权限证据。网络超时、限流及提供方错误只返回安全错误码，不记录或回传提供方响应正文。

换绑后，旧代次的人员、部门、群引用继续保留供核对，实际授权跳过这些条目。员工使用模式、能力包场景和剩余名单照常判定。旧黑名单失效可能扩大范围，群共享和定时通知跳过涉及旧身份的方向或目标。仍属于当前代次的规则继续按现有身份与目录证据判定。

## 验证

确定性测试覆盖三家字段协议、仅HTTP认证、企微无网络、回执过期/凭据变化/作用域隔离、验证期间撤权与改绑、并发保存、重放、显式换绑影响确认及状态不虚构。实际提供方账号认证/手机扫码不属于该确定性回归的覆盖范围。

本轮回归记录：`TestChannelCredentialEvidenceAndRebind`、`TestChannelCredentialRechecksAuthorizationAndVersion`、`TestManualChannelSecretRejectsPlaceholders` 和 `integration/channelprobe` 协议测试使用确定性 HTTP transport；`TestManualChannelCredentialsHTTPAndConcurrentSave` 三渠道及既有 `TestChannelPublicConfigurationHTTP` 在全新临时 PostgreSQL 中以 `-race` 通过。没有使用用户真实渠道账号进行验证，不代表实际机器人连接已完成。
