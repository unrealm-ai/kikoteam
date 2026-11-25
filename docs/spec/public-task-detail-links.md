# 任务详情链接与共享 Web 详情

本功能打开既有执行 Task Session，不新建会话。飞书、钉钉、企微可发外部详情链接；登录态 Web 对话使用自身任务详情，消息中不发 bearer。任务导航／任务中心、对话内任务和 token 页面共用详情与执行过程组件，分别使用登录态接口和公开最小投影接口。

| 编号 | 行为 |
| --- | --- |
| TDL-01 配置 | 严格按已保存的任务链接地址与发送开关生效，两者同时有效才签发。设置由平台管理员读写，保存检查版本；默认地址为空、勾选开启。地址为空时界面置灰但保留勾选值；不回退到部署 Origin。接受显式 HTTP/HTTPS DNS 或 IP Origin，可附端口和根斜杠，归一化、不探测网络。关开关或清空只阻止新签发和未送出投递，不自动撤销已发链接。 |
| TDL-02 签发 | `/tasks/<token>` 是可转发的持有凭证，默认 72 小时，不登录、不换 Cookie、不续期，不暴露内部任务路径。数据库存随机凭证摘要，同一任务允许多个独立 grant。外部群只读；同一个有权限的原渠道发起人从私聊发卡才可获得可写范围，否则不提权。操作记为某 grant 提交，发送目标与原执行授权主体分别记录；无法由 bearer 判断实际点击者。 |
| TDL-03 只读投影 | 公网只给当前 grant 的安全状态、活动和产物，不开放原始工具记录、思考、空间列表或工作区 API。最近活动最多 120 条，可标记截断；列表最多 100 个可见产物。普通群展示通用执行状态和曾确认送达该群／话题的结果，不展示未公开任务原文或标题。私聊可查看原渠道发起主体、代次及当前权限匹配的已完成轮次结果和该 grant 提交的输入。Hook/定时链接只见对应已确认通知的结果状态及文本，不见任务随后的轮次；当前无通知产物发布证据，因此不开放该链接的产物。 |
| TDL-04 产物与实时 | 私聊仅授权主体的已完成且结果明确的轮次产物可读；群须有该会话、绑定、代次和话题一致的已确认发送证据，不以 artifact ID 本身授权。下载复验同一投影资格。公开 SSE 以 Authorization 请求头和 grant 绑定的快照游标续接；推送是新的有界快照，不保证补齐断线期间的逐条事件。过期或撤销停止流并拒绝新读写。 |
| TDL-05 两种写入主体 | 登录态补充和停止以当前登录账号及既有会话、工作区和 Web 使用权限裁定；保持既有 TaskSpace 授权规则，不为停止增加能力包检查。可写 token 使用原任务发起人的原渠道身份，不使用访问者身份或投递目标；每次核验 grant、来源及当前授权。只读 token 隐藏写控件，直接调用亦拒绝。 |
| TDL-06 挂起、停止、继续 | 人工确认或事项交接挂起时，两类详情均按各自权限接收排队补充，不代答、不撤回或解除挂起；在同一 Session 锁内拒绝从两类页面停止（`task_waiting_for_action`），登录态 Web 的直接任务归档同样受该保护，避免借其内部停止取消挂起工作；token 不提供归档入口。不改变普通 IM MCP 停止和上层会话生命周期编排。普通 token 补充默认 steer。停止后须有停稳证据、匹配停止版本并显式继续，才可在原任务新开轮次；迟到普通补充、陈旧双标签及未知停止或工具副作用不自动继续。幂等重试不重复追加或二次停止。 |
| TDL-07 上下文与回告 | 成功接受网页追加或继续后，以真实 Input 关联原来源主对话的非触发系统记录；未受理不记为成功。原任务真正回告按原来源路由，并按源 Input 幂等，不因网页提交者或卡片收件目标改路由。记录只标明统一业务 `task_id`，附 JSON 引用的原文；显式继续注明已提交新一轮。新主动回告同样只以 `task_id` 定位业务任务。无 bearer，不独立开轮，也不把此系统 Input 当作用户授权。 |
| TDL-08 发卡与降级 | 普通外部渠道用户轮与主动回告轮在有效配置下可用 `send_task_detail_link(task_id, text?)`；登录态 Web 不用此工具。用户轮限本对话已启动且有权限的普通任务，不按历史投递去重。主动回告轮限可信根关联的本回告普通任务，拒绝任务不匹配、未过期的已送达或待核对投递；回执分别说明原因。平台生成授权链接，不允许模型拼 URL 或指定目标、权限。渠道优先卡片，有兼容限制或明确拒绝时降级原说明加同一链接，未知不重发。工具回执不返 URL；确认送达后记录「已发送任务…的详情链接」为非触发系统 Input，实际投递形态仅用于内部核验。 |
| TDL-09 混版与触发 | 新 Hub 在领取普通投递、授权触发通知时声明支持任务详情；旧 Hub 不领取新卡片类型，也不会收到触发通知链接。普通卡片的待发链接加密短期保存、在已授权的发送阶段交付，终态清理。较新的 Hub 在授权触发通知时另声明结果卡片能力；只支持附链接的版本保留原文加链接。 |
| TDL-10 主对话任务编号 | 从创建回执、列表、查询、发卡、补充/停止到静默记录及回告，统一使用业务 `task_id`。Manager 在原授权范围映射到执行 Session；不要求模型选择执行 Session ID。Engine、Runner、内部路由与登录态 Web API 继续使用原有执行 Session，轮次和输入标识仍各自独立。 |
| TDL-11 Hook／定时结果卡片 | 具备只读详情资格的已完成执行结果，在飞书、钉钉、企微以原生卡片承载原回告正文，另提供「查看任务详情」。卡片不适用或明确未发送且允许降级时，改发原文和同一链接；已受理或未知不补发。若不能签发或附加链接，保留原通知。Web 仍是原文本与不触发 Input，不下发 bearer；固定提醒不变。飞书按实际发送形态验证原消息和历史锚点，确认带详情的卡片或兜底链接都在原结果 Input 前注明「[系统消息] 已发送任务结果，任务详情链接可查看。」，不带链接或 token，也不另起模型轮。Web 走同一文案规则，当前没有发链接，保留原正文。今后若给企微／钉钉接入结果回写，也必须按确认送达事实复用同一口径。[通知投递与上下文](../../backend/docs/trigger-notifications.md#发送后先历史再系统记录)。 |
| TDL-12 普通任务回告承载 | 主动回告工具带非空说明时，由该投递承担本轮文字回告，不再额外提交同一正文；附件投递不变。无说明或工具拒绝时，保留普通文字回告。带正文投递明确失败，或旧 Hub 不认领且超过 1 分钟时，保留原说明作为普通文字回告；已受理或未知不发送重复正文。支持正文的 Hub 通过内部能力头认领。 |

工具成功返回 `accepted` 表示 Manager 已持久接收投递请求。渠道照旧异步尝试原生卡片，确认卡片格式不可用才降级同一链接。调用结果不返回投递状态或可访问 URL；主对话不重复发送或复述渠道回执。无法确认的渠道结果仍保留在投递回执中，不将其标为已送达。

## 部署与验证

- 公网用 HTTPS。内网单机允许 HTTP/IP:端口；HTTP 不加密，客户端要能实际访问该地址。首次打开路径含可操作凭证：应用设置禁缓存、禁索引、无 referrer；浏览器 API 请求不携带 Cookie、拒绝重定向。启用前必须依照[反向代理说明](../deployment.md#nginx-反向代理配置示例)确认所有入口不记录 `/tasks/` 原始路径或 bearer，且不缓冲 SSE；应用头不能替代代理、CDN 或 APM 配置。
- [外部详情 OpenAPI](../../backend/contracts/modules/manager/public-task-detail.yaml) · [管理员设置 OpenAPI](../../backend/contracts/modules/platform/task-link-settings.yaml) · [工具契约](../../backend/contracts/taskdelegation/tools.json)。
- 隔离 PostgreSQL 与 HTTP/SSE 夹具：[设置权限和并发](../../backend/internal/platform/repository/task_link_settings_test.go)、[实例、有效期、来源与触发目标](../../backend/internal/platform/repository/task_detail_grants_test.go)、[详情、挂起禁停、原渠道补充和撤销续接](../../backend/internal/platform/repository/task_detail_page_test.go)、[外部投递和非触发同步](../../backend/internal/platform/repository/task_detail_delivery_test.go) 已通过针对性验证。
- [渠道协议及混版](../../event-hub/internal/channel/task_detail_delivery_test.go) 的供应商夹具覆盖三家卡片编码、确认拒绝后的降级、结果未知不重发及旧 Hub 兼容；Event Hub 隔离全量竞态、lint、构建通过。Web [共享入口、只读控件、快照恢复](../../web/test/public-task-detail.test.mjs)、[模型工具清单隔离](../../backend/internal/manager/mcp/taskdelegation/catalog_test.go) 的针对性测试及 Web 构建通过。[应用首屏安全头](../../backend/internal/app/task_detail_landing_test.go) 有专门用例。已用 Chrome 在纯 HTTP 的本地浏览器夹具验证只读隐藏、Bearer 与文件下载、SSE 更新、普通补充、挂起时可补充但不可停止，以及停稳后的显式继续按钮。该夹具不连接真实 Manager 或 IM；真实渠道客户端打开详情 URL、目标部署的外部代理日志配置尚未验收。
- 统一任务 ID 验证：[MCP 参数、结果和旧选择器兼容](../../backend/internal/manager/mcp/taskdelegation/task_identity_test.go)、[真实 PostgreSQL 委派](../../backend/internal/platform/repository/manager_delegation_test.go)、[回告任务标识](../../backend/internal/manager/repository/task_report_identity_test.go)及[登录态历史与现行编号导航](../../web/test/manager-conversations.test.mjs)。旧存量历史不改写，历史事件仍可打开原任务。
- TDL-11：[三渠道卡片正文、同链接降级与未知回执](../../event-hub/internal/channel/trigger_result_card_test.go)、[飞书锚点](../../event-hub/internal/channelhistory/anchor_test.go)夹具及新版 Event Hub 全量 race 通过。Manager [隔离 PostgreSQL 专项](../../backend/internal/platform/repository/task_detail_delivery_test.go)通过，覆盖 Hook／定时结果卡片和链接回执、旧 Hub、未启用、飞书不触发且无 bearer 的结果记录；[既有 Web、unknown 回归](../../backend/internal/platform/repository/trigger_notifications_test.go)通过。真实渠道客户端显示、链接跳转尚未验收。

- TDL-08／12：[真实 MCP 与隔离 PostgreSQL](../../backend/internal/platform/repository/task_detail_report_delivery_test.go)覆盖用户轮不去重、同会话错任务拒绝、回告并发占位、有效链接去重和过期重发、异常用户追加不改变回告根、未知不重发、失败或旧 Hub 不认领保留正文；[三渠道测试](../../event-hub/internal/channel/task_detail_delivery_test.go)覆盖正文、同链接降级和未知回执；[Web 对话历史组件测试](../../web/test/manager-conversations.test.mjs)确认显示新工具名且入队不等于送达。
