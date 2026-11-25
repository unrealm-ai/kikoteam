# 企微智能机器人文本渠道

本实现针对智能机器人 WebSocket，不是企业应用 CorpID/Secret 或群 Webhook。Platform 管绑定/凭据与联系人映射，Manager 管输入、权限和投递事实，单个 Event Hub 持有本地收发 session。没有分布式连接归属或跨 Hub 路由。

## 协议与身份

`event-hub/internal/channel/wecom.go` 使用既有 gorilla/websocket 实现文本协议。字段和时序依据官方 `@wecom/aibot-node-sdk` 1.0.7 源码及其本地 WS 测试核对；这不是“使用官方 Go SDK”的声明，也没有给 Hub 增加 Node/Pi 定制运行时。

固定连接 `wss://openws.work.weixin.qq.com`，`aibot_subscribe` 提交 bot_id/secret。只有对应业务 ACK `errcode=0` 才建立可发送 session。认证超时 10 秒；心跳每 30 秒，连续三次未确认则关闭；同 bot 不准并存两个本地活 session。断线、接管、配置变更由既有 Supervisor 退出重建，旧 session 不复用。

可信身份范围是 `namespace=app_id, tenant_id=bot_id`，不是企业 CorpID。回调 aibotid 必须与绑定一致，from.userid 是 `user_id` 命名空间；可选 corpid 仅作为格式校验，不授予企业身份。msgid 去重；req_id 只作当前 session 协议关联。私聊目标是 userid，群目标是 chatid；provider_directed 表示平台定向机器人消息，不伪造 bot_mentioned 或结构化 @。没有话题/普通群监听/关键词触发能力。

仅文本入站；引用文本不取得 `control_eligible`，该标志只给无 quote/mixed/image/file/voice 的私聊纯文本。Manager 的验证码消费使用此可信标志，验证码是否有效仍由 Platform/Manager 判定。

回调经有界队列持久受理，ACK读取不阻塞于业务数据库写入。队列满或持久化失败关闭连接。企微协议没有 durable inbound ACK，本实现不伪造 ACK，也不承诺提供方必定重投。接收时刻在 socket 读取时固定，避免排队延迟改变近期历史锚点。Manager 持久受理首条可信消息后，Hub 即可用已确认的机器人身份回复。随后配置快照补齐相同身份时沿用原连接和回调关联。

## 发送确认与隔离

所有发送沿同一已认证收件 socket。原消息回复仅用缓存的 req_id，目标必须一致；缓存过期、重启或接管后不能改成主动发送。企微私聊的后台任务结果由 Manager 按原任务 Inbox 的可信发件人独立建单，重新授权后可在当前连接主动回告。主动文本走 `aibot_send_msg` markdown；回复走 `aibot_respond_msg` stream，finish=true。输出上限 16 KiB，不涉及媒体。

发送前检查 binding/generation/revision、session 活性，再在 write mutex 内执行共享 `BeforeDispatch` 授权门禁。每个 callback 同时只允许一个发送，全 session 最多 128 个未决 ACK。

匹配 req_id 且 errcode=0 的业务 ACK 才返回 sent：EvidenceType=business_ack、EvidenceID/ProviderRequestID=req_id、ProviderSessionID 为本地随机会话标识。协议没有消息 ID，不伪造 provider_message_id；sent 不等于用户已读。

已知未派发返回 failed/not_dispatched；可能已派发但结果不明返回 unknown，不自动重发。未知 req_id 加不可淘汰污染标记，不能被迟到 ACK 用来确认下一次发送。一次回复收到成功业务 ACK 后，下一笔独立投递可以沿同一 callback 开启新的 stream，并等待自己的 ACK。回复结果未知时同时封存原消息 ID 和 req_id，任一封存集合达到 1024 时关闭 session。Provider没有已实现的 req_id 远程发送查询。

## 近期群历史

`wecom_history.go` 以机器人凭据调用 CLI HTTP 协议，非 shell 命令：
- 固定 qyapi.weixin.qq.com `/cgi-bin/aibot/cli/get_cli_config`，SHA256(secret + bot_id + time + nonce) 签名。
- `/cli/service/discovery` 只接受固定同源 `/cli` 和 POST `/chat/messages/list` 或 `/chat/groups/list`，不会跟随提供方返回的任意 URL。
- 每次请求前及结果返回前再次调用 Manager 授权门禁；5秒总读取预算，响应上限4MiB。失败降级仅处理当前消息，不改成其他群/其他渠道。

群消息读取规则：同一个已授权群、前导且仅一个 @名称、全文相同、回调接收时刻 ±5秒且唯一候选为锚点；重复候选或无法覆盖该时间窗均降级。向前取到上一次该名称被@之前，最多100条、8页/10,000条扫描、128KiB文本（另遵守Manager 32768字符上限）。北京时间解析，近7天范围，保持提供方顺序和同文重复记录。配置/权限在查询后变化则丢弃结果。

历史没有提供方消息ID：`message_id` 必须空，使用 `record_key` 仅在当前job中定位/去重，不跨查询合并、不用于回复或授权。历史说话人是显示标签，`sender_type=unknown`、无可信ID namespace，不创建principal、不把搜索/历史userid等同回调userid。Manager 仍将每条历史存为独立silent user Input，由Engine原生路径按现有规则合批。

`ListWecomRecentGroups` 已接入统一选群的企微近期群刷新。管理员为当前 AI 员工请求刷新，Platform 登记绑定版本与请求人范围内的任务，Hub 按固定最近7天窗口分页读取；每页与最终发布都复验绑定身份和权限。只有完整分页结果才原子发布候选，最多10页、10000群、35秒；候选有效5分钟，失败不发布部分结果。此列表不是完整成员目录，也不要求管理员本人加入目标群。定向群入站保留的已观察群仍作为另一种来源，不能冒充当前完整群成员证据。刷新不读取群消息、不赋予管理员读群权限。

公开接口为 `POST .../channels/wecom/groups/refresh` 和 `GET .../channels/wecom/groups/refresh/{id}`；请求合并正在执行的同人同绑定刷新，结果通过统一群选择器读取。内部任务采用独立Hub服务身份、45秒租约和3分钟截止，旧连接版本、过期租约、撤销权限均不能发布候选。

## 验证与尚未验收

新增本地WS/HTTP mock测试：认证前不发送、同socket回复/主动发送、正确业务ACK、unknown/迟到ACK不串单、首条消息跨身份刷新回包、同一回调连续回复、发送前撤权、接管、持久受理失败不伪造ACK；历史唯一/多候选/缺锚点、同文保留、5秒窗口、provider discovery同源检查、撤权丢弃及query-local ID。

没有连接真实企微、没有外部发送；真实应用权限、CLI能力和生产协议兼容性仍需部署验收。本轮不迁移媒体、定时/Webhook、人确认业务或统一选群UI。


### 已观察群自动补名

- 定向群入站照常同步登记群 ID；不在收消息或回复路径调用企微。后台沿用近期群查询，在发现最近 7 天收到过消息的无名群时，以当前已核验的机器人绑定排队。每个绑定只保留一个自动刷新任务，连续入站共享结果；同配置下两次查询至少隔 15 分钟，且有较新的群消息。手动刷新仍优先。
- 完整查询成功后，用机器人绑定、身份代、租户、群 ID 精确匹配，只把非空群名补进仍获准的已观察群的空名称。不新增观测群、不覆盖原名称、不改变群 ID 或权限；失败、空名和未匹配不影响聊天。后台任务不会发布近期群候选，手动刷新仍可发现没发过消息的近期群，也顺手补已观察群名称。
- 选群仍只读数据库，补名后可按名字搜同一条记录，没设名就显示群 ID。迁移 100 下滚会删除系统补名任务，恢复手动任务的请求人约束；已获取的群名属于显示数据，保留。
