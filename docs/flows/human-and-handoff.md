# 人工继续与跨员工事项交接

状态：人工确认和纯文本跨员工交接已接入执行链路。当前 Conversation / Task / Session 的最小关系及生命周期按[专项设计](../architecture/conversation-lifecycle.md)确定，工具默认直接执行，故障后新 Input 继续；不新建 Run。文件移交属于后续范围，各员工的 Sandbox 独立。

报告进行到一半，等待财务确认统计口径，和把初稿交给合规员工审核，是两种不同的业务动作。前者沿用原 Task、Session 和材料范围；后者延续 WorkItem，为下一位员工建立独立的工作上下文。

## 等指定人员确认后继续

确认人选择遵循[统一选人与跨渠道群共享](../product/identity-selection-and-group-sharing.md)。可选 Web 工作区账号及飞书、钉钉、企业微信联系人，每个来源按自身身份校验。当前配置、私聊查询和答复流程见[后台任务找人确认](../../backend/docs/human-requests.md)。

1. Backend 登记结构化人工问题、版本及来源 Task、Session 和 Turn，并核对当前确认人的配置与私聊使用权。
2. Engine 关闭目标 Session 的窗口、收尾所属后台进程并核对在途操作，保留运行时的原生会话历史。Backend 收到停稳事实后将 Task 标为等待人工，不新建 Run，不影响共享环境中的其他任务，也不因等待回收环境。
3. 指定确认人在 Web 待确认列表或与员工的主私聊查看问题。Backend 停稳后受理答复，核验答复人、工作区、Task 和问题版本。失去员工使用权后，Web 待确认列表和外部渠道主私聊仍可查询、答复本人已获指派的有效问题。受限渠道轮次只提供确认工具，由主私聊 Agent 核对题目、澄清或提交。
4. 满足条件后，Backend 核验父 Conversation 和原 Session 仍允许输入，原子登记答复及唯一的新 Input。Engine 加载原 Session，为该输入执行新 Turn，重验当前权限；不自动恢复旧 Turn。重复答复不得重复提交，内容冲突或过期有明确结果。

确认人身份失效、被移出规则或投递结果未知时，保留原人选与请求供核对。主私聊 Agent 可依据语义、编号、可信引用或已送达的澄清上下文定位答复。

### 找人确认的运行时停稳

首版接入 Pi。Manager 保存确认单、验权和裁决是否可以续跑。Engine 调用当前运行时的停止机制，核对输入队列、工具与后台进程，再提交停稳事实。有效答复和停稳事实都已提交时，Manager 为原 Task 和 Session 生成唯一的续跑 Input，Engine 加载原生历史开启新 Turn。在途操作结果未知时，保留待核对状态并暂停续跑。

- **Pi 首版**。`ask_human` 通过 Pi 原生工具结果返回 `terminate: true`。工具钩子拦截同批其他调用，拦截结果同样标记 `terminate: true`，满足 Pi 对整批终止的要求。Engine 在工具返回前停止发送 `steer`，核对 Pi 原生队列，将明确未消费的输入退回平台队列。收到 `agent_settled` 后，Engine 再核对原生历史和在途活动。`terminate` 留在 Pi 适配层。 [Kiko 的 Pi RPC 运行时](../../agent-engine/docs/session-runtime.md)
- **Claude Code 后续接入**。可评估在确认请求受理后，由同步 `PostToolUse` hook 返回 `continue: false`，或由宿主通过 Agent SDK 的 `query.interrupt()` 中断。使用 SDK 中断时，宿主还要核对已排队输入，以防它们在中断后开启下一轮。 [Claude Code Hooks](https://code.claude.com/docs/en/hooks#json-output-fields) · [Claude Agent SDK](https://code.claude.com/docs/en/agent-sdk/typescript#query)
- **Codex 后续接入**。由宿主调用 App Server 的 `turn/interrupt`，等待同一 Turn 的 `turn/completed`，状态为 `interrupted`。`PostToolUse` 的 `continue: false` 会替换工具结果，模型仍可能继续。 [Codex App Server](https://learn.chatgpt.com/docs/app-server#interrupt-a-turn) · [Codex Hooks](https://learn.chatgpt.com/docs/hooks#posttooluse)

每个新运行时接入时，须实测 `ask_human` 与写工具出现在同一批、队列已有输入、远程调用结果未知，以及答复后同一 Session 只续跑一次。写工具必须在产生副作用前被拦截。Claude Code 和 Codex 的实现与验证随各自的运行时接入进行。

## 交给另一位员工处理

A 的原任务停稳并等待成果。每次交接或整改在同一事项 TaskSpace 中为接手员工创建新 Task 和 Session。成果提交且停稳后，Manager 向 A 的原 Session 登记验收 Input，Engine 优先领取。A 沿原权限和原生历史开新 Turn，验收通过时完成事项，退回时继续等待整改成果。

内部工具角色、停稳、重放与材料授权见[事项交接与原任务验收](../../backend/docs/work-items.md)。
