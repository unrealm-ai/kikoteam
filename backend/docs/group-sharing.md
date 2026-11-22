# 员工级群共享策略

状态：2026-11-18 开发中，策略配置专项已实现，跨组件读取、派生限制和整体联调须另行验收。本文不表示所有渠道已可读取群历史，也不表示建立了任意 shell/MCP 数据防泄漏沙箱。

## 配置含义

策略归 AI 员工，由工作区 owner/admin 管理。配置人无需加入该员工所在群，只获取配置所需元数据，不因此获得群消息读取权限。

每条策略有自定义名称（必填，1–100 字，不要求唯一）、启停、版本和独立方向：

- `directed`（信息同步）：`sources` 为来源群，`targets` 为接收群或指定真人。每个来源到本策略每个目标形成一条有向读取授权，不是自动转发。
- `mutual`（项目组）：`sources` 至少两个群，`targets` 必须为空；仅本策略内的群两两有向互通。本期不支持指定人加入项目组，不能隐含人群双向授权。

主体可以跨 Web、飞书、钉钉、企微，不按姓名或相同字符串 ID 合并。指定人不含部门；企微真人须已有绑定验证，搜索 ID 不是可信回调身份。

每侧最多 20 个主体，每员工最多 100 条策略。禁止重复主体和自指边。A→B 与 B→C 不产生 A→C，交叠项目组不合并。共享不合并 Conversation、Input 或模型会话。

## 引用与失效

存储完整 `SelectionRef`：工作区、员工、渠道、种类、原生 ID、命名空间、绑定/租户/代次。群含候选 ID，Web 群含群与员工成员代次。显示名不授权。

新增边在事务内验证源和目标的当前身份。拒绝跨工作区/员工、伪候选、错误代次和未验证企微身份。旧失效边可以保留以便查看、重命名或禁用；增加边须重验两端，重新启用须重验全部边。不映射到同名新群或新人。

配置保存只确定授权意图；运行时还须根据真实任务输入、当前受众、精确方向边、策略版本、员工与渠道状态验证读取和披露。发现候选的有效期不是永久群成员证明。运行读取链以该模块最终验收为准。

## 接口

前缀 `/api/v1/workspaces/{workspace_id}/agents/{agent_id}/sharing-policies`：

- GET：`q` 名称子串、`page`/`page_size`，ID 升序，返回 items/total。
- POST：创建返回 201；无幂等键，响应未知须先核对列表，不盲目重发。
- GET /{policy_id}：读取本员工策略。
- PUT /{policy_id}：完整替换且携当前 version，冲突 409。
- DELETE /{policy_id}?version=N：版本删除，方向边删除但审计保留。
- POST /preview：验证当前引用、展示单条策略方向，不写策略、不读消息、不建会话、不发送内容。

契约：`contracts/modules/platform/group-sharing.yaml`。

预览 history_capability 表示提供方能力：Web local_history、飞书 provider_history、企微 recent_window、钉钉 unsupported。history_permission_verified=false 表示没有验证真实外部读取权限；钉钉策略可以保存，但不能表示历史已可读。

## 持久化与审计

迁移 48：platform_group_sharing_policies 保存完整策略；platform_group_sharing_edges 同事务存单策略有向边，不存传递闭包；platform_group_sharing_policy_events 保存创建/更新/删除的操作者、版本、策略快照，删除策略不删除审计。

版本裁决并发。配置审计不能替代读取/披露审计；后者还需记录真实源、目标、范围、策略版本与结果。

## 验证范围

已加入方向/渠道能力单测，真实 PostgreSQL CRUD、版本竞争、非传递、失效边限制、跨渠道真人与 HTTP 鉴权/CSRF/CRUD 回归。配置不产生 Conversation、Input 或 Inbox，审计与方向边一致。使用隔离 schema，无生产库或真实外部群操作。

整体仍需验收任务可信受众、读取时撤权、跨目标派生限制、外部 API 权限和失败状态。不能以配置专项通过代替共享读取端到端完成。
