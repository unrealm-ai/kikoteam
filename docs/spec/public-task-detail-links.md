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
| TDL-07 上下文与回告 | 成功接受网页追加或继续后，以真实 Input 关联原来源主对话的非触发系统记录；未受理不记为成功。原任务真正回告按原来源路由，并按源 Input 幂等，不因网页提交者或卡片收件目标改路由。记录分别标明 `task_id` 与 `session_id`，附 JSON 引用的原文；显式继续会注明已提交新一轮。无 bearer，不独立开轮，也不改变回告轮工具权限。 |
| TDL-08 发卡与降级 | 普通外部渠道轮次在有效配置下按确切 `task_id` 使用 `send_task_detail_card`，不传目标、范围或 URL；Manager 再核对可信触发根、来源、目标和设置。主动回告轮、登录态 Web 对话均不可调用。卡片用原渠道既有投递；只有明确未被渠道受理的可降级拒绝才尝试普通链接，未知不补发。工具回执不返 URL；已确认投递后才记一条独立非触发卡片或链接系统记录。 |
| TDL-09 混版与触发 | 新 Hub 在领取普通投递、授权触发通知时声明支持任务详情；旧 Hub 不领取新卡片类型，也不会收到触发通知链接。普通卡片的待发链接加密短期保存、在已授权的发送阶段交付，终态清理。符合条件的已完成 Hook/定时结果可在同一外部通知附只读链接；不可配置、签发失败或链接过长时保留原通知，不新发、不阻断原通知。 |

## 部署与验证

- 公网用 HTTPS。内网单机允许 HTTP/IP:端口；HTTP 不加密，客户端要能实际访问该地址。首次打开路径含可操作凭证：应用设置禁缓存、禁索引、无 referrer；浏览器 API 请求不携带 Cookie、拒绝重定向。启用前必须依照[反向代理说明](../deployment.md#nginx-反向代理配置示例)确认所有入口不记录 `/tasks/` 原始路径或 bearer，且不缓冲 SSE；应用头不能替代代理、CDN 或 APM 配置。
- [外部详情 OpenAPI](../../backend/contracts/modules/manager/public-task-detail.yaml) · [管理员设置 OpenAPI](../../backend/contracts/modules/platform/task-link-settings.yaml) · [工具契约](../../backend/contracts/taskdelegation/tools.json)。
- 隔离 PostgreSQL 与 HTTP/SSE 夹具：[设置权限和并发](../../backend/internal/platform/repository/task_link_settings_test.go)、[实例、有效期、来源与触发目标](../../backend/internal/platform/repository/task_detail_grants_test.go)、[详情、挂起禁停、原渠道补充和撤销续接](../../backend/internal/platform/repository/task_detail_page_test.go)、[外部投递和非触发同步](../../backend/internal/platform/repository/task_detail_delivery_test.go) 已通过针对性验证。
- [渠道协议及混版](../../event-hub/internal/channel/task_detail_delivery_test.go) 的供应商夹具覆盖三家卡片编码、确认拒绝后的降级、结果未知不重发及旧 Hub 兼容；Event Hub 隔离全量竞态、lint、构建通过。Web [共享入口、只读控件、快照恢复](../../web/test/public-task-detail.test.mjs)、[模型工具清单隔离](../../backend/internal/manager/mcp/taskdelegation/catalog_test.go) 的针对性测试及 Web 构建通过。[应用首屏安全头](../../backend/internal/app/task_detail_landing_test.go) 有专门用例。已用 Chrome 在纯 HTTP 的本地浏览器夹具验证只读隐藏、Bearer 与文件下载、SSE 更新、普通补充、挂起时可补充但不可停止，以及停稳后的显式继续按钮。该夹具不连接真实 Manager 或 IM；真实渠道客户端打开详情 URL、目标部署的外部代理日志配置尚未验收。
