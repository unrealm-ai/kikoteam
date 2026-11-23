# web · AI 员工工作台与管理控制台

**任务与协同中心（2026-11-20）**　任务导航默认打开三列协作事项看板，可切换任务记录、人工确认和任务空间。人工确认默认列出当前工作区待处理摘要，也可切换已处理记录。仅指定 Web 确认人或已有来源任务读取权限的人可查看问题、补充与答复详情；其他成员只能看状态等最小摘要。顶部提醒显示当前工作区未结束的确认数量，点击打开同一份只读清单。四页共用原版的导航和展示样式。仅真实模式按工作区权限读取 Manager 数据；本地演示保留任务导航和深链接，但只显示连接真实后端的提示，不请求任务中心接口，也不显示待确认数量。任务详情按后端权限和最新执行状态提供原有操作，包括补充输入、停止和归档；任务记录还可按编辑时的版本修改标题（冲突保留草稿），或撤回符合条件的普通排队输入；不撤回确认回执输入，也不在只读检查视图提供改名或撤回。事项、人工确认历史和空间管理在此入口只读，不从工作区历史代为答复。接口与权限见[任务记录设计](../backend/docs/task-records.md)。

前端独立构建为静态文件，与 backend 打包在同一容器。浏览器访问平台唯一的公网域名，由 backend 路由工作台页面和静态资源；业务请求和 SSE 订阅也使用该域名。采用 Vue 3 + TypeScript + Naive UI，当前页面保留 `src/direction2/` 结构。

本组件包含工作台页面与显式本地演示 Mock API；生产模式下接入 Kiko Backend。实际接口以 [Backend 契约](../backend/docs/http-api.md) 为准。

登录页和工作台显示「KIKO 企业级AI员工云平台」，页面标题保留视图及工作区名称，导航和操作统一使用「AI 员工」。Web 使用 SVG 品牌图标，浏览器标签图标提供深色系统主题样式；路由与接口标识不随界面改名。

## 职责与边界

- **工作台与对话**　展示员工、会话、历史和附件，支持流式回复、委派任务以及任务执行期间继续对话。
- **任务与事项**　提供任务详情、进度、补充输入、取消、人工答复、事项交接和产物下载。分别展示执行结果、人工等待和渠道送达状态。
- **员工与渠道设置**　Agent 只有一套员工配置；Web 与各渠道新建主对话使用该员工所选模型，不回退到工作区或官方默认。chat 的 Harness 与工具边界仍由平台固定，任务运行配置不变。后续业务界面再承接渠道、群范围、触发器及人工协作配置。
- **AI 员工与资源库**　管理模型、员工配置版本、能力包、Skill 目录、Plugin 及共享或独享的插件连接，显示可用性与授权状态。员工仅通过包绑定资源；同员工多包同插件共用账号，各包工具范围可不同。工作区能力包模板复制后独立；未实现功能隐藏，沿用现有样式与 token。Agent 保存即生成版本，无独立发布流程；员工模板属于后续业务，Sandbox 使用平台默认方案。项目文件以相对项目根的路径展示，异步附件使用稳定引用。[会话文件](../backend/docs/conversation-files.md)
- **身份与状态**　提供工作区切换、成员邀请与授权管理入口。按 backend 返回的可执行操作呈现界面，权限裁决归 backend。配置已保存、机器人已认证、连接在线和消息已收发分别取对应事实展示。

**AI 员工市场（2026-11-18，已实现）**　新增入口统一命名为「AI 员工市场」，提供官方精选与工作区模板。真实模式下，主导航首项和无指定视图的入口均为「AI 员工市场」；品牌标识也回到市场。隐藏「首页」导航入口，但保留原页面与显式 `view=home` 链接。未接入市场的本地 Mock 默认打开 AI 员工名录，仍不请求市场接口。工作能力由模板完整预设，从模板创建时只读展示，无需再次勾选；向导为「岗位设定、模型与接入、确认创建」。模板详情、创建后的账号接入与首次试用见[专项设计](../docs/product/digital-employee-market.md)。

本轮简化复核：模板保存和上下架复用 PATCH，来源与使用范围复用员工 GET/PATCH；创建不自动重试，岗位预览在本地计算，接入状态只读查询。真实试用沿现有任务界面，不增加满意度反馈或“已验证”徽章，详见[技术设计](../backend/docs/agent-template-market.md)。

**平台管理员（已实现）**　根据当前用户 is_platform_admin 展示官方模板管理；平台管理员可打开「设置 → 平台管理 → 用户管理」，允许管理员给其他已注册用户授予或撤销平台管理员资格。首位成功登录用户由后端一次性初始化；工作区模板仍按 owner/admin 展示管理入口。账号角色和接口规则见[身份设计](../backend/docs/platform-auth.md#平台管理员)，不在前端决定谁是首位管理员。

「设置 → 平台管理」也提供「邮件与注册」和「集成设置」。SMTP 支持网易 163 邮箱（发件邮箱和客户端授权码）及标准自定义 SMTP（发件邮箱、密码或授权码、DNS 主机和端口，可使用自建服务）。发件邮箱同时作为登录名；端口 465 使用隐式 TLS，587 使用 STARTTLS。旧配置不符合此表单时不会自动改写，可停用原配置或明确替换。SMTP 测试只发往当前管理员的账户邮箱，测试草稿并不保存配置；SMTP 接受不等于送达。首次未配置时仅草稿勾选启用邮件和注册验证；保存启用配置（含迁入旧部署配置）必须先测试同一 SMTP 草稿，测试有效期为 10 分钟。端点、邮箱、凭据或配置版本改变时须重测，单独调整注册验证不影响测试。保存后以服务端生效配置为准。同一 SMTP 目标的旧密码可留空保留，目标改变或切换 TLS 模式时不能沿用。停用邮件前须有可用密码登录的平台管理员。GitHub OAuth 只供插件账号授权，不是网站登录方式；同一 Client ID 允许保留或明确清除已保存的 Client Secret；更换应用时不能沿用旧密钥。旧部署环境配置可由管理员在相应页签主动迁入数据库。两个编辑器都不回显密钥，保存使用配置版本；冲突时可读取新版本并保留已编辑字段，密钥不会因合并草稿而被改绑到新连接目标。

**官方模型与积分（2026-11-18，已实现）**　官方模型加入员工及模板创建的原有模型下拉框，不新增 Key 管理入口；新增官方模型管理和「我的积分」，赠送、余额与账单操作并入现有平台用户管理。员工模型选择、首次扣费绑定与异常展示见[产品/UI 设计](../docs/product/official-models-and-credits.md)，接口与代码落点见[技术设计](../backend/docs/model-proxy.md)。Chrome 真实交互及后端联调步骤见 [实施验证](../backend/docs/model-proxy.md#12-实施验证)。

**验收场景**　用户切换工作区时，旧请求和旧 SSE 订阅的迟到数据不能进入新页面；报告已完成但渠道发送失败时，用户仍能在任务页下载获准的产物。

Web 私聊支持[项目文件树和附件](../backend/docs/conversation-files.md)，包括拖入上传、逐项重试和历史卡片下载。

## 对话与事件订阅

目标业务组织是一个 Conversation 含唯一主 chat、多个 Task，每个 Task 关联唯一 task Session，详见[生命周期设计](../docs/architecture/conversation-lifecycle.md)。停止主对话不停止任务；任务停止/归档只作用自身；前端不提供主对话归档入口；后端手动归档 API 保留原有停稳后归档规则，任务归档只作用任务自身。页面须区分已受理、已归档和运行资源已释放，不能从 Turn 完成推断环境已回收；Web 不直接控制 Engine/Runner。Web 固定私聊和任务查看已接 Manager；自动任务结果回告按 `source_kind: task_report` 展示，实际验收见下方会话呈现记录。

SSE 按最后处理的可靠事件序号续读，过滤重连时的重复事件。临时文本用于即时呈现，轮次结果、后台进程概况与后续产物发布依据已提交的事件或查询结果。用户可以在后台任务进行时继续对话。进度、人工请求、业务结果和渠道送达分别展示。

会话字段与消费规则以[会话事件协议 v1](../backend/docs/session-events.md)为准：直接读取平铺事件，按 message_id / tool_use_id 合并消息与工具快照，正式正文替换临时预览；事件中的 input_seq 与可靠事件 seq 分别表示输入顺序和历史位置。type 是预设计目录，接入时按实际服务已启用的子集实现，不要求先展示或模拟全部类型。

## 本地运行与构建

要求 Node.js 24（本次验证 24.19.0），依赖版本由 package-lock.json 固定。

```sh
cd web
npm ci
npm run dev          # http://127.0.0.1:15173，显式 Mock API
npm run typecheck
npm test
npm run build        # 独立生成 dist/，包括第三方许可证
```

默认开发服务器使用 Node 内存 Mock，重启即重置；任务与协同中心不提供开发 Mock，请使用下方真实联调方式。Mock 不包含在静态生产服务中。`npm run preview` 仅预览静态产物，不提供 Mock API。真实联调使用：

```sh
DENTITY_USE_REAL_BACKEND=true DENTITY_CONTROL_PLANE_URL=http://127.0.0.1:18300 npm run dev
```

这两个环境变量在 Vite 启动时读取；前者默认关闭，后者默认上例地址。真实模式禁用 Mock，不回退为模拟成功。真实模式的账号与工作区使用 `/api/v1` 契约（`api/platform.ts`），只使用 HttpOnly Cookie，不发送旧 Mock 的 localStorage Bearer；所有写操作附带 CSRF 防护头。生产静态构建默认真实模式。其他业务保留原 `/api` 客户端，未接入的接口失败不会回退 Mock，也不会阻断登录和工作区设置。

### 本期 Platform 流程

- 邮箱密码登录优先。注册是否需要验证码由服务端 auth/options 决定：无需验证时密码必填；要求验证码时密码可选。邮件可用时保留验证码登录和重置密码；Google OAuth 在真实模式禁用。真实模式不显示或接受调试验证码。
- 新注册账号默认创建自己的工作区。旧账号无工作区时可以创建，或打开邀请链接加入；已有账号从左下角菜单创建/切换工作区。
- 工作区设置使用原版 SettingsView，提供「工作区设置」和「成员与权限」两个页签。真实模式下仅实例平台管理员能看到第三个页签「平台管理」。该页签不在主导航展示，不依赖工作区详情查询成功；原 `view=platform-admin` 链接会转到该页签，非平台管理员则回到普通设置。名称、描述和成员角色/启停通过版本号更新，成员与邀请分别分页。版本冲突时保留基本信息草稿以供核对。
- 邀请默认创建可多人复用的通用链接（member，默认 7 天，可选 1–180 天），仅在 auth/options 的 email_code_enabled 为 true 时提供可选邮箱输入（加载失败默认隐藏）。填写一个或多个邮箱后生成一条限定名单的链接，所有者可指定统一的初始角色。每个邮箱独立接受、投递和重试；不提前添加成员。登录/注册后自动接受当前邀请；已知失效链接显示服务端提供的邀请人邮箱，未知链接提示联系邀请人，不依赖不可信 URL 邮箱。
- 邀请页面支持 `/?invitation_token=…` 和 `/invite/…`；部署静态入口须提供 SPA 回退，但未知 `/api` 必须保留 API 错误。不要在访问日志中记录邀请 token。
- 保存成功与列表刷新失败分别处理，成员权限由后端裁决。离开未保存工作区设置时使用统一草稿守卫。


## 工程初始化与验收

2026-11-16 初始化独立前端工程与 AuthView，源码、资源、测试和设计材料完整保留。

迁入修正原 monorepo 的构建、测试和许可证路径，显式锁定 TypeScript 5.8.3（与来源安装版本一致）、测试直接使用的 esbuild 和配置需要的 Node 类型。原 lockfile 的自动 peer 安装会选到不兼容 vue-tsc 的新版 TypeScript，已避免该漂移。许可证生成器及补充证据在 `scripts/`，Apache LICENSE、原 NOTICE 和资源署名保留；上游 vdirs 版权证据待确认的问题未被本次迁移消除。

已验证：25 项源码/Mock 回归测试；本地 Vite 首页 200，未登录 auth/me 返回 401 且带 Mock 标识，测试进程退出。构建包含类型检查。浏览器完整交互和真实 Backend 联调尚未验收。

完整 Kiko ESLint/Prettier 门禁尚待接入；本次不放宽仓库规则、不批量重排来源代码，不能把类型检查和现有测试等同于完整静态检查通过。

本批验收：46 项前端测试通过，另有 1 项 opt-in 真实 HTTP 集成测试默认跳过。此前 OTP 版本该集成测试已单独执行通过，本次密码注册及通用邀请变更未启动后端重跑。包含 Cookie/CSRF 适配、challenge 传递、无调试码、注册响应不捏造工作区、错误请求标识、204 退出，以及真实 Vue 组件挂载验证有效/撤销/未知邀请页面。真实前端 client 经 Vite 代理对隔离 PostgreSQL 后端完成注册、登录、工作区创建/改名、邀请生成/撤销/接受与退出；邮箱使用测试文件投递器，无外部邮件。

浏览器只完成真实登录与注册页可视检查，之后 Chrome 调试连接中断，完整点击链路未验收。组件测试和 HTTP 集成不能替代该项。typecheck 与生产构建通过，仍有既有大 bundle / vdirs 署名警告。

可选真实集成测试需先按 backend 文档启动隔离 HTTP 测试 fixture，再启动本地 Vite 代理：

```sh
KIKO_UI_TEST_ORIGIN=http://127.0.0.1:5173 KIKO_UI_TEST_MAIL_DIR=<fixture 输出的测试邮件目录> node --test test/platform-live.test.mjs
```

该测试只接受 127.0.0.1；勿指向生产后端或真实邮箱投递器。

### Mock 账号初始化

本地 Mock 不再预置可登录账号或当前用户，也不提供示例账号快捷登录。请自行注册；Mock 注册沿用演示工作区初始化行为，真实 Platform 注册响应不捏造工作区。人员目录等非认证演示数据保留。认证回归测试通过接口自行注册独立 fixture，不依赖默认账号。

### 无 SMTP 的密码注册

真实登录页通过 `GET /api/v1/auth/options` 获取注册和验证码能力，不从失败响应猜测邮件配置。默认使用邮箱密码登录；允许密码注册时只提交 `method: password`、邮箱、姓名和密码，不提交验证码或 challenge。密码为 10–72 个 UTF-8 字节。账号邮箱验证状态以服务端 `email_verified` 为准，不默认已验证。

SMTP 未启用时隐藏验证码登录/找回密码入口及密码注册的验证码表单；启用验证码注册策略时保留原 OTP 注册流程。能力读取失败时禁用提交并允许重试。未验证邮箱不能接受定向邀请，403 `email_verification_required` 明确引导通过验证码重置密码完成验证，邮件不可用时联系管理员。Google OAuth 仍未开放。此批未连接真实 SMTP，也未改动运行中的服务。

邮箱输入在 trim 后须满足基本邮箱格式、域名包含点且不超过 254 字符；注册、密码登录和验证码按钮均有无效/超长邮箱不发送业务请求的 Vue 组件回归测试。

### 邀请入口安全与自动加入

通用链接不要求 SMTP 或邮箱验证；定向邮箱邀请仍要求验证。邀请注册/登录成功后自动调用接受接口，并选中响应的工作区；接受失败独立显示，不把已成功注册改成失败，可重试或进入自己的工作区。过期/撤销/未知链接继续使用服务端联系人或安全兜底。

邀请 token 在页面读取后立即从地址栏移除，仅在当前页面内存保存，不写 localStorage/sessionStorage、不输出日志；静态入口设置 no-referrer。刷新或关闭页面后需重新打开原邀请链接，页面明确提示。部署访问日志仍应屏蔽入口 URL 中的邀请 token。生成链接请只分享给信任的人，撤销后不再允许新成员加入。

邮件能力未开放时只生成通用邀请；历史定向邮箱邀请仍正常显示并可撤销，不因隐藏新建入口而丢失管理能力。

### 定向邀请邮件与邮箱验证

定向邀请创建成功与邮件发送结果分开：显示 pending/sending/sent/failed/unknown/cancelled，sent 仅表示已发送，不保证送达。邀请记录支持刷新状态；failed/unknown 可对同一邀请重试，unknown 必须先确认可能产生重复邮件。撤销/过期不再接收新成员，已经发出的邮件仍可能到达，最终接受由后端再次验证。

从定向邀请进入的新用户，邮件能力可用时优先验证码注册；现有未验证账号可点“验证邮箱”，预填邮箱进入验证码重置密码。重置成功立即清理前端旧会话，要求使用新密码登录后再自动接受邀请，不把旧会话当作已验证。整个过程邀请 token 仅在页面内存保留，刷新仍需重开原链接。

### Platform 模型配置接入

工作区资源库采用统一的页签、卡片、抽屉与通讯录样式。共享模型池、技能、插件和渠道通讯录共用一个页面入口，每个页签独立读取当前工作区的 Platform API。开发联调需要真实登录会话，启动方式见本页的真实后端开发配置。

支持 Chat Completions、OpenAI Responses、Anthropic Messages。图文测试由后端并行执行，前端请求预算 50 秒，展示各用例返回的脱敏输出、结论和耗时；文字通过且服务端回执未过期才允许保存，图片通过、不支持、不确定均独立展示。修改任何配置或凭据会清除回执并取消当前等待，迟到响应不恢复保存按钮；切换工作区、关闭编辑器或离开页面也丢弃旧测试结果。后端仍必须重新校验回执与配置、版本及权限。

模型表单只显示一个 API Key 输入框。新建、原配置没有凭据或更换 API 地址时必须填写有效的密钥。已有凭据且地址相同时，留空会省略 `api_key` 并沿用服务器凭据，输入新值会在重新测试通过后替换。启停、设为默认和撤销凭据都携带资源版本。撤销后显示待重新测试，恢复使用需要新的凭据和有效测试回执。owner/admin 可管理，member 只读。

模型组件回归覆盖三个协议、凭据保留与替换、跨地址保护、测试回执失效、图片和文字的独立结论、工作区切换和角色权限。运行入口为 `node --test test/model-components.test.mjs test/model-contract.test.mjs`（在 web 目录执行）。

真实客户端联调已在独立临时 PostgreSQL 后端与 loopback Mock 模型服务完成：密码注册、工作区、三协议测试和保存、回执配置篡改拒绝、保留凭据编辑复测、启停、默认和撤销。可通过 `KIKO_MODEL_TEST_ORIGIN`、`KIKO_MODEL_TEST_ENDPOINT` 运行 `node --test test/model-live.test.mjs`；默认跳过，仅允许本地 fixture（拒绝正式开发 18080 端口），无付费供应商请求。此项为真实 HTTP 客户端联调，不代替浏览器点击验收。

上述可选真实客户端测试仅消费外部提供的验收地址，不启动模拟模型服务；独立模拟服务应放在仓库之外，不纳入项目启动链路。

### Platform 员工基础管理

真实模式员工入口使用 `PlatformEmployeesView.vue` 与 `api/agents.ts`，请求 `/api/v1/workspaces/{workspace_id}/agents`；原完整 WorkforceView 与 Mock 设计保留供后续迁移。列表仅依赖员工、工作区角色与真实模型配置接口，不等待目录、渠道或能力包等尚未实现的接口，也不显示虚构运行统计。

本期创建填写名称、模型；岗位职责可留空；创建时不展示岗位行为规范，保存后可在详情补充（对应 instruction）。头像复用原预设选择器。固定 Pi，无员工级思考参数。默认模型只用于新建预选，保存明确的 model_profile_id；工作区默认变动不切换已保存引用。模型可用条件为已启用且已验证，不要求必须有密钥。既有模型后来不可用时，仍允许编辑该员工其他基础字段及停用，不静默换绑模型；重新启用由后端核验模型。

配置写入携带资源 version，显示 config_version；启停不冒充连接在线。创建后继续在详情补充，渠道等后续标签明确禁用，不能把配置保存成功当成 Agent 已可执行。owner/admin 可写，member 只读；最终权限由服务端裁决。详情离开复用统一未保存守卫，工作区变化丢弃迟到响应；写成功与随后列表刷新失败分别表达。

员工详情关闭草稿使用 Naive UI Dialog，生产根 `Direction2App` 提供 `NDialogProvider`。回归测试直接挂载 `App.vue` 验证员工页渲染与未保存确认，不能只在单组件测试里人工包 Provider 掩盖根装配缺失。


### 技能、插件与能力包管理

资源库按卡片展示技能的交付物和插件的空间共享连接状态，支持按来源和中英文用途搜索。内置官方技能和插件均提供中英文展示简介，默认中文，自建资源保留作者原文。详情沿用原有导入、固定版本分配和工具授权流程。资源库已移除能力包模板页签，已有员工能力包和授权继续生效。

- 新建 Skill 支持 ZIP、公开 GitHub（ref 和子目录）、Markdown 文件或文本。改名仅 PATCH 展示名。上传新版本沿用首个版本的导入方式，先校验目录、查看文件清单，再发布。历史手动创建的 Skill 仍可按原方式更新，员工模板来源可查看和改名。历史文件按不可变版本 ID 读取，已有会话沿用创建时的版本快照。
- MCP 资源库管理远程 HTTPS 端点、认证方式、启停和定义历史。资源库可按官方或空间自建筛选。通过「分配与管理」选择员工和能力包。界面显示最新定义版本，账号可见工具只读展示，列表暂不可用也可完成分配。官方资源可以直接分配，或显式复制为空间自建资源。
- AI 员工卡片的“能力包与授权”管理独立能力包，不能直接绑定 Skill 或插件。新会话和新任务采用创建时的最新资源版本。工作区模板可填入新包草稿，复制资源绑定和使用范围，账号由目标员工配置。
- 空间共享账号和员工专属账号经过真实 initialize / tools/list 后保存，不回显密钥。同员工多包复用同插件账号，新任务默认可用该账号提供的全部工具；账号撤销影响全部引用包，但仍允许包改名或停用。
- 使用范围仅支持 Web 工作区成员白名单/黑名单；其他渠道隐藏。固定版本 Skill 与静态认证远程 MCP 的 Engine 执行已接入；配置管理和真实执行验收分别记录，不把配置保存等同于任务执行成功。
- 组件回归测试：`node --test test/capability-components.test.mjs`，覆盖导入凭证、跨工作区迟到响应、只读角色、最新版本展示、账号凭据保留及撤销版本、失效账号下的管理操作。

### 渠道通讯录

从资源库打开「渠道通讯录与身份核验」，选择渠道和绑定的应用。管理员可按 AI 员工和应用 ID 区分同渠道的多个绑定，查看飞书、钉钉已同步的成员、部门、更新时间和规模。搜索、部门导航及分页始终固定在所选绑定和快照版本。手动同步只提交所点来源，页面轮询已开始的同步。企微按关键词查找联系人，支持转交验证码、查询状态及取消。网页来源展示工作区成员，邮箱邀请入口位于工作区设置。

### Web 固定私聊与 Manager 接入（2026-11-17）

真实模式对话入口使用 `PlatformConversationsView`，不调用旧 execution Mock 协议。同一工作区、登录用户、AI 员工只复用一个 Web 私聊 Conversation；无新建会话或主对话归档按钮，未来 Web 群聊单独设计。后端手动归档 API 保持原有行为。固定私聊被手动归档后仍返回原 Conversation/chat，前端显示不可发送提示并保留历史，不自动恢复或新建替代，任务归档规则不变。首次输入创建主 chat，带稳定请求幂等键；网络未知重试沿用原 key/内容，明确 chat_session_exists 才读取唯一 chat 并提交该输入。

每个当前页面只有主 chat 与当前打开的 task 两条 SSE；任务详情同时打开一个。关闭详情不停止任务，停止主 chat 不停止任务。任务列表可见时每 5 秒补偿刷新，隐藏暂停；任务创建/轮次变化也触发合并刷新。列表包含当前 Conversation 的任务，不把每条记录都宣称为主 chat 委派。任务补充和停止使用 Manager，停止受理不等于停止已确认。

历史首次 backward 获取最近 50 个可靠事件，页内正序显示，next_cursor 向上加载旧页；首次 resume_cursor 立即连接 SSE。旧页不改变实时游标，消息按 message_id、工具按 tool_use_id 归并最新快照，事件按字符串整数 seq 排序去重。切工作区/员工和退出页面取消旧请求、关闭流，忽略迟到响应；撤权后停止订阅并清除已载事件。当前只有可靠完整消息，不伪造 token delta；任务 Turn 结束不表示业务任务完成。自动任务回告、附件和更多渠道尚不在本批。

验证入口：`node --test test/manager-conversations.test.mjs`，覆盖真实 Vue 组件固定私聊入口、Cookie/CSRF、双流切换清理、倒序历史与实时去重、首次输入未知结果幂等重试和跨工作区迟到响应。另跑 `npm run typecheck`、`npm test`、`npm run build`；浏览器和实际后端验收独立记录，不以组件模拟代替。

### 会话呈现与任务回报（2026-11-17）

`ManagerSessionPanel` 使用独立的 Markdown、工具调用与任务回报组件。员工回复支持标题、列表、表格、引用和代码块；用户输入保持原文。Markdown 禁用原始 HTML，链接只允许 HTTP(S) 并隔离新窗口，图片只展示替代文字，不自动请求外部资源。

工具默认显示名称、目标、真实状态和耗时，展开后通过输入/输出标签查看参数、命令与返回结果；原始响应保留在折叠区。只读取 `available` 的输入/输出，隐藏、无权限、待返回和不完整结果分别说明。嵌套 MCP 包装按结构解包，同时保留外层错误判断；命令非零退出码显示失败，后台进程运行中不显示执行成功。

六种 `task_delegation_*` 操作映射为“触发任务”“查看任务进展”等产品文案。任务卡只对当前 Conversation 已加载的任务提供跳转；桌面详情在右侧，窄屏上下排列，仍只保留主 chat 与一个 task 的订阅。回报卡区分本轮结果、回报处理状态、未知工具结果、后台进程与排队输入；只折叠 `input_id`、`turn_id` 同时匹配且没有其他输入/工具插入的主对话回复，原始回报可随时展开。没有产物下载接口时不生成下载按钮，不从 Markdown 路径构造资源请求。

验证：新增真实 Vue 组件回归覆盖 Markdown/XSS、隐藏字段、嵌套 MCP 错误、命令输出、任务跳转、回报归并边界、异常回报及键盘标签切换；执行 `npm test` 和 `npm run build`（包含类型检查），生产依赖 `npm audit --omit=dev --audit-level=high` 通过。独立 Chrome 使用已保存销售示例的 47 条真实事件回放，检查 1600×1100 与 390×844 布局、输入/输出切换、任务打开/关闭，无页面异常和横向溢出。浏览器回放隔离认证与 API 响应，未发送新输入或修改用户配置；它验证生产前端呈现，不替代登录和实时后端全链路验收。

### 飞书员工配置首批（开发中）

`PlatformFeishuPanel` 使用真实员工级 `/api/v1/workspaces/{workspace_id}/agents/{agent_id}/channels/feishu` GET/PUT，仅 owner/admin 开放。App ID/Secret、群 @/关键词/正则及飞书 open_id 使用范围均按员工独立保存。空 Secret 更新时省略，不回显已有密钥；员工/工作区切换取消请求，迟到响应不得写入新页面。

配置 v、凭据代际和规则版本独立显示；仅 Hub 报告 applied/connected 且三项版本均一致才标“已应用当前版本”。保存不证明凭据有效，connected 不证明消息收发全部验收。刷新状态不覆盖编辑草稿；版本冲突需显式重新读取。默认禁用、空 allowlist、按需历史默认关闭，可显式开启：从上次有效触发之后、本次之前最多100条，首次取近期；历史与当前触发合并为一条 user 输入，边界由原生上下文提交回执确认。失败降级仅当前消息，不跨群或降级读取话题主线。群成员使用独立开关默认关闭，开启不受私聊白名单限制，黑名单和群范围仍优先。飞书 open_id 不绑定 Web 成员。扫码、部门选择、跨群共享及其他渠道未开放；不把旧 Mock 表单当作真实功能。

前端回归入口 `node --test test/feishu-components.test.mjs`；覆盖初始安全默认、Cookie/CSRF、留空保留密钥、乐观版本冲突、应用状态版本核对、成员不可读取、关闭草稿确认、迟到响应及撤权。真实平台/Hub 与浏览器验收另行记录，组件模拟不代表渠道真实租户验证。

### 飞书部门层级选择与快照有效期

飞书渠道中的通讯录选择仍为当前应用范围的过渡能力，不代表跨渠道统一身份选择已经完成。允许、拒绝部门分别按单条规则编辑“包含子部门”；新选择及旧 ID 默认仅直接成员，已有递归值原样保留。提交显式携带两侧规则数组及 ID 镜像；清除一项或更换应用时同步清除规则，不能留下隐藏授权。

部门视图从可见范围根（含授权范围内缺父的部门）下钻，面包屑返回重置分页。根搜索覆盖全部可见部门，进入部门后的搜索仅限直接子部门；查询及游标绑定当前快照。失效快照不展示候选，但保留已有未解析规则及递归状态，可显式移除，不能改递归开关。新快照有效期 24 小时，页面只信后端 expires_at，不自行延长旧快照。

回归覆盖旧 ID 默认直接、两侧递归值保存、失效保留与移除、父节点分页与面包屑重置；测试使用受控响应，不调用真实飞书或发送外部消息。

### 三渠道配置界面（开发中）

现行 Workforce 员工页提供一次提交的「验证并保存」和「验证并更新凭据」。企微扫码后保存结果不明确时，重试先读原员工绑定，再决定是否提交本次凭据。绑定、连接、权限及目录状态分别展示。员工详情每 4 秒读取状态，隐藏时暂停，恢复可见时立即读取。后台创建的绑定进入列表时保留当前编辑草稿。

飞书权限表读取当前绑定和凭据代次的后台观测，扫码和手填共用。缺失权限有飞书补授权入口。扫码新建飞书应用默认申请全员通讯录范围。飞书和钉钉渠道页独立显示通讯录可用性，来自后端返回的有效完整快照。同步期间仍有有效快照时显示可用，缺少观测时显示未确认。企微成员验证对话打开时自动检查回码结果。

员工页提供飞书、钉钉、企业微信独立入口，复用真实配置面板并请求对应 /channels/{provider}。钉钉使用 Client ID/Client Secret，企微使用 Bot ID/Bot Secret。切换渠道取消旧请求，密钥不回显，版本冲突保留草稿。换绑弹窗读取受影响的名单和规则，管理员确认后保存，旧代次引用在运行鉴权中失效。只有当前连接代际的 connected 且渠道启用才展示“已连接”，不从配置保存/applied 推断连接成功；首可信消息自动确认身份不要求额外验证操作。

非飞书不显示关键词、正则及飞书扫码诊断。钉钉不显示历史开关，企微仅显示群近期历史能力而不宣称话题支持。飞书与钉钉复用各自应用的真实目录快照 API；企微为独立联系人搜索与管理员转交验证码映射，不提供组织树。未映射搜索 ID 不可加入使用范围，映射后的 callback ID 仍须明确加入草稿并保存。钉钉扫码接入走真实 registration API，ready 后明确确认保存，不从 consumed 推断已连接。不复用 Mock API，也不将联系人映射与机器人连接确认混为一谈。真实渠道端到端验收仍受未绑定环境限制。

### Employee unified usage policy (current real UI)

The employee list now exposes **统一使用配置**, backed by `GET/PATCH .../usage-config` and its own CAS version. Access (including default-on group allow-list bypass with deny priority), channel-qualified user/department/group selectors, Web/Feishu keyword and regex rules, and supported group-history channels share that resource. External subjects retain binding ID + identity epoch; unresolved old subjects remain visible and removable rather than silently becoming new-application identities. Web group candidates come only from the current member-visible paginated group API; external group candidates are observed-message evidence with server search and truncation warnings, not complete membership lists. No Web group chat UI is added here.

Channel forms now write credentials/enablement only. The former independent Web access editor is removed. Feishu/DingTalk snapshots and verified WeCom callback identities are selected inside the unified employee policy. WeCom search IDs alone cannot be selected. Component tests cover real endpoint request contracts with mocked HTTP responses; they do not claim a bound external tenant or real external delivery.

#### 2026-11-18 local integrated acceptance

- Final Web suite: 156 tests, 154 passed and 2 live-environment tests skipped; typecheck and Vite build passed. Web group candidate tests use the real `{items,total}` response, including 101 items across two pages and permission revocation. Browser verification on the existing local service confirmed the unified policy entry, default-on group bypass, empty member-visible Web group candidates, all three unbound channel forms, and read-only Feishu/DingTalk registration empty states. No external application was created or message sent.
- Runtime database 26→36 was rehearsed on an isolated restored backup before the local three-service restart. Existing Input, Turn, conversation and account columns matched before/after hashes. Runtime retained 4 processed Inputs, 4 completed Turns, zero tasks and zero external bindings.
- The existing employee's old Web policy is intentionally not migrated into the new unified allow-list. Its prior conversation remains readable, but a new private send returns 403 until an administrator explicitly configures its usage scope. No existing employee permissions were changed during acceptance. An isolated fresh-employee regression verifies creator-only Web permission plus default-on group bypass and successful private input admission; it does not claim an actual model response. Current external directory/provider behavior remains validated by mock/isolated integration tests, not a live bound tenant.

### TaskSpace worktree UI

The **任务空间** tab reads the owner-scoped TaskSpace list, details, grants and tasks. The following describes the write capabilities implemented before the task navigation became read-only on 2026-11-20. An optional business key opens or creates a space without granting another employee implicitly. The detail view explicitly grants/revokes an employee's ability to execute tasks in that space with the current space version; this does not edit employee usage scope or capability packages. New tasks use `task_space_id` and never fabricate a source conversation. Task lists filter by space and reuse the existing task detail/execution UI.

Loading, empty, permission-revoked, stale-workspace responses, cursor errors, version conflict and uncertain-write outcomes have explicit handling. Failed creates are not automatically retried; a deliberate same-body retry retains its idempotency key. Revoking then restoring a grant does not make old tasks resumable. There is no hook, handoff, confirmation, materials or cross-owner sharing UI in this increment. `test/taskspace-components.test.mjs` tests the HTTP contracts and UI states with mocked transport; this is not a live browser/model acceptance claim.

TaskSpace increment verification: `npm test` reports 171 tests (169 passed, 2 live-environment tests skipped), including 15 TaskSpace component scenarios. `npm run build`, a final `vue-tsc --noEmit`, and `git diff --check` pass. No real-browser acceptance was performed for this new tab in this worktree; the existing local services were not restarted for this frontend change.

### Automation management UI

The employee card's **自动化任务** panel uses the Manager trigger APIs. New schedules and Webhooks default to enabled, with an option to create them paused. Task schedules expose empty/fixed/calendar-period business keys (minute/hour/day/Monday-based week/month/year) with an explicit IANA timezone. Fixed reminders require notification targets. The **接收外部事件** option saves an employee prompt and optional result groups. On creation, the panel shows the callback URL and first generated credential together with separate copy controls. Closing the panel clears the secret. GitLab signing mode opens paused so the administrator can copy the URL, then import GitLab’s token and enable the hook in one save. The same event URL accepts multiple event types. The employee uses the prompt and event body to interpret repository, branch and issue fields. [Event webhook contract](../backend/docs/http-api.md#webhook身份回调与-mcp)

Credentials are generated/revoked through the real API and secrets remain only in component memory for the one-time display. Trigger event acceptance is not presented as task completion. Notification delivery and context-admission statuses are separate; unknown delivery has no retry control. Target options come from authenticated backend APIs, never by treating a provider group ID as a conversation ID. Capability packages independently configure trigger allow/deny IDs, with deny precedence; manual member scope is unchanged and empty template allow-lists remain restrictive when copied.

The trigger component tests cover real HTTP request shapes with mocked transport, permissions, version conflicts, one-time credentials, stale confirmations/responses, notification states, fixed reminders, GitHub adapter and each calendar period. Capability component tests cover trigger allow/deny editing, unknown references and restrictive template preservation. These tests do not claim a bound real external provider or actual external notification.

Notification selection now loads signed-cursor group candidates across employee Web group membership and trusted observed IM groups. Feishu also supports an explicit, separately paginated live joined-group lookup; its verified candidates expire after five minutes. Selecting a candidate performs a server-side resolve before adding the returned conversation ID. The UI never inserts the provider group ID into `notification_targets`, and pending/failed/late resolves do not become selected targets. Labels retain provider, candidate ID and evidence so same-name groups are distinguishable. Search filters loaded options only, not an invented global provider search. Repeated cursors stop pagination.

The UI uses TaskSpace business-key ownership and shows the server's `next_due` after save. The generic command Webhook and GitHub Issue adapters retain their existing URLs and signature protocols. Event hooks accept a GitLab signing token in signing mode and generate one-time credentials for the other sources. Trigger records show acceptance, execution and group delivery separately.

Web notification candidates follow employee membership, not administrator membership. Workspace owner/admin configuration may read necessary group metadata without joining the group; this does not grant group-message reading rights. The backend separately enforces membership when accessing group messages.

Final automation UI verification (2026-11-18): production build and the complete 195-test suite passed (193 passed, 2 live-environment tests skipped, 0 failures). The 22 trigger component scenarios include the final employee-group metadata semantics, candidate pagination/resolve and duplicate credential confirmation. Commit `72e3bf5947a3c7d91d7916229b110e676466dd6c` passed all five remote CI workflows. These are not real-browser or live external-IM acceptance claims.

### 统一人员与群选择（2026-11-18）

员工使用范围、员工能力包与自动化通知通过 `PlatformIdentityPicker` 复用 Platform 的 `/selectors` 契约；不使用旧 Mock 目录接口。人员按渠道搜索，飞书/钉钉根据服务端能力显示部门浏览及子部门规则；企微仅搜索与管理员转交验证码绑定验证，不提供部门或完整通讯录。未验证的搜索 ID 不进入授权名单。已有人员与群选择分别通过各自批量 resolve 回显名称和不可用原因；单个来源失败不隐藏其他来源的有效名称，解析失败仍保留原引用，可显式移除，不能按同名替换。

真实模式的员工使用范围按渠道、绑定和身份代次回显姓名或部门名；名称解析只影响展示，找不到匹配身份时保留原 ID，不改写授权规则。使用范围卡片与员工详情的未保存摘要使用同一套规则比较，包含子部门或身份代次的变化会被识别，名称回显不会产生草稿。

群按员工所在/已发现群查询，不要求配置人是成员；统一选择器的 resolve 无创建会话副作用。只有通知业务明确选中并重新 resolve 通知目标时才定位/创建对应群会话。飞书支持分页刷新，企微近期群刷新只表示任务已受理，查询状态完成后重读候选；钉钉观察群、企微近期/观察群均不表示完整群目录。已选通知 conversation ID 在名称列表截断或候选失败时保留。

选择器请求绑定工作区、员工、渠道、查询及用途；分页固定目录版本，重复游标/快照变化清空候选，不自动重试写入。跨渠道权限由后端保存和执行校验，不因前端选中就授权。能力包模板不绑定员工外部身份；群共享/人工确认业务与 Web 对话导航不由本组件提前开放。

### 员工群共享策略

员工列表的「群共享策略」复用统一人员/群选择器，保存完整来源身份。信息同步为来源群到目标群或指定真人的单向授权；项目组仅所选群两两双向，不包含指定人，也不跨策略传递。切换项目组前必须显式移除目标，避免暗中扩大授权。列表服务器搜索、分页；策略保存携带版本，冲突保留草稿，不自动覆盖。网络结果未知禁止重复创建，须重新读取核对。

方向预览只展示后端声明的历史能力与不可用原因，不读取消息、不证明外部授权成功；候选出现不保证可读取。失效身份保留并显示，换绑不映射同名群。回归入口 `node --test test/sharing-components.test.mjs`，覆盖来源身份、版本冲突、权限变化、迟到结果、读取能力提示及模式边界。

### 员工与渠道原版布局适配（2026-11-18）

真实员工页恢复原 Workforce 的名录进入全页详情、六个配置页签及返回名录交互；不再以每个业务模块独立抽屉替代详情。保留官方模型/计费确认、模板接入与现有真实接口，业务分区仍各自版本化保存，不能视为全员工原子保存。跨页签、渠道和名录离开均检查当前草稿；子模块草稿由员工页聚合到工作区导航守卫。

未绑定渠道初始仅提供“创建智能机器人”与“绑定已有机器人”两个操作，进入页面不初始化扫码登记；点击创建才展开扫码流程，点击绑定已有才展开 ID/Secret。飞书、钉钉进入时先读取登记，明确没有登记才初始化；失败、未知或中断登记不会自动重新创建。钉钉授权 ready 后按原版自动保存一次；确认结果未知时先读回状态，不因切换页签或刷新重复自动提交。钉钉/企微提供员工名展示和复制，名称仍需在官方页面填写，不捏造名称透传。飞书 connected、钉钉 consumed 后只读刷新配置，中间凭据保存状态继续保留诊断流程。企微通过官方弹窗收取授权结果，必须由用户点击打开以满足浏览器弹窗限制，校验来源、窗口、可选 state、有效期并拒收取消后的回调；使用现有渠道保存接口。保存凭据、Hub 应用和连接成功仍分开显示。

原版综合“入职待完善/告警异常”状态和多执行引擎选项不伪造：真实员工 API 当前以启用/停用、模型可用事实展示，执行引擎仍是 Pi。事项接手范围与按来源员工限制的能力包在员工「能力包」页配置。

本批验证：完整前端测试 229 通过、2 项 opt-in 跳过，包含员工全页及跨页签/渠道草稿、三渠道默认入口、扫码恢复、企微回调来源/取消/过期；类型检查及生产构建通过（保留既有大包警告）。飞书与钉钉登记的独立真实 PostgreSQL 回归 7 组通过，含本地截止不被网络耗时延长；Backend lint 通过。根目录 make lint 尚不支持 COMPONENT=web，不能把类型检查等同完整 Web lint。真实飞书/钉钉 provider 初始化取得授权链接不代表浏览器扫码或租户授权成功；本批尚未做三家真实浏览器授权和消息收发验收。

### 原 Workforce 组件真实接口适配（工作区进行中）

本轮 UI 基准是完整的 Workforce 页面及内部组件，不是仅恢复六页签后继续使用 `PlatformEmployeesView` / `PlatformFeishuPanel` 的简化界面。现已在组件加入 transport ports、真实员工资源快照及独立 CAS 版本、三家扫码/手填接入、统一使用范围和共享策略适配。原样式保留；用户明确变更包含移除解绑、统一连接/权限两个状态、规范凭据字段、去除虚构能力预览、群共享移至权限页签，以及统计/思考档位显示暂无数据。

适配模块不允许将缺失的真实操作回落到 Mock。扫码凭据已保存不等于已连接；企微格式检查不等于凭据认证，保存后由正式连接验证。飞书与钉钉手填新凭据必须取得服务端验证回执。各卡片使用其编辑时的独立资源版本，不因其它卡片刷新而默默升级未保存草稿的版本。

渠道接入卡片在两个模式下共用标题和能力说明，右上角直接切换下方内容。飞书和钉钉创建模式在二维码旁展示本次登记的官方扫码页链接，企微创建继续通过官方弹窗完成。手填模式按渠道展示 ID 和 Secret 两个字段，旁边提供官方后台创建入口。

飞书授权链接带有较多应用配置时，固定尺寸的二维码格子过小。二维码内容超过 500 字符时，码面放大到 256 px，较窄的容器按可用宽度缩放。较短链接沿用原尺寸。组件测试覆盖长短链接、扫码链接完整性以及钉钉的显示尺寸。

主路由已在源码切换到原 Workforce 的真实模式。按用户决定，能力包与触发器暂保留既有真实组件，不在本轮迁移其不等价字段；其它页签采用原组件。跨资源编辑明确要求分项保存，不承诺原子提交。关键词没有渠道总开关，只保留规则逐条启停，@ 触发不受影响。部署与真实浏览器验收仍需单独确认；新增 HappyDOM 与协议测试使用本地替身，不代表真实扫码审批或浏览器验收。

本轮最终验证（2026-11-18）：完整前端 315 项，313 通过、2 项 opt-in 跳过、0 失败；类型检查与生产构建通过（保留大包警告），`git diff --check` 通过。本地三服务重建后健康检查通过，页面静态产物与新构建一致。合入会话确认后，本地数据库已核验迁移 51（会话确认）和 52（命名触发规则），Event Hub 重启确认无待执行迁移。

真实 Chrome/CUA 已点击验收员工名录、六页签、概览暂无数据、设定草稿离开/放弃、真实 Web 选人、共享策略弹窗、保留的能力包和触发器页面。飞书与钉钉读取旧失败/过期登记后分别显式刷新一次，二维码实际渲染且倒计时十分钟，随后停止等待；三渠道手填字段及未验证禁保存状态均已观察。企微入口实际打开官方授权弹窗，但浏览器安全策略不允许观察该站内容，未绕过，因此不宣称已验证企微官方二维码显示。未进行手机授权、凭据保存/换绑、外部权限写入或消息收发，协议/组件回归不能代替这些租户端到端验收。

后续入口优化：三家未绑定渠道初始只显示创建/绑定已有两种操作；点击后才展开，返回与切换上下文恢复选择态，不自动初始化登记。主按钮沿用现有 `NButton type="primary"`。企微创建在用户点击手势内打开官方窗口，取消后拒绝迟到凭据绑定。权限页签第四项邮箱定向邀请卡片、弹窗和员工页对应调用已删除（不是禁用占位），不影响其它页面的工作区邀请能力。

该优化最终验证：完整前端 318 项，316 通过、2 项 opt-in 跳过；类型检查与生产构建通过。重启后真实浏览器确认三家初始双入口、主按钮现有配色、手填显式展开、返回/渠道切换回选择态，以及第四邮箱邀请模块完全不存在。飞书显式创建恢复已有过期登记，未刷新生成新登记；本轮未输入凭据、保存绑定或进行手机授权/正式应用创建。企微点击手势、重复打开与退出迟到凭据保护通过组件测试，不代替官方扫码端到端验收。

状态真实性修正：权限采用就绪/未就绪/未确认三态。飞书从匹配当前应用与连接的登记中读取真实 permission_groups，逐 scope 显示结果；具体文档、群等资源访问无探测证据时只显示未确认，接口授权不代表业务工具已接入。缺少诊断、检查结果未知或钉钉/企微没有权限探测时不显示未就绪。钉钉 Stream 连接不等于群 @ 实际收发成功，群交互状态显示尚未验证。运行时刷新允许显式未知清除旧就绪状态，不改编辑中的策略。

跨页面渠道配置变更：另一页面停用或换绑后，员工详情的轮询遇到新配置版本、不同绑定或明确未绑定时，撤下原在线状态并提示重新加载。未保存草稿和原配置版本保留到用户确认重载且读取成功。回归覆盖跨页面停用、换绑响应和草稿保留。

群共享卡片移至权限页签底部编号 4；群共享选择器暂仅提供飞书、企微，不影响使用范围选人。已有钉钉/Web策略继续显示并保留原引用，重命名不静默删减；显式移除后不会作为可选候选重新加入。

渠道能力限制提示在绑定前后始终显示；企微与钉钉共用现有 warning 配色，不再使用企微绿色提示。对应原样式 hash 仅更新这项用户明确授权的例外，其余原组件样式守卫不变。

最终整合验收：同步远端官方模板目录更新后，完整前端 333 项，331 通过、2 项 opt-in 跳过，类型检查和生产构建通过。按本地启动依赖重启三服务，数据库迁移至 53，官方模板目录同步成功，服务健康及当前静态产物核对一致。CUA 实测双入口保留、旧登记权限显示未确认、企微警告配色、权限页底部第 4 群共享、共享下拉仅飞书/企微；未提交业务配置或进行正式扫码授权。绑定后业务表各诊断状态及旧钉钉/Web共享配置保留通过组件/API回归验证，不冒充真实租户的外部权限或消息收发验收。

### 能力包独立场景与员工使用范围（2026-11-19）

真实能力包编辑继续使用固定 Skill / Plugin 版本、员工插件账号探测与逐工具摘要授权；使用范围区拆为独立的私聊、群聊、触发器开关，只展开开启场景的规则。新建包默认开启私聊和群聊，二者继承员工范围且黑名单为空。触发器场景默认开启，当前员工的所有符合运行条件的触发器均可使用该包，黑名单为空。关闭场景时保留原名单。私聊和群聊分别选择继承员工范围加黑名单或纯白名单；群聊白名单同时配置人员（含部门）和群时按两类交集处理。跨渠道人员引用保持原绑定/代次，企微不提供部门。模板可选 Web 成员，复制后独立，不复制业务账号或工具授权。

旧 `scope` 不自动推断为新 `conversation_scope`。编辑名称、资源时旧规则原样保留；用户明确审阅后可重配私聊和群聊（初始关闭），保存时不同时提交旧模型。模式切换不会隐式清空已有名单。员工使用范围只提供全员加黑名单或纯白名单，群内可用独立；旧非等价配置须显式确认替换，不因普通保存而丢失。

组件回归见 `test/capability-components.test.mjs`、`test/workforce-rules-permissions.test.mjs` 和 `test/workforce-usage.test.mjs`；模拟交互测试不代替真实渠道授权验收。

**原员工页团队复用（2026-11-19）**　概览接入现有真实 `AgentSetupPanel`：示例仅在两类模型明确可用、当前账号有使用权且服务端 `can_try=true` 时允许填写到对话草稿，不自动发送；另存为工作区模板使用员工/配置版本创建草稿，不自动发布。来源模板更新不会覆盖现有员工。官方模型已有付款人按员工详情显示，新建选择官方模型时提示使用当前账号积分，切换模型仍保留原明确确认。详情按工作区/员工隔离加载，离开时放弃迟到响应；插件草稿继续参加页面离开保护。

### 原版员工工作台功能恢复（2026-11-19）

- 员工名录沿用原版卡片网格。搜索名称、职责及启停筛选在服务端分页前执行，状态按钮显示当前搜索范围内的人数。工作区所有者、管理员和普通成员都能在卡片上看到已绑定渠道、能力包数和触发器数。完整 ID 和配置版本见详情。分页在网格下方，仅多页时显示，每页 20 条。只有进入员工详情才读取该员工的配置；目录不等待模型目录或其他员工详情。创建和基础保存以服务器已确认的返回为准，后续读取失败不重试写入。
- 员工概览复用实际接入检查。示例任务只有满足当前使用权限、模型和示例条件时可填进对话草稿，不自动发送；「另存为工作区模板」创建草稿，不自动发布。显示模板来源说明及当前官方模型付费人，创建时选择官方模型会提示从本人积分扣费。
- [群聊触发与近期上下文](docs/workforce-group-policies.md)使用原版渠道页；三家 IM 只展示 `@` 能力，不编辑开关。飞书显示关键词／正则和近群历史，企微显示近群历史（条数与飞书共用），钉钉不显示这两类配置。Web 群配置能力与现有规则保留，界面暂隐藏。
- 权限页的群共享可在保存前预览实际方向和来源历史能力，不保存、不请求历史正文。渠道连接只显示已连接/未连接；未连接可悬停或聚焦查看当前真实错误码。独立的权限状态仍保留。

名录验收（2026-11-20）使用同一组示例员工对照设计基线与接入 PostgreSQL 的生产构建。在 1440、1024、390 像素视口下，首张「研发助手 AI」卡片分别呈三列、两列、单列布局，卡高均为 241 像素，资源行和底部操作区在卡内的位置与设计基线一致。23 位员工的工作区验证了末页、按职责搜索后筛选停用员工及详情返回。持续回归见 `test/workforce-original-root.test.mjs` 与 Backend 的 `TestAgentDirectoryFiltersAndResourceVisibility`。

**原选人框目录体验（2026-11-19）**　飞书/钉钉使用真实 selector 的部门下钻、人员部门过滤及面包屑，快照版本、最近同步、过期时间与失败错误来自目录状态接口；同步失败但旧完整快照仍有效时单独说明，不将失败等同无快照。企微继续搜索＋绑定验证，显示搜索 ID 与验证后的消息/回调 ID、已返回的部门路径及验证码有效期；验证码仅私下交本人，完成验证不自动授予使用权。选中仍通过原身份 resolve 和版本/epoch 校验，不更改授权语义。

真实员工详情的「设定与模型」第 4 节「人工确认规则」可配置 Web 工作区、飞书、钉钉、企业微信的真人和负责范围，并指定一名默认确认人。候选使用统一选人接口，已选列表标注来源。员工详情回归覆盖下拉的四个来源与跨来源身份保留。发布时由 Event Hub 先应用迁移 61，再启用新版 Backend 和 Web。见[后台任务找人确认](../backend/docs/human-requests.md)。
