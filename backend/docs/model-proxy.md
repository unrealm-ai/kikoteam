# Platform 模型代理、积分与员工计费设计

首版于 2026-11-18 实现。本文记录契约、存储与运行边界。产品规则与 UI 见[官方模型与积分产品设计](../../docs/product/official-models-and-credits.md)。

## 1. 目标与现有基线

在 `backend/internal/platform/modelproxy/` 下建立独立功能模块，负责官方模型、员工代理身份、模型转发、用量和积分。保持同一 Backend 进程、Go module、数据库及公网域名，仍属于 Platform，不增加第四个系统级模块或独立代理部署。

已有可复用能力：

- 平台管理员、`GET /accounts` 和用户管理已经实现，沿用[身份设计](platform-auth.md#平台管理员)，不重新初始化角色或新增同义权限。
- 自备模型已在 `/api/v1/workspaces/{workspace_id}/model-profiles` 管理，支持三种协议、连接诊断、加密凭据和当前授权检查，见[既有模型配置](platform-models.md)。
- 员工创建、修改与从模板创建已经接通；官方模型同时接入这几条入口，模板不复制供应商或员工凭据。
- 新建主对话和任务均以员工配置的模型为来源，已有会话保持原配置快照；Manager 下发 Harness 配置，Engine 按既有协议、地址、型号及凭据装配 Pi。
- Event Hub 统一执行数据库迁移，并已有周期调用 Backend 补偿接口的模式。

本模块只根据代理 Key 识别员工、扣费账户，根据请求模型 ID 和实际用量计费。不接收任务、Input、Turn 或 Session 计费身份，不修改 Pi 内部循环或新增扩展。

## 2. 模块边界与依赖

### 2.1 职责

| modelproxy 内部 | 现有能力继续负责 |
| --- | --- |
| 官方模型配置、价格与测试 | Platform 身份、工作区成员和员工管理 |
| 员工 Key 生成、查找、内部轮换 | 原模型列表及员工选择入口 |
| 官方模型请求鉴权、转发及用量解析 | 自备模型管理与直连 |
| 用户钱包、赠送、流水及结算 | Manager 会话、任务、运行身份与调度 |
| 结算补偿用例 | Event Hub 周期触发，Engine/Pi 执行模型调用 |

积分目前只服务官方模型消费，先在模块内实现，不再拆积分服务或支付模块。代理服务调用积分服务结算，不在每个协议适配器内分别扣钱包。

### 2.2 少量接入接口

| 消费方 | 所需能力 |
| --- | --- |
| 注册用例 | 同事务初始化钱包、发放注册赠送 |
| 员工普通创建、模板创建和首次开通 | 同事务确保员工 Key 与固定扣费账户存在 |
| 原模型选择用例 | 取得官方模型安全元数据，与自备模型统一查询、分页 |
| 运行配置适配 | 按可信员工 ID 解析官方模型地址、逻辑型号及当前代理 Key |
| Event Hub | 触发待结算记录补偿 |

消费方定义所需的小接口，`app/server.go` 装配实现。调用关系不等于 Go 导入关系；不让 Platform 父服务与 modelproxy 服务互相导入，不通过无边界 common 包解决循环，也不让 app 成为每次业务请求的数据中转层。

注册赠送、员工 Key 初始化必须加入原有事务。由持有事务的 repository/装配适配层向模块提供同一事务的参与能力，最终提交或回滚仍归外层用例；不得另开事务、另发 HTTP 或先提交主资源再异步补 Key。其他模块不自行写这六张业务表。

## 3. 产品语义与模型解析

### 3.1 员工与扣费账户

- 新员工创建时生成一份代理 Key，扣费账户固定为创建时所在工作区的所有者（owner）。选择自备模型也按既有创建契约生成 Key；仅有 Key 不产生扣费。
- 同一员工选择不同官方模型时复用 Key。原生请求的 `model` 决定具体官方模型，不设置逐 Key 模型白名单。
- 改模型、改员工、换操作人或更换消息来源均不转移已有扣费账户；第一版不提供转移入口。
- 旧员工首次明确选择官方模型时，由有管理权限的操作者在原员工模型表单确认并保存，绑定所在工作区所有者的钱包。不因存在官方默认项而为选用自备模型的旧员工新增绑定。
- 纯改名、普通 GET、运行时请求和后台触发不能建立首次绑定。前端在首次保存模型设置前明确展示扣费说明，后端只在明确提交模型选择且将使用官方模型的用例中建立旧员工绑定。
- 已有 Key 时只校验和复用，不用新的操作人覆盖 payer。并发首次绑定与员工版本修改在原员工事务串行；后到的旧版本请求冲突，不能静默给不同账户建立第二份绑定。
- 代理校验员工启用、当前 Key 仍绑定该付款人，且付款人是有效工作区所有者。没有有效所有者或绑定不符合时拒绝新请求，不使用操作者的积分作兜底。此前放行的调用按入场时记录的付款人结算。
- 升级通过 `000104_workspace_owner_model_payer.sql` 为必要的所有者补建钱包并重绑现有代理 Key，不轮换凭据、不移动余额、不改已有用量记录和流水。发布时先停旧 Backend 的新模型请求和员工写流量，执行 Event Hub 迁移后再启用新版 Backend；不要让旧实例在迁移后继续创建绑定。

使用者是否有权执行员工，继续由现有入口、Manager 和运行配置鉴权裁决。代理 Key 是绑定员工的调用凭据，不重新推断每个消息发送人的身份；也不把模型 Key 当成浏览器登录态或 Engine 服务凭据。

### 3.2 默认模型和快照

原模型选择接口增加官方候选，但不复制全局模型到每个工作区。选择场景的有效默认项优先为可用工作区默认模型，否则使用官方默认模型，最多一项。

默认项用于表单预选，不参与对话运行。新建 chat 使用员工明确保存的模型，task 选择规则不变。两种已有 Session 均继续使用其创建时快照；更换员工模型只影响新建会话，不改写历史，也不热切换在途 Turn。员工选用的模型缺失、停用或不可用时明确失败，不换另一个模型。未绑定扣费账户的员工选用官方模型时需在员工设置确认，不在运行时自动绑定。

Session 模型配置快照可以增加官方来源引用，但不能增加钱包、payer 或计费状态。官方模型的逻辑 ID 和协议/真实型号在引用后保持稳定；服务地址、供应商凭据按当前验证配置解析。自备模型继续使用原快照与当前授权核验。

价格不由 Session 快照锁定。每次代理请求开始时读取当前价格并保存本次快照；同一长期会话内的后续调用采用各自开始时价格。

### 3.3 积分精度和计价

使用 `bigint` 最小单位：1 积分 = 1,000,000 units。管理 API 的价格是「积分 / 百万 Token」，数据库价格是「units / 百万 Token」。金额通过十进制字符串传输，禁止经浮点数往返。

~~~text
charge_units = ceil(
  (input_tokens × input_price_units
   + output_tokens × output_price_units
   + cache_read_tokens × effective_cache_read_price_units
   + cache_write_tokens × effective_cache_write_price_units)
  / 1,000,000
)
~~~

先求和，最后统一取整一次。中间乘加使用可检查溢出的整数运算，价格、用量和最终余额更新都不能溢出或截断。输入、输出和缓存价格非负。缓存价格为 null 表示沿用输入价格，0 表示免费；请求开始时将有效缓存价格解析到快照中。

协议适配器产出互不重叠的普通输入、缓存读取、缓存写入和输出计数；不把已包含在总输出中的明细再次相加，也不把流式累计值逐事件求和。供应商的包含关系无法明确或最终用量不足时标为待核对，不猜测收费。已确认不存在的用量类别计 0，未知用量保存 null。

所有官方请求统一检查余额大于 0，不冻结或预扣。并发已放行请求可全部结算为负数；不保证透支上限。模型调用失败不自动免费，成功也不意味着一定已取得可结算用量。

## 4. API

### 4.1 通用约定

机器契约使用手写的 OpenAPI 3.1，分别维护[平台资源](../contracts/openapi.yaml)、[内部协作](../contracts/internal/openapi.yaml)及[原生代理](../contracts/model-proxy/openapi.yaml)。

- 资源接口前缀 `/api/v1`，使用现有登录 Cookie，写操作要求 CSRF 防护；管理身份不另起 `/admin` API 分组。
- 配置修改携带 `version`；冲突返回 409，客户端重新读取后由用户决定，不自动覆盖。
- 普通模型/员工创建不要求通用幂等键，客户端不自动重发结果未知的创建。注册赠送、人工赠送及模型扣费使用明确业务去重。
- 配置列表使用 `page/page_size`，默认 20、上限 100；流水和调用列表使用 `cursor/limit`，默认 20、上限 100，按 `created_at DESC,id DESC` 或 `started_at DESC,id DESC` 稳定排序，游标绑定筛选范围。
- 时间筛选使用带时区的 RFC 3339，统一换算 UTC，区间为起始包含、结束不包含；页面传入展示时区的月初/月末用于月度汇总。
- 普通错误沿用 `{error:{code,message,request_id,details?}}`，不返回供应商凭据或原始敏感响应。
- 账户本人只能查询自己支付的记录；平台管理员可查询所有账户的余额、积分和计费用量，不因此获得提示词、回答正文或私聊读取权限。工作区管理权不等于钱包读取权。

### 4.2 扩展现有接口

| 方法与路径 | 变化 |
| --- | --- |
| GET `/workspaces/{wid}/model-profiles` | 增加可选 `include_platform=true`；默认 false 保留原管理列表行为 |
| POST `/workspaces/{wid}/agents` | 支持官方模型引用，内部初始化 Key |
| PATCH `/workspaces/{wid}/agents/{aid}` | 切换模型；存量首次确认时内部补建绑定 |
| GET `/workspaces/{wid}/agents/{aid}` | 返回两种模型引用、派生来源/可用性；有管理权限时返回扣费账户安全展示信息 |
| POST `/workspaces/{wid}/agents/from-template` | 复用同一选择规则和原子员工/Key 创建 |
| GET `/accounts` | 现有管理员用户列表增加积分余额 |

使用 `include_platform=true` 时，先授权工作区，再合并当前工作区自备模型和已启用、已验证的官方模型，统一稳定排序、计数和分页；不先分别分页后拼接。返回 `source=workspace|platform`，选择列表的 `is_default` 表示本次有效默认，不能把两种资源的原始默认标记同时当作推荐。

自备模型保持现有元数据；官方候选只含安全名称、协议/能力、可用性、默认标记和积分价格，不返回真实服务地址或凭据。前端类型与 OpenAPI 使用来源可区分的结构，不伪造可管理的工作区 ModelProfile。原自备模型管理页不传新参数。

~~~json
{
  "items": [
    {
      "id": "00000000-0000-4000-8000-000000000001",
      "source": "platform",
      "name": "通用模型",
      "protocol": "chat_completions",
      "enabled": true,
      "validated": true,
      "is_default": true,
      "prices": {
        "input": "10.000000",
        "output": "40.000000",
        "cache_read": null,
        "cache_write": null
      }
    }
  ],
  "total": 1,
  "page": 1,
  "page_size": 20
}
~~~

员工选择官方模型的 PATCH 示例：

~~~json
{
  "version": 3,
  "platform_model_id": "00000000-0000-4000-8000-000000000001"
}
~~~

新建时必须给出一个非空模型引用。PATCH 提交非空 `platform_model_id` 时后端清空 `model_profile_id`，反之亦然；两者都省略保持原值，同时提交两个非空引用或试图清空最后一个引用返回 400。纯改名请求不隐式提交或改写模型选择。

客户端不提交 Key、任意 `payer_account_id` 或持久化 `model_source`。首次绑定会改变员工对外可观察状态，应受同一员工资源版本保护；没有模型内容变化时不伪造模型配置版本。已绑定员工仅返回扣费账户 id/展示名，不返回其钱包余额或邮箱。缺少旧员工绑定时运行配置返回 `422 platform_billing_unconfigured`，提示在原员工设置完成确认。

### 4.3 官方模型管理

以下接口均只接受当前平台管理员。

| 方法 | 路径 | 行为 |
| --- | --- | --- |
| GET | `/platform-models` | 分页管理列表，包含停用项、连接元数据和 `has_key`，供编辑抽屉使用 |
| POST | `/platform-models/test` | 测试待保存连接配置，返回结果及短期绑定回执 |
| POST | `/platform-models` | 创建配置，成功 201 |
| PATCH | `/platform-models/{id}` | 保存连接、价格、启停或默认状态，成功 200 |

不增加 `/configuration`、`/set-default`、`/enable` 或删除接口。GET 列表不返回明文/密文密钥；新建请求结果未知时先核对列表，不依靠同名去重。

创建示例（域名、模型及价格均为示例）：

~~~json
{
  "name": "通用模型",
  "protocol": "chat_completions",
  "base_url": "https://provider.example/v1",
  "upstream_model_id": "provider-model",
  "api_key": "example-placeholder",
  "prices": {
    "input": "10.000000",
    "output": "40.000000",
    "cache_read": null,
    "cache_write": null
  },
  "test_receipt": "example-receipt",
  "enabled": true,
  "is_default": true
}
~~~

PATCH 另带当前 `version`。省略凭据表示保留，明确空字符串表示无需认证；换地址必须明确提供新凭据或选择无需认证，禁止自动把旧密钥转发到新地址。官方连接沿用现有 HTTPS、URL 规范化及不跟随重定向的边界。

测试沿用现有协议和地址规范，使用代理本身的流式用量解析验证是否可计费。工具等能力按实际测试结果声明。网络 I/O 在事务外，测试前后和保存时重新核验管理员资格及目标版本。

回执采用短期签名令牌：绑定操作者、目标模型（新建为空）、目标版本、连接配置的带密钥摘要、测试结果和 10 分钟期限；令牌不含明文凭据。价格不属于连接指纹，单独改价不要求重测。签名回执只证明测试配置，不能绕过当前授权，也不承诺新建请求幂等；不为全局测试新增回执/租约业务表。

新建默认停用；启用或连接变更后的重新启用必须有当前配置有效测试结果。单独改名称、价格或启停不改变连接验证结果。禁用清除默认；设置默认在同一事务内撤销原默认并设置新默认，以唯一约束裁决并发，冲突返回 409。隐式取消的旧默认项也更新其资源版本，防止旧页面误覆盖。

### 4.4 积分和调用

| 方法 | 路径 | 权限与行为 |
| --- | --- | --- |
| GET | `/accounts/{id}/credit-wallet` | 本人/平台管理员，返回余额 |
| GET | `/accounts/{id}/credit-ledger` | 本人/平台管理员，已入账流水及关联调用明细 |
| POST | `/accounts/{id}/credit-grants` | 平台管理员，赠送正数积分，必须提供 `Idempotency-Key` |
| GET / PATCH | `/credit-settings` | 平台管理员，注册赠送额度及配置版本 |
| GET | `/model-usage` | 本人支付记录/管理员授权范围，调用明细 |
| POST | `/model-usage/{id}/resolve` | 平台管理员，对待核对记录补录可信用量或确认不收费 |

流水支持 `agent_id/model_id/from/to/kind`；调用支持 `account_id/agent_id/model_id/from/to/status`。指定他人账户不能扩大普通用户范围。员工筛选时只汇总关联该员工的消耗，不把无员工归属的赠送重复分摊给各员工。

流水响应保留 `items/next_cursor`，附带当前筛选期间的 `summary.spent/granted`；汇总基于授权及筛选全集，不是本页条目。页面本月消耗复用该查询，不新增汇总 API 或汇总表。调用列表返回展开所需的可信 Token、价格快照、状态和关联扣费；不再增加调用详情端点。

~~~json
{
  "amount": "1000.000000",
  "reason": "运营赠送"
}
~~~

赠送键作用域为操作者、目标账户与操作类型；相同键、相同金额和原因返回原流水，初次成功 201、重放 200。相同键不同内容返回 409。去重命中仍重新授权。键和业务去重记录随流水保留，不引入通用 HTTP 响应回放表。

设置 PATCH 示例为 `{"version":1,"signup_bonus":"1000.000000"}`；非负、最多六位小数。改设置只影响后续注册。

异常处理请求：

~~~json
{
  "action": "settle",
  "usage": {
    "input_tokens": 500,
    "output_tokens": 200,
    "cache_read_tokens": 0,
    "cache_write_tokens": 0
  },
  "reason": "根据供应商请求记录核对实际用量"
}
~~~

另一种为 `{"action":"no_charge","reason":"核对原因"}`。只允许待核对状态转换，持锁检查后按原价格快照计算；不接受任意扣费金额。并发及重复处理不会再次扣费；已处理记录返回当前结果或明确状态冲突，不改写已入账事实。页面需要重新核对后再操作，不自动重放不同结果。

### 4.5 原生代理与内部补偿

| 方法 | 路径 | 身份与行为 |
| --- | --- | --- |
| POST | `/model-proxy/v1/chat/completions` | 员工 Key，Chat Completions 原生请求及响应 |
| POST | `/model-proxy/v1/responses` | 员工 Key，Responses 原生请求及响应 |
| POST | `/model-proxy/v1/messages` | 员工 Key，Anthropic Messages 原生请求及响应 |
| POST | `/api/internal/v1/model-proxy/reconcile` | 限定 Event Hub 服务身份，执行有界批次结算补偿 |

前两种使用 Bearer，Messages 接受其原生 `x-api-key` 认证头；同时提供冲突凭据时拒绝。Cookie、Engine token 和 Hub token 不能代替员工 Key 调用代理。Base URL 的下发遵循现有各协议 SDK 拼接规则，确保最终请求命中上述路径。

代理只开放必要 POST 方法，不开放任意路径透传、`GET /models` 或上传/异步资源管理接口。保持协议、工具字段与流式内容，不做跨协议转换；请求 `model` 必须是已配置的官方逻辑 ID，替换为真实上游型号。调用方不能覆盖服务地址、上游认证或扣费账户。转发头采用白名单并替换认证，浏览器 Cookie、内部凭据和员工 Key 不能透传给供应商。

准入失败：无效 Key 为 401，员工或扣费成员不可用为 403，余额不足为 402/`insufficient_credits`，模型不可用为 422/`model_unavailable`，数据库不可用为 503。尚未开始流式响应时按对应协议错误结构返回；已经开始的流不能改发第二个 HTTP 状态，按协议结束并记录实际状态。响应可携带服务端生成的 `X-Model-Call-ID` 供核对。

使用独立流式 handler 和适当的总时限/空闲时限，不能直接套用普通业务接口的短超时。入站请求截止时间写入调用记录，流式期间不得无限续期。代理不自动重发上游请求；SDK 显式重试是新的调用，不按请求正文哈希合并费用。

## 5. 数据库

六张新增表都由 Platform/modelproxy 写入，以 `platform_` 命名；共享 PostgreSQL，不建立独立迁移链。UUID 由服务端生成，时间使用 `timestamptz`。除明确 nullable 外均 NOT NULL。相关账户、工作区、员工及模型历史不级联删除账单；首版沿现有停用及保留策略。

### 5.1 platform_proxy_models

| 字段 | 类型/默认 | 含义 |
| --- | --- | --- |
| id | uuid PK | 全局逻辑模型 ID |
| name | text | 展示名称 |
| protocol | text | chat_completions / openai_responses / anthropic_messages |
| base_url | text | 验证后的上游地址 |
| upstream_model_id | text | 上游真实型号 |
| credential_ciphertext | bytea nullable | 无认证时为空，其他情况加密保存 |
| input_price_units / output_price_units | bigint | 每百万 Token 的单位价格 |
| cache_read_price_units / cache_write_price_units | bigint nullable | null 沿用输入价 |
| enabled / is_default / validated | boolean，默认 false | 启用、默认与连接验证 |
| version | bigint，默认 1 | 乐观并发版本 |
| tested_at | timestamptz nullable | 最近有效测试时间 |
| updated_by | uuid FK → platform_accounts | 操作者 |
| created_at / updated_at | timestamptz | 时间 |

价格非负、version > 0；启用必须已验证，默认必须启用。对 `is_default=true` 建唯一部分索引。开始被员工、会话或调用引用后禁止变更协议/真实型号；不另建模型历史或价格版本表。引用和变更并发时在短事务内锁模型并复核。

凭据复用现有加密原语及部署密钥，采用独立用途派生，附加数据绑定模型 ID/用途。备份须保留相应部署密钥，不在响应、日志或测试回执输出密文或明文。

### 5.2 platform_agent_model_keys

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| agent_id | uuid PK | 一员工一份当前 Key |
| workspace_id | uuid | 与员工的工作区一致 |
| payer_account_id | uuid FK → platform_credit_wallets.account_id | 固定钱包 |
| key_hash | bytea UNIQUE | 高熵随机 Key 的鉴权摘要 |
| key_ciphertext | bytea | 已授权运行时重新装配所需密文 |
| created_at / updated_at | timestamptz | 时间 |

外键 `(workspace_id,agent_id)` 指向员工的对应唯一键。索引覆盖 payer/workspace，供工作区付款人核对使用。Key 至少使用 32 字节密码学随机材料，不使用员工 UUID、邮箱或钱包 ID 派生可猜测凭据；密文采用独立用途与员工身份绑定。

无 `enabled`、展示前缀、管理版本或 Key 历史表。普通准入跟随员工状态，内部轮换原子替换 Hash/密文，旧 Hash 立即不再接受新请求；已有调用按保存的员工和 payer 结算。轮换不变更 payer，不增加用户 API；运行时通过原配置刷新取得新值，不假定 Pi 支持热替换。

### 5.3 platform_credit_wallets

| 字段 | 类型/默认 | 含义 |
| --- | --- | --- |
| account_id | uuid PK / FK → platform_accounts | 一账户一钱包 |
| balance_units | bigint，默认 0 | 可负余额，不加非负约束 |
| version | bigint，默认 0 | 每次入账递增 |
| created_at / updated_at | timestamptz | 时间 |

钱包是当前余额的查询入口，流水是每次变化的事实。禁止直接改余额而不写同事务流水；不新增员工钱包或余额缓存。

### 5.4 platform_credit_ledger

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| id | uuid PK | 流水 ID |
| account_id | uuid FK → wallet | 账户 |
| wallet_version | bigint | 本次入账后的钱包版本 |
| kind | text | signup_bonus / admin_grant / model_charge |
| amount_units | bigint | 赠送为正，扣费为负 |
| balance_after_units | bigint | 此次入账后的余额 |
| agent_id | uuid nullable FK | 模型扣费的员工 |
| usage_id | uuid nullable UNIQUE FK → usage | 一次调用最多一条扣费流水 |
| biz_key | text UNIQUE | 业务去重身份 |
| request_fingerprint | text nullable | 人工赠送内容指纹 |
| operator_id | uuid nullable FK → account | 人工赠送操作者 |
| reason | text nullable | 人工赠送原因 |
| created_at | timestamptz | 入账时间 |

唯一 `(account_id,wallet_version)`。model_charge 必须有 usage/agent，金额 <= 0；admin_grant 必须金额 > 0 且有操作者、原因；signup_bonus 金额 >= 0。0 赠送仍可记录一次注册入账事实，避免后续改设置触发补发。usage/account/agent 的对应关系由结算事务从同一调用事实取得，不接受客户端覆盖。

biz_key：注册为 `signup:{account_id}`，扣费为 `usage:{usage_id}`；人工赠送对操作者/账户/操作/幂等键做无歧义编码或摘要。流水不可修改或删除，去重字段与流水同寿命。首版不增加退款、余额编辑或反向流水 API。

索引 `(account_id,created_at DESC,id DESC)`，以及按账户/员工/时间查询的索引；模型维度通过 usage 关联查询。金额和余额以流水为权威，不在 usage 重复存最终扣费。

### 5.5 platform_model_usage

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| id | uuid PK | 代理生成的调用 ID |
| workspace_id / agent_id | uuid | 原请求员工归属，复合 FK 验证一致 |
| payer_account_id | uuid FK → wallet | 准入时固定的扣费账户 |
| model_id | uuid FK → proxy_models | 官方模型 |
| price_snapshot | jsonb | 模型展示名、计价单位、当次有效单价等安全快照 |
| input_tokens / output_tokens | bigint nullable | 归一化普通输入与输出 |
| cache_read_tokens / cache_write_tokens | bigint nullable | 归一化缓存计数 |
| status | text | running / usage_recorded / settled / pending_review / no_charge |
| upstream_request_id | text nullable | 有提供方请求标识时保存 |
| error_code | text nullable | 可安全展示的错误分类 |
| resolution_reason | text nullable | 人工核对依据 |
| resolved_by | uuid nullable FK → account | 核对人 |
| started_at / deadline_at | timestamptz | 开始及有界调用截止时间 |
| finished_at / settled_at | timestamptz nullable | 调用结束及入账时间 |

计数已知时 >= 0。快照中的价格使用最小单位整数字符串，缓存回退已展开；模型改名或改价不篡改历史展示和费用。没有价格版本、Key 版本、最终金额、Prompt/回答正文，也没有 task/session/input/turn 字段。

索引 `(payer_account_id,started_at DESC,id DESC)`、`(agent_id,started_at DESC,id DESC)` 及待处理状态/截止时间的部分索引。对 settled 记录必须存在且只存在一条对应流水，结算事务共同维护该不变量。

### 5.6 platform_credit_settings

| 字段 | 类型/默认 | 含义 |
| --- | --- | --- |
| id | smallint PK，CHECK id=1 | 单例 |
| signup_bonus_units | bigint，默认 1000000000 | 默认 1000 积分，非负 |
| version | bigint，默认 1 | 并发版本 |
| updated_by | uuid nullable FK → account | 迁移初始化时可空，管理写入必填 |
| updated_at | timestamptz | 更新时间 |

不增加低余额阈值或通用配置注册表。初始化行不是用户注册赠送事实。

### 5.7 现有表变更

`platform_agents` 及 `platform_agent_versions` 的 `model_profile_id` 改为可空，新增可空 `platform_model_id` 外键指向官方模型。数据库 CHECK 两者恰有一个非空，原自备模型的工作区复合外键保留。不重复保存来源字段。

原有员工查询对工作区模型的 INNER JOIN 要改为按来源解析，避免官方员工从列表中消失；员工、模板创建、版本回读与 Manager 配置扫描均支持新引用。现有 `is_platform_admin` 等身份字段已经实现，不重复增加或再次初始化。

## 6. 请求、事务与恢复

### 6.1 准入与转发

1. 按 Key 摘要查当前绑定，核验员工和有效扣费成员身份。
2. 校验请求协议、官方模型状态及钱包余额 > 0，解析本次有效价格。
3. 在短事务中完成准入复核，保存 running 用量记录及截止时间；不锁定或扣除余额作预占。
4. 提交后才使用真实供应商凭据转发，调用中的价格、payer 和模型目标保持本次已解析值。
5. 持续转发流式数据，提取可信用量；取得完整归一化用量后先持久化为 usage_recorded。
6. 调用积分服务按记录 ID 结算。模型端已经完成而数据库结算失败时，不要求客户端重发模型请求。

停用与新准入由相关资源的短事务锁/当前状态复核确定先后。已放行调用不因余额后来下降、模型/员工停用或成员退出而取消结算。不同请求可同时看到正余额并被允许，这正是接受透支的设计。

数据库在准入前不可用则返回 503，不先向供应商发请求再补登记。无需每次事务都更新钱包版本或调用任务额度检查。

### 6.2 结算事务

1. 锁定 usage，已 settled 直接返回已有流水，其他不可结算状态不强行扣费。
2. 确认可信用量及价格快照，计算费用。
3. 锁定该 usage 固定的 wallet，原子扣减并递增版本；不再检查余额够不够。
4. 插入以 usage 为唯一身份的流水，写入扣后余额。
5. 将 usage 标为 settled 并写时间，一起提交。

统一锁顺序为需要的权限主体/工作区/员工等前置资源 → usage → wallet；结算取得 usage/wallet 后不反向获取员工、模型或权限行。已准入调用的结算不重新决定 payer，也不要求其此刻仍能发起新请求。人工赠送在完成当前管理权限检查后锁 wallet。注册、创建绑定的参与操作服从外层既有锁顺序，不另起相反顺序的事务。

流水唯一约束、usage 状态锁及钱包版本共同保证重放不重复扣费。提交结果未知时按原 usage/biz_key 核对，不生成新扣费身份；数据库事务重试不得包含上游网络调用。

### 6.3 注册和人工赠送

注册两条现有路径都在账户创建事务中初始化钱包、读取当次设置、写唯一 signup 流水。注册回滚则钱包及积分一起回滚；登录、重建会话或建工作区不发注册赠送。设置为 0 也完成一次注册处理事实。

管理员赠送在同一事务中完成权限核验、内容指纹/幂等查验、钱包加分及流水写入。负余额正常相加，不先归零。重复请求不增加余额，普通 CRUD 不引入这套资金幂等机制。

### 6.4 异常与补偿

~~~text
running → usage_recorded → settled
       ↘ pending_review → usage_recorded → settled
       ↘ no_charge
pending_review → no_charge
~~~

- 已取得可信用量时，即使供应商响应失败或用户取消，也按可信实际用量计费。
- 明确未产生计费用量时可 no_charge；超时、断流或未知结果不能直接等同免费。
- Event Hub 复用 elected WorkGroup 的周期触发方式，由 Backend 有界扫描 usage_recorded 并重试结算；running 超过 deadline 加合理收尾宽限后转 pending_review。单批上限与超时由该补偿操作配置，避免一次占用整个调用周期。
- 客户端断开后尽力在既定调用截止时间内收尾；已取得用量后可使用独立的短收尾上下文落库，不继承一个已取消的浏览器 context 导致必然丢账。无法取得用量则待核对。
- 迟到用量、补偿和人工处理在同一 usage 锁下裁决。已 settled/no_charge 的终态不被迟到结果覆盖，也不重复扣费。
- 用户或 SDK 发起的新 HTTP 请求是新的调用；代理没有上游恰好执行一次的保证，不使用相同正文去重。
- 崩溃发生在上游产生费用到可信用量落库之间时，仍可能需要人工核对；不承诺无损自动恢复所有供应商用量。

内部补偿只调用 modelproxy 用例，Hub 不直接改余额或账单。首版不增加队列、Redis、分布式计费服务或通用任务租约框架。

## 7. 运行时与生命周期接入

现有内部 `GET /api/internal/v1/sessions/{session_id}/harness-config` 继续核验 Engine、assignment、generation、Input 和员工使用权限；在通过检查后向模型解析接口传递可信 agent_id。不能仅凭浏览器给出的员工 ID 取 Key。

官方分支将固定逻辑模型解析为代理 base_url、协议、逻辑 model_id 及该员工当前 Key；自备分支继续原有凭据解析。尽量保持 Engine 既有 `protocol/base_url/model_id/api_key` 消费结构，Pi 继续原生配置，不增加计费插件。

运行时可以增加模型来源的配置适配，但不新增逐 Turn 钱包查询、payer 传播或任务费用字段。Harness 配置不得把供应商 Key 发给员工；只有代理内部使用供应商认证。

停用员工阻止新的代理 HTTP 请求，已经放行的单次请求可结束。它不保证当前整轮需要的后续模型请求仍可发起；这与现有运行控制「允许当前轮收尾」的边界区别必须体现在 UI 文案和回归测试中，不由代理修改 Turn 终态。余额不足通过原错误路径反馈，补充后不自动恢复任务。

Key 轮换是内部维护动作，替换后旧 Key 新请求失效，已授权请求正常结算。现有 Pi 进程在下一次配置读取/重启时取得新 Key，不承诺中途热替换。员工、账户、模型的历史保留不得级联删除账单。

## 8. 代码结构

模块代码按以下职责组织。模块内沿用 controller → service → repository/integration；测试与代码同目录，不为小领域再建独立 module。

~~~text
backend/internal/
├── app/server.go
├── platform/
│   ├── controller/                  # 原员工、自备模型选择、账号入口
│   ├── service/                     # 原注册、员工、运行配置用例
│   ├── repository/                  # 原事务发起方与接入适配
│   └── modelproxy/
│       ├── README.md
│       ├── controller/
│       │   ├── resources.go         # 官方模型管理、积分和用量 API
│       │   └── proxy.go             # 原生模型协议 HTTP 入口
│       ├── service/
│       │   ├── models.go
│       │   └── keys.go              # 内部 Key 能力
│       ├── repository/
│       │   ├── models.go
│       │   ├── calls.go             # 准入、用量、结算与补偿
│       │   └── credits.go           # 钱包与流水
│       └── integration/
│           ├── upstream.go
│           └── usage.go
├── manager/                         # 仅增加模型来源配置适配
└── webgw/                           # 现有渠道边界
~~~

其他位置：

| 位置 | 接入职责 |
| --- | --- |
| backend/contracts/modules/platform | 扩展既有模型/员工 schema，新增官方模型与积分契约 |
| backend/contracts/internal | 内部补偿契约；必要的运行配置来源扩展 |
| backend/internal/platform/controller、service、repository 的现有员工/模板/认证入口 | 消费窄接口，不复制 Key 或积分实现 |
| backend/internal/platform/service/runtime_models.go | 官方与自备模型的配置装配分支 |
| backend/internal/manager 的配置解析与 Harness 入口 | 支持来源引用，并将可信员工 ID 传给解析能力 |
| event-hub/database/postgres/migrations | 实施时新增版本化迁移，按当时最新编号，不修改旧迁移 |
| event-hub/internal/scheduler/model_usage.go | 通过受限内部接口触发结算补偿 |
| web/src/direction2/api/models.ts、agents.ts | 扩展模型候选、来源和员工引用 |
| web/src/direction2/api/credits.ts | 积分与账单客户端 |
| web/src/direction2/views/PlatformEmployeesView.vue 及模板创建入口 | 同一个模型选择流程，展示首次扣费说明 |
| web/src/direction2/views/PlatformAdminView.vue | 现有用户管理增加余额、赠送、账单与注册赠送设置 |
| web/src/direction2/views/OfficialModelsView.vue | 官方模型管理页面 |
| web/src/direction2/views/MyCreditsView.vue | 用户积分与调用明细 |

API 契约和迁移继续留在组件的既有权威目录，不在模块中复制一份。新模块 README 在实施时说明职责与指向本文的链接，不重复完整设计。共享加密、诊断、鉴权和事务能力通过明确接口复用；不预先建设模块注册器、供应商插件系统、通用配置平台或内部事件总线。

## 9. 精简清单与保留理由

| 已删除/合并 | 结果 |
| --- | --- |
| 员工 Key 四个管理接口和 Key 页面 | 员工创建/模型保存内部处理；运行配置内部下发 |
| 普通用户独立官方模型目录 | 原 model-profiles 增加可选合并参数 |
| 来源切换页/开通向导 | 同一个模型下拉框 |
| 独立积分管理页面 | 并入现有平台用户管理 |
| 官方配置详情、启停、设默认独立动作 | 列表提供编辑数据，PATCH 统一保存 |
| 代理模型发现接口 | 运行配置已确定模型，不增加 GET /models |
| 供应商/渠道/模型池分层 | 每个官方模型一条完整配置 |
| 独立 Key 状态、展示字段、历史和价格版本表 | 生命周期跟随员工，价格保存请求快照 |
| 员工钱包、任务冻结、额度分配、自动充值 | 用户钱包和请求完成后扣费 |
| 任务/会话计费字段 | 按员工、模型、时间查询 |
| 通用幂等表、账单汇总表和异常中心 | 业务唯一键、实时查询、账单内核对 |

继续保留六张职责明确的表。usage 记录尚未入账的过程和异常，ledger 只记不可变入账事实，wallet 提供当前余额；不为了减少一张表而混淆请求执行状态与资金事实。模型和员工 Key 分开，因为同一模型服务很多员工，同一员工 Key 也可调用多个官方模型。

## 10. 迁移、兼容与发布

1. Event Hub 新增迁移创建六表、设置单例、为既有账户初始化零余额，并扩展员工及版本表引用；既有自备模型值保留，不猜测旧员工 payer，不自动补注册赠送。
   迁移完成后若旧 Backend 仍接到注册请求，该账户仍按零余额显示。首次绑定员工或收到管理员赠送时补建钱包。
2. 所有 Backend 先具备两类模型读取、权限、员工版本和运行配置解析能力；实际代理能力未就绪时不得开放官方选择或伪装已可用。
3. 更新前端模型联合类型、选择保存与用户管理；默认未传 include_platform 的旧自备管理接口保持原行为，显式核对全部选择调用方。
4. 普通和模板创建采用同一 Key 初始化；注册两条路径采用同一赠送规则。测试未知提交结果及回滚，不留无 Key 的半完成新员工或无流水余额。
5. 官方功能启用后，不能将只认识旧必填 model_profile_id 的 Backend 混入承接流量。回退前关闭新选择/调用入口并停稳受影响调用，保留账单和新引用数据，不直接删表回滚。
6. 迁移编号以实施时最新序号为准；结构快照按现有工具生成。Backend/Engine 不执行迁移、不检查 schema 版本来阻塞启动或接流，缺结构时当前请求失败，补齐后恢复。

首版对应 Event Hub 迁移 `000018_platform_model_proxy.sql`。验收使用隔离 PostgreSQL 和测试供应商。

## 11. 实施验收

以下是验收范围，实际执行结果见下一节。

| 范围 | 验收 |
| --- | --- |
| 模型选择 | 合并后分页/总数/有效默认一致；官方不出现在自备编辑列表；停用项、无默认、已有选择不可用不静默替换 |
| 新用户 | 注册事务发一次默认赠送；0 赠送、修改设置、注册回滚和重复登录均正确 |
| 员工创建 | 空白/模板创建及 Key 同事务，失败不留半成员工；模板不携带原员工或作者 Key |
| 存量员工 | 明确保存模型后首次绑定；纯改名、浏览、聊天和后台触发不能猜测 payer；并发绑定只有一个结果 |
| 固定归属 | 多用户、多渠道、聊天、任务调用同一员工均扣同一钱包；切换模型不转移扣费账户 |
| 并发与精度 | 正余额准入、允许扣负、原子减余额；整数精度、缓存去重、一次取整、零费用和溢出边界 |
| 幂等 | 注册赠送、人工赠送、重复补偿只入账一次；不同内容复用赠送键冲突；SDK 新调用分别计费 |
| 价格与配置 | 调用开始后改价不影响本次；新调用使用新价格；地址/凭据变更须重测，原供应商密钥不被转发到新地址 |
| 流式协议 | 三协议真实 handler、工具字段透传、流式最终用量、客户端取消、超时及错误响应；不把分片累计量重复相加 |
| 恢复 | 用量已落库而扣费失败能补偿；崩溃/未知用量进入待核对；人工处理与迟到用量竞态不重复扣费 |
| 权限 | 跨工作区、他人钱包、过期管理权、成员退出、新旧 Key、Cookie 与内部服务凭据混用均按边界拒绝 |
| 生命周期 | 停用员工/模型阻止新代理请求，已有单次调用完成；不承诺整轮继续；轮换后配置刷新可取得新 Key |
| 兼容与 UI | 自备模型、已有会话及员工版本回归；不设默认模型也可使用员工明确所选的可用模型；余额、费用状态和首绑提示真实一致 |
| 迁移 | 空库重放、带存量账户/员工升级、外键和唯一约束、多 Backend 并发、失败回滚及既有数据保留 |

验收使用真实的 HTTP 接口、原生模型流及 PostgreSQL。浏览器用例额外启动 Engine 和 Pi，并使用构建后的 Web 页面。

## 12. 实施验证

以下用例使用隔离的 PostgreSQL 16、真实 Backend HTTP 路由及本地确定性测试供应商。

- Backend 的 `make check` 校验三个 OpenAPI 入口，执行 Go 静态检查和真实 PostgreSQL race 测试。专项覆盖三种原生流式协议、注册赠送、测试回执、员工固定扣费、并发透支、请求价格快照、补偿重入、Key 轮换、存量升级和模板创建。
- Event Hub 的 `make check` 校验版本化迁移、隔离数据库测试及结构快照。启用自动结算补偿时，Event Hub 设置 `EVENT_HUB_BACKEND_URL`，Event Hub 和 Backend 配置同一独立的 `KIKO_HUB_TOKEN`。只运行迁移的 Hub 没有后端补偿连接。
- Chrome 使用真实页面注册、测试保存官方模型并创建员工。原生 Pi 的聊天和任务分别走同一个员工代理 Key，在同一账户各入账一笔。浏览器随后从平台用户管理补充积分，并在「我的积分」刷新余额和用量明细。

浏览器验收在仓库根目录运行。先按组件说明构建 Web、准备 Pi、Sandbox Runner 和 builtin MCP，并安装可用的 Playwright 与浏览器，再执行：

~~~sh
cd backend
KIKO_PROXY_WEB_DIST="$(cd ../web/dist && pwd)" \
KIKO_PROXY_BROWSER_SCRIPT=web/test/browser-model-proxy.mjs \
go test ./internal/platform/repository -run '^TestModelProxyBrowser$' -count=1 -v -timeout 120s
~~~

使用与 `make check` 相同的 `BACKEND_TEST_DATABASE_URL`。可用 `KIKO_PLAYWRIGHT_MODULE` 指定 Playwright 安装路径，或用 `KIKO_PLAYWRIGHT_CHANNEL=chrome` 使用已安装的 Chrome。

三种协议的代理与计费在测试供应商上验证。浏览器到原生 Pi 的聊天和任务使用 Chat Completions 流。生产供应商的响应差异须通过官方模型页面的流式用量测试核对。验收未连接收费供应商或迁移正式数据库。
