# 引导输入与任务回调通知

2026-11-17 基础输入/回报已实现；2026-11-18 有序合批与渠道边界更新实施中，下文新增章节取代旧版逐条领取与 false 阻塞策略。Engine 直连固定 Pi RPC；不修改 Pi 源码，不新增或扩大既有扩展。当前实现与以下验收记录一起评估，历史文档中的逐条排队和自动回报未交付说明由本页增量取代。

## 输入与轮次

公开 inputs 和创建 Session 的首条 input 接受 `delivery_mode: steer | queue`，省略默认 steer；MCP supplement_task 固定使用默认 steer，接受文字和有序附件。Web 输入框可选引导或排队，默认引导。存量 Input 在迁移 15 中保留 queue，新提交使用 steer。普通 Input 去重指纹包含有效方式，旧省略字段请求重放兼容旧指纹；改方式不能复用原 key。

steer 忙时通过原生 RPC 引导当前 Turn，空闲时启动新 Turn；不会强制终止正在运行的工具或撤销副作用。queue 保留在数据库，当前平台 Turn 终结后才领取，原生 follow_up 队列不承担平台持久队列。一个 Turn 可处理多条同主体输入，始终最多一个活动 Turn。Turn.input_id 表示起始输入，各 Input.turn_id 保存关联。显式 queue 不会被回报提前消费。

运行期间每 250ms 检查待引导项；相同主体、渠道代次和当前权限才允许加入。跨主体用户 steer 返回 409 steer_actor_conflict，调用方可明确改用 queue。已有排队回报或正在执行回报时，用户消息可直接排在后面，保留原 delivery_mode。已经入队但当前轮主体不同的 steer 留到下一轮。

普通任务回报使用 queue，每条独立开启一个主 chat Turn。Engine 按可信 source_kind 处理存量 steer 回报，回报与用户输入或其他回报分别成轮。在同一主体提交 `用户 A → 回报 R → 用户 B → 用户 C` 时，执行顺序为 `A｜R｜B + C`，B 开轮期间 C 仍可在满足原生消费条件时 steer。

起始 `prompt` 收到 `started` 或 `handled` 时记录为 `committed`。每个 Turn 同时最多等待一条输入的用户消息事件。追加 `steer` 收到 `queued` 表示 Pi 已排队，只有观察到与当前输入匹配的原生用户 `message_end`，才把后续操作归到这条追加输入。`handled` 表示 Pi 已自行处理该指令。正常收尾时，将 `clear_queue` 明确移除的引导输入以原身份退回平台队列。停止范围内的输入和结果不明的输入均不自动重发。

## 任务结果主路径

Engine 在 task Turn 正式消息、执行终态和输入事实提交成功后调用：

`POST /api/internal/v1/task-results/callback`

携带 Engine 内部 Bearer，拒绝 Cookie/Origin。正文 `{ "turn_id": "UUID" }`；未开始即取消/拒绝的输入使用 `{ "input_id": "UUID" }`，两者互斥。不接受结果正文、成功标志或目标地址。Engine 有限重试两次，每次最多 2s；失败保留原任务结果并记录 task.report_pending，通知不触发任务重跑。

Manager 从数据库读取已经提交的终态与可信 source_chat_session_id，在目标准入事务中去重、重新鉴权，原子创建 manager_task_reports 和内部 queue Input，以及可靠 input.updated。没有常驻 Manager worker，不调用模型或外部服务于数据库事务内。多 backend 实例由目标 Session 锁、来源 Input/Turn 唯一约束与 report_id 唯一约束裁决；响应丢失或与补偿并发都返回原身份。

回报每个 task Turn 一次，正常结束、待确认回复、无文本、失败、中断、取消均覆盖；无 Turn 的拒绝/取消单独按 Input 去重。来源结果固定到原轮次，不取后续轮次替代。预览最多 16000 字符并标识截断，内部保留 Task/Session/Turn/Input 关系；传给模型的新回告只提供业务 task_id 和具体 Turn/Input 引用，使用 task_id 调用 get_session。正常回复保留原文；非成功终态明确说明状态，不把过程回复当成最终成功。

主会话已归档或原权限已失效，登记 suppressed，不恢复或替换会话。队列暂满等临时错误回滚，后续补偿。主会话无 Engine 时复用既有分配；活跃 Engine 在本轮检查输入，空闲 Engine 启动新轮。

## 原生上下文与权限

使用 Pi 原生 prompt 承载带明确标记的内部任务回报。原生传输角色仍是 user，平台事实 source_kind=task_report。正文固定平台说明加 JSON 引用结果，指示结果资料不构成授权，需要确认时询问用户。公开写接口无法伪造 source_kind 或 report_id；历史只生成输入更新，不生成 user.message，Web 左侧显示任务回报卡片。

回报启动主 chat 新轮时，Engine 按 Backend 为已领取输入返回的可信 `ReportOnly`，在同一条 Pi user prompt 的正文前加 `<system-reminder>`；不改 Input、回报原文（含现有来源标签）、顶层 system 指令或发送权限，也不另发消息来触发。提醒要求先说可核实结果，再说明未完成部分和必要的下一步；产物选择不等于送达。普通用户正文和历史回放沿用原有内容。

同一主 chat 在通常的模型请求中声明同一组任务工具定义。Web 不声明渠道产物选择；有渠道的会话保留 `artifact_select`。仅支持直接答复确认的主私聊声明三个人工确认工具，群和话题不声明。这个目录只包含静态名称、描述与参数结构；调用仍以每轮真实授权为准，没有可调用凭据时同名工具返回不可用，不读取业务数据。Pi 冷启动的发现顺序或不可用时的结果不承诺固定，不因此承诺提供方缓存命中。

纯渠道回报轮仅允许调用 artifact_select；Web 回报轮不允许调用任何任务或确认操作。各终态按实际结果回报，并携带该任务在该 Turn 明确发布的 artifact_id、版本、名称、类型和大小。Manager 将可选范围限定为本轮起始回报和已确认进入上下文的回报。写委派需本轮存在已确认进入原生上下文的真实用户输入，调用与事务内来源检查都核验；任务文本无法授权新业务操作。正常任务工具仍按起始主体与当前能力授权，steer 只允许同主体加入，不建立跨主体权限并集。

输入受理、主会话回复和渠道送达分别记录。故障后保留已提交的平台状态，结果不明的输入不重放。stop 不停止子任务，停止后新产生的任务回报属于新输入，可以在收尾后唤醒；archive 拒绝新回报输入。

## event-hub 补偿

elected WorkGroup 包含通用 TaskReports 与渠道 Supervisor。即使没有启用渠道绑定，配置 backend 内部连接后也运行回报补偿。接任后立即调用一次，之后每 30s 调用：

`POST /api/internal/v1/task-results/reconcile`，Hub Bearer，`{ "limit": 100 }`。

Manager 查找终结超过 10s、启用回报且尚无回报记录的 Input/Turn，逐条复用主动回调业务；单条失败不阻断该批其他记录，不持久推进会跳过失败记录的时间游标。一次请求可能部分提交，重试去重。Hub 丢失主身份后停止调用并等待工作退出，迟到的重复请求仍由数据库裁决。

迁移时已经终结的旧输入 report_enabled=false；升级时尚未结束和之后受理的输入可回报，避免历史任务集中唤醒。待办查询有部分索引，无 MQ、LISTEN、独立回调队列或 Manager 扫描进程。

## 渠道与发布

任务创建时保存来源主 chat Input，固定关联原 Inbox。回报轮以原可信 Inbox 创建 `task_reply:<chat_turn_id>` Delivery，每个选中附件独立生成 Delivery，引用所属回报的原 Inbox。普通渠道用户轮可通过 get_session 取得同主体获准的历史候选，主动重发时绑定当前用户消息。发送前照常鉴权，提供方发送未知仍保持 unknown。升级前任务缺少来源 Inbox 时保留内部回报。升级前由回报启动的已结束轮次继续使用回报投递路径。

普通渠道用户轮按 Turn 读取正式正文，为每轮生成一条 `reply`。锚点选同一渠道会话中该轮结束前已经确认进入原生上下文的最后一条真实用户输入，只有起始输入符合时回复起始消息。较早的合并输入由同一条回复覆盖。重复扫描复用已建投递及其锚点。未执行就被拒绝或取消的入站消息仍分别生成状态提示。

回归覆盖回报排队期间和运行期间的渠道受理、两个连续回报独立成轮、兼容存量 steer 回报、回报之后普通用户恢复 steer，以及三渠道合并轮的正文、锚点和并发去重。升级时先让旧 Engine 停稳，再启用新 Backend 和 Engine 的隔离规则。

迁移 15 与新 Backend/Engine 同代部署，先停旧 Engine 并确认 Pi 停稳；旧消费者不理解输入方式，禁止混跑。迁移遇已有 steer 或回报数据拒绝 Down，不丢弃已受理输入。历史与原生目录保留。没有真实飞书凭据时不声称完成提供方线上收发。

## Input 触发标志与来源扩展（2026-11-17，迁移 19）

`manager_inputs.trigger_agent_loop` 为非空布尔值，默认 `true`，已有数据与省略该字段的内部写入保持原行为；Input 查询及 input.updated 事件返回该字段。它与 `delivery_mode` 独立：后者决定引导当前轮或排队下一轮，前者决定此输入是否独立触发 Agent loop；false 仍可作为此前上下文随后续 true 合批进入原生上下文。

来源 `source_kind` 保留 `user`、`task_report`，新增 `system`（平台权限等固定文案、定时提醒）和 `human_confirmation`（AI 找人确认的问题/请求记录，不代表人的批准答复）。来源不自动决定触发标志，不是模型角色，也不授予操作权限。`task_report` 与非空 `report_id` 的原双向约束保留；其他来源不得占用 report_id。

迁移 19 先交付数据底座，内部仓储写入支持显式 false；本次补充渠道历史与合批。公开创建/提交 Input 请求仍不接受自选来源或触发字段，避免伪造平台来源。定时提醒和人工确认端到端入口尚未实现；被拒渠道原文及固定拒绝不实时写 Input。普通 user/task_report 生产者默认仍触发，按需群历史另写 false。

迁移 19 初版采用 false 阻挡后续输入的临时安全策略。2026-11-18 本次实施由下面的迁移 20 合批方案取代：false 单独等待，后续首个 true 到来时随批消费；不需要用户先撤回 false 才能继续。停止、归档和恢复仍按各自事实处理，不借合批丢弃未提交上下文。

发布需先停稳旧 Engine，再升级迁移与新 Backend/Engine，禁止混跑：旧消费者不知道该标志，仍可能错误执行 false 输入。Down 遇 false 或新增来源记录时明确拒绝，不能丢掉语义后回退；本次没有执行运行中服务迁移或重启。

迁移 19 底座验收（不是本次合批/话题验收）：隔离 PostgreSQL 全量迁移生成 schema 并 check-schema 通过；新增真实数据库测试覆盖旧数据默认 true、false 普通/steer 双路径阻挡与撤回恢复、来源/回报约束、Down 拒绝语义丢失、内部写入及 HTTP 读取/事件字段回路。Backend Manager 与 Platform repository 的 race 回归、Engine repository/runtime 测试、Backend/Engine lint 与 build 通过。没有执行生产迁移、真实模型/渠道验收或全系统端到端回归；既有消息前缀规范保留；该批验收时合批尚未实施，不能沿用这份旧测试记录宣称新增合批和话题已全部通过。


## Input 有序合批（2026-11-18，实施中，迁移 20）

Engine 主动拉取 Session 队列，不将所有实时群消息灌入 Input。只有 false queued 时不启动 Pi，Backend 分配也只在存在可触发输入时安排执行。找到按 seq 排序的第一条 true 后，原子领取它及此前尚待处理的 false；后面的记录留下一批。Manager 不预先合并原文，Engine 才组装一条纯文本原生 user prompt：此前内容作为历史上下文，最后 true 作为当前执行请求。单条输入不额外套合批分区。文本标签仍遵循[消息前缀规范](../../docs/flows/conversation-and-task.md#前缀规则)，系统来源统一 `[系统消息]`，已有标签不重复加；批次分区不是新增身份或权限标签。

迁移 20 增加 `manager_inputs.batch_input_id`，指向该批的触发 Input，同 workspace/session 的复合外键约束防止跨会话挂接。Engine 按触发根 `Input.id` 记录投递结果；各成员保留自己的 ID、seq、原文，`turn_id`、启动状态及投递状态按批次传播，不新造用户消息替换原记录。`processed` 不等于 `committed`，也不等于模型成功或渠道送达。

执行权限来自 true 根。false 引用内容不因历史发送者当前无权限而被拒绝，但仍复验员工 epoch、绑定、租户、连接代次和会话隔离；不建立跨主体权限并集。主会话与每个话题分别拥有独立 Session、Pi 原生历史和批次；话题配置继承父会话，每次授权重新检查，旧无话题映射保持不变。渠道话题映射由迁移 21 引入 channel_thread_id，旧主会话为空 thread、既有历史不自动拆迁；飞书群话题按 thread_id 独立分页补历史，私聊话题不抓历史；群话题读取失败时明确降级，仅当前事件进入自身 Input 并推进自身边界，绝不混入主群历史。不能仅凭 Engine 按 Session 隔离宣称全部渠道话题能力已经可用。

忙时只检查按序最早的 true。若它为 queue、主体不兼容或不能引导，则等待，不绕过它挑后面的 steer；允许引导时同样一次提交该 true 及其前面的 false。引导批次收到 `queued` 后等待对应的用户消息事件，再发送下一批。

明确 clear_queue 证实整批未消费时整批归还；配置/Pi 启动等失败且明确 absent 的 false 上下文，在非取消情况下退回 queued，等待下一条真实触发，不因为失败根已终结就把背景丢掉。失败 true 保留失败事实，不自动重跑用户执行请求。结果未知的输入保留 `unknown`，取消范围内的输入随 Turn 收尾。

渠道历史水位现改为“该员工该群/话题事件成功写入 Input”，不等待 native committed。历史 false、当前事件及来源关联/水位在同一事务提交；无权限事件不写 Input、不推进。群历史不按作者使用权限过滤；群/私聊 Inbox 策略、关键词静默、当前飞书非 user sender 防循环限制及定时入口缺口详见[渠道运行](channel-runtime.md)。

当前验证：Engine 完整 make check/build 已通过，含隔离 PostgreSQL/race、原生 Pi RPC+MCP、既有扩展测试、OpenAPI、lint 和 build；明确 absent false 归还、unknown 不重放、stop canceled 不归还已测。Backend 群话题边界隔离/降级及私聊话题不拉历史的真实 PG/race 专项、lint/OpenAPI 通过；Hub 话题历史 Mock/race 和 lint 通过。完整跨组件及提供方验收以最终交付记录为准，不把局部测试扩大为所有业务已验收。未执行生产迁移、部署重启或真实渠道端到端验收。迁移 20 Down 遇已有 batch 关联拒绝丢弃，迁移 19 Down 继续保护 false 与新增来源；新旧消费者禁止混跑。
