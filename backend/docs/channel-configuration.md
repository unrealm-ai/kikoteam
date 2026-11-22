# 员工渠道配置与 Hub 同步

首批仅飞书，人员身份使用本应用 open_id，不要求或自动绑定 Web 账号。配置管理仅工作区 owner/admin；渠道凭据按员工独立持久化，使用统一根密钥派生用途密钥 AES-GCM，加密关联工作区/员工/连接。普通管理查询只返回 has_secret，不返回密钥。

## 管理与版本

飞书与钉钉的扫码绑定和手填绑定使用同一员工使用策略投影。迁移 57 将已有渠道的实际使用范围与员工页配置对齐。飞书及钉钉登记取得凭据后的检查和提交在后台推进。飞书已绑定应用的当前权限使用持久租约周期检查，结果按凭据代次发布。目录首次同步在可信来源可用后自动开始，成功后每 15 分钟刷新，未通过时按一分钟复查。目录范围变化后的完整快照替换旧版本，明确拒绝时撤下旧权限证据。

`GET/PUT /api/v1/workspaces/{workspace_id}/agents/{agent_id}/channels/feishu`。首次 GET 返回 404 channel_not_configured。PUT 完整提交 trigger/history/access，version 首次为 0，此后为 config_revision；省略 app_secret 保留密钥，切换 app_id 必须提供新密钥。

- config_revision：任一配置写入的乐观锁版本。
- connection_generation：凭据替换、应用身份变化或连接启停时提升；仅应用变化清空已确认租户及机器人身份；同应用 Secret 轮换保留身份 pin，新凭据观察到不同租户或机器人时拒绝应用；旧事件与旧身份不得继续使用。
- identity_epoch：仅 App ID 换绑时提升，换回原 App ID 也不会复用此前 epoch。Manager 会话/主体以 epoch 与已确认租户联合隔离；普通 Secret 轮换及启停不切断同租户身份历史。
- rules_version：trigger/history 改变时提升，不为单纯关键词修改重连或轮换凭据。
- trigger：mention、keywords、regexes 为 OR 条件，正则按 Go RE2 语法检查；飞书群本地过滤，私聊不使用此过滤。
- access：mode 为 all/allowlist/denylist，allow_ids/deny_ids 为该应用 open_id，拒绝名单始终优先；可加 allow_chat_ids/deny_chat_ids，空群白名单不额外收窄。默认人员 allowlist 为空，拒绝私聊业务使用。group_members_enabled 默认 false；显式开启后群成员不受私聊白名单限制，但人员黑名单与群范围仍优先。
- history：limit 为 1–100；启用时按上次有效触发之后、本次之前补读，最多 limit 条，首次取近期；Reader/Manager 联调与失败降级须单独验收，保存开关不等于真实飞书历史已验证。

保存配置不表示凭据已验证或长连接在线。身份由 Hub 官方 API 和已认证事件确认，applied 报告只能精确匹配当前三版本，身份首次 pin 后不可被不同值覆盖。员工停用由 Manager 返回固定业务提示，不混同渠道连接禁用。

## 内部协议

专用 `KIKO_HUB_TOKEN` 至少 32 字节，独立于 Engine token；内部端点拒绝 Cookie/Origin，不对浏览器开放。GET `/api/internal/v1/channel-bindings` 返回一致全量快照 snapshot_revision 和 items（含凭据，必须禁止缓存/日志）。POST `/{id}/applied` 回报三版本及 applied/connected/disconnected/failed 状态、固定 error_code，可携带 observed_tenant_id/observed_bot_open_id 首次绑定身份。

Platform 配置与全局通知钟在同一事务保存。后台向 `KIKO_HUB_CONTROL_URL` 的 `/api/internal/v1/channel-bindings/refresh` POST `{revision}`；202 只表示通知被接收，不代表配置已应用。通知失败保留待发版本，一秒周期尝试；Hub 启动及周期全量拉取兜底。Hub 仍依赖 PostgreSQL 选主和迁移，不直接查询 Platform 渠道配置表。

Hub 状态为观察值，不作为准入授权依据；三版本 CAS 不能严格排序同一配置的旧 leader 在途状态报告。失锁取消在途请求，晚到在线状态不证明当前可用。精确的跨 leader 状态序列协议尚未实现。

## 验证边界

已新增合成密钥、独立 PostgreSQL 测试覆盖：加密/不回显、独立版本、保留或替换密钥、乐观冲突、跨工作区密文不可解、权限拒绝、一致快照、持久通知、状态旧版本拒绝、身份首次 pin、内部 Cookie/Origin 拒绝。真实飞书应用权限、审批与消息收发需独立真实租户验收，不能由配置保存成功推断。

当前 Hub 为每个员工绑定启动独立飞书连接，尚无同应用共享连接和多员工路由规则。因此暂时在串行配置事务内拒绝同一 app_id 的第二条 enabled 绑定，返回 channel_app_binding_ambiguous；可以先保存禁用配置。此限制用于防止消息错路由，不是飞书平台固有限制，也不是长期业务唯一约束。未来定义共享连接路由后可替换该检查。

飞书权限诊断只读取授权范围首页，与通讯录同步使用相同的 ID 类型和每页 50 条参数。飞书曾对每页 1 条返回 HTTP 500（业务码 `2200`），同一凭据改用每页 50 条即可正常读取。明确拒绝标记通讯录访问失败；服务端异常、限流和不可解析的响应只标记「尚未确认」与 `feishu_directory_check_unavailable`，不会冒充权限缺失，也不会判定就绪。诊断通过不等于全量同步完成，成员和部门数据仍由同步任务独立验证。
