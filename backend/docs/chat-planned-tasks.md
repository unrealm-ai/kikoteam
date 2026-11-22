# 主对话待办与执行任务

主对话工具移除 `task_delegation_list_targets`。仍仅委派当前员工、当前 Conversation，不因此开放跨员工、TaskSpace、事项或人工确认。

## 工具

- `create_session`：立即创建并受理执行任务，保持现有行为。
- `create_todo` 保存未来实际执行的目标、范围、限制和独立审批前提，延迟调度由 `planning_state=planned` 表达。返回稳定 Task/Session ID 和 `planning_version`。
- `start_todo`：明确启动未开始的待办；必传查询所得 `expected_version` 和稳定 `request_key`。
- `cancel_todo`：仅取消尚未启动待办。已启动返回 `already_started`，绝不调用 stop；取消后不能再启动。
- `list_sessions/get_session`：包含待办状态与版本；待办详情返回保存的全部正文、`application=on_start`、可用动作，不伪造 Turn 或结果。
- `send_message`：已启动任务支持文字、有序附件或二者，按默认 steer 追加，不重写已执行历史。
- `stop_session`：只停止已启动任务，不能当作取消待办，也不是暂停/恢复。

## 待办状态语义

保留 planned 不执行、启动保留 Task ID、启动时重验模型/权限、补充 on_start、CAS 与幂等、取消待办不停止执行的语义。保持一 Task 一 task Session：待办有一个不执行的空配置 Session，但没有 Input、Turn、原生 Pi session、Sandbox 或能力快照。第一次显式启动才取得当时可用配置/能力快照并生成唯一首 Input。

## 安全与原子性

新待办动作限同源主对话和原创建主体；渠道主体仍须当前绑定代次及权限有效。每次调用和幂等重放都先重新核验来源的活动输入、授权、会话及员工状态。既有已运行任务的群协作语义不改变。

事务复用 Manager 的员工、Conversation、source Session、目标 Session 锁序；待办修改再锁一对一 Task。启动/取消/补充竞争相同 `planning_version` 只能一个成功；不同内容不能复用请求键。正文合并、规划状态转换、配置快照、首 Input、可靠事件及去重引用在同一事务提交；模型配置不可用则全部回滚，待办仍未启动。

通用 SubmitInput 在服务层拒绝 planned/canceled，数据库 INSERT guard 再次阻止旁路。Dispatch 只分配真实待执行 Input；无输入的待办不占执行容量。通用 stop 对待办返回明确错误，archive 仍表示关闭会话而不是取消待办，归档后不能启动。

执行提示词根据已提交的 `start_todo` 记录说明延迟调度条件已满足，并要求继续遵守独立业务审批、指定人员确认、期限和其他任务限制。普通任务也使用 `planning_state=started`，因此提示词依据实际操作记录识别显式启动。任务 Input 保留原业务正文。

幂等重放返回原资源当前状态及 duplicate，不暗示过去动作再次执行。取消和补充不会制造任务完成回告。真正启动后的轮次仍走既有 task_report。

## 边界

本轮不实现事项交接、人工确认、暂停恢复、资料共享或跨员工委派。不得把 `idle`、一轮 completed 或受理成功解释为业务任务已经完成。测试和验收状态以交付报告为准。

## 查询与追加回执

`list_sessions` 的 `planning_state`、`execution_state`、`latest_turn_status` 三项独立筛选按 AND 组合，游标绑定完整筛选及访问范围。轮次 completed 不代表业务事项完成。

`get_session` 保留执行状态、最新/指定 Turn 和可靠结果分页；另返回当前 `input_revision`，默认最新或 `input_id` 指定的输入回执。Manager 为已启动任务的追加确定来源 Input、去重键和输入版本，在事务内检查后写入；相同来源、目标和正文及有序路径重试返回原回执。待办补充使用 `supplement_todo` 和原待办版本。

回执 `recorded=true` 只说明平台保存，`delivery=pending` 表示仍等待或尚无明确投递证据；`received` 只对应原生上下文已 committed；`unknown` 表示投递结果不明；`not_received/not_delivered` 表示明确缺席或未送出的取消/拒绝。`application=unconfirmed` 始终不声称模型语义上应用了指令。输入 seq 是输入版本，待办 planning_version 是规划 CAS 版本，二者不混用。查询、重放均重新核验权限。

对选中的 Turn，`get_session` 额外返回有序的 `supplement_receipts`，按输入序号排列，最多 100 条，更多时 `supplement_receipts_truncated=true`。它是按本轮实时查询的输入回执，不包含后来排队的输入、同批历史或系统上下文；已进入原生上下文仍不等于模型按要求修改了结果。
