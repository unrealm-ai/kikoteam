# 远程 MCP 插件与业务账号

官方插件和空间自建插件都可以直接绑定到数字人的能力包。新绑定默认使用空间共享连接，管理员可以给单个数字人选择独享连接。外部服务按连接所用的业务账号判断项目与数据访问权，Kiko 按任务主体、能力包和工具限制调用。

## 配置与存储

- 插件版本固定 HTTPS endpoint。官方 GitHub 的所有只读版本支持 PAT（Bearer）和 OAuth，创建连接默认选择 PAT。其余插件沿用定义中的认证方式。插件 API 的 `supported_auth_methods` 和 `default_auth_method` 用于渲染连接表单，`auth_type` 保留历史定义值。
- `platform_plugin_connections` 中每个空间和插件固定版本有一份共享记录，每个数字人和固定版本可以另有一份专属记录。每条连接以 `auth_method` 记录实际认证方式，静态密钥和 OAuth token 使用带记录关联数据的 AES-GCM 加密。空间自建 OAuth 应用凭据单独保存在 `platform_plugin_oauth_clients`。
- `platform_agent_plugin_connection_settings` 独立保存账号来源和修订号。缺省来源为共享，选定独享后即使撤销连接也保持独享。迁移 000078 将原员工账号和已撤销记录迁为独享，保留原账户 ID、授权版本及密文。
- 空间 owner/admin 管理连接。前端可查看共享使用人数、已验证的工具和实际 scope。响应不回显业务密钥。OAuth 服务商可通过 token 响应提供可展示的 `account_id`。

## 管理入口

路径均以 `/api/v1` 开头。

| 操作 | 接口 |
| --- | --- |
| 列出空间共享连接 | `GET /workspaces/{w}/plugin-accounts` |
| 配置、读取或撤销共享连接 | `PUT/GET/DELETE /workspaces/{w}/plugin-accounts/{p}` |
| 管理数字人专属连接 | `PUT/GET/DELETE /workspaces/{w}/agents/{a}/plugin-accounts/{p}` |
| 查询有效连接、切换账号来源 | `GET/PUT /workspaces/{w}/agents/{a}/plugins/{p}/connection-source` |
| 请求第三方授权 | 两种账号路径下的 `POST oauth/authorize` |
| 登录服务商后返回 Kiko | `GET /auth/callbacks/plugin-oauth` |

共享连接的 GET 和 DELETE 使用查询参数 `plugin_version_id` 指定版本；PUT 在请求体中提供版本。数字人的专属接口兼容仅有一个版本的旧请求。

注册 OAuth 客户端时，回调 URI 使用 Backend 配置的公网 Origin 加上 `/api/v1/auth/callbacks/plugin-oauth`。Backend 校验受保护资源元数据、issuer、PKCE S256、一次性 state 和原浏览器登录会话。使用严格 SameSite Cookie 的浏览器可以从回调页点击「完成插件授权」继续。授权事务十分钟有效，发起人权限、连接版本和数字人来源都在写入前重验。用新 token 实际列出工具后才保存连接。

## 裸 sbx 执行

用户从主聊天发起委派，任务创建时固定插件定义版本、连接 ID、来源及授权修订号。首次成功列举的工具及其 schema 存入会话目录，后续在该目录内核对远端变化。Pi 直接使用原生 MCP 协议。免认证插件访问远端；带凭据的插件使用 Backend 的 `/mcp/plugin-connections/{connection_id}`，Pi 只获得与当前 Input、分配和连接绑定的短期凭证。Backend 复查当前工具目录、任务与权限后发起实际调用。未确认的执行结果保留待核对的审计事实。

静态凭据需由管理员更换。OAuth access token 临近到期时，Backend 在单份连接的数据库行锁下使用 refresh token 续期。正常续期只增加凭据修订号。换账号、权限变化、撤销或切换账号来源会阻止旧快照继续使用。明确失效的 refresh token 需要重新连接。撤销先在平台生效，再按服务商支持情况撤销上游 token。

生产探测仅允许 HTTPS，禁止 URL 用户信息、query 与 fragment；拒绝重定向，不使用环境代理。DNS 结果全部通过地址检查，再绑定解析后的地址连接，避免 DNS rebinding。回环、链路本地、云 metadata/CGNAT 等特殊地址拒绝访问。私网地址默认拒绝；管理员可通过部署配置 `KIKO_MCP_PRIVATE_CIDRS` 显式放行必要的私网网段，这不会解除 metadata/回环禁令。

保存账号时执行初始化和工具列举。内置同花顺、GitHub 与智谱连接还会分别执行单股行情快照、读取登录身份和一次限量搜索来验证实际业务权限。单次探测整体超时 18 秒，每个响应最多 2 MiB，最多 20 页、500 个工具。工具 schema 保存内容摘要，后续包授权与运行时须同时验证工具名和 schema 漂移。

升级到迁移 `000078` 时，先停稳旧 Backend 和 Engine，再由 event-hub 执行迁移，随后启动同一提交构建的新进程。旧 Backend 查询的员工账号表已更名。降级前需处理已经创建的共享连接和 OAuth 版本。

### 随仓库发布的连接

| 服务 | 空间或数字人提供 | Backend 出站行为 |
|---|---|---|
| A 股行情快查、Exa | 免认证 | 模板绑定时从随版本发布的工具定义建立共享连接，任务开始时重新核对服务端 schema。 |
| GitHub | 个人访问令牌（默认）或网页授权 | 两种连接都通过官方只读 MCP 验证身份。空间共享和数字人独享可以各选一种认证方式。 |
| Tavily | Tavily API Key | 保存连接时通过用量接口校验 Key；执行工具时通过 Bearer 请求头访问 Tavily 官方 MCP。 |
| 同花顺、智谱 | 同花顺 API Key、带联网搜索权益的智谱 Key | 同花顺用 `X-api-key`，智谱用 Bearer。 |
| 飞书 | 自建应用 App ID 和 App Secret | 每次建立 MCP 会话时换取有效租户令牌，并随请求发送文档工具白名单。 |
| 腾讯会议 | 个人 Token | 发送 `X-Tencent-Meeting-Token` 和协议包固定版本 `X-Skill-Version`。 |
| 钉钉待办、高德地图 | 钉钉专属 URL、高德 Web 服务 Key | 仅向内置服务商目标发送签名 URL 或查询参数。 |
| 纷享销客 | 企业 MCP 地址、发行方、OAuth 客户端、租户授权的只读工具名 | 首次授权请求可携带 `tenant_oauth`。租户应用保存在本空间，共享及独享连接分别执行 PKCE。更换应用前需撤销其已授权连接。 |

能力包内展示账号可见的全部工具。运行时核对会话工具快照、当前连接和业务来源。修改插件定义后，所选连接须对新定义版本有效。

### 配置 GitHub 连接

默认选择个人访问令牌（PAT）。管理员可在 GitHub 创建限定仓库的 fine-grained PAT，为所需的 Contents、Issues 和 Pull requests 授予读取权限，在连接表单中填写并测试。使用 PAT 的部署可以省略下面的 GitHub App 配置。

启用网页授权需要为本部署注册一个 GitHub App。

1. 部署管理员注册 GitHub App，在 Callback URL 填入 `${KIKO_PUBLIC_ORIGIN}/api/v1/auth/callbacks/plugin-oauth`，其中 Origin 使用浏览器访问平台的实际地址。为 Contents、Issues、Pull requests 选择 Read-only，将应用安装到需要访问的仓库。
2. 平台管理员在「设置 → 平台管理 → 集成设置」保存 GitHub 插件 OAuth 的 Client ID、Client Secret 并启用。每套部署配置一次。数据库尚未保存时才读取旧 `KIKO_GITHUB_OAUTH_CLIENT_ID` 和 `KIKO_GITHUB_OAUTH_CLIENT_SECRET`；缺少或禁用时仍可用 PAT。
3. 空间管理员在 GitHub 插件连接中点「前往 GitHub 授权」。Backend 请求 `offline_access` 用于续期，通过只读端点读取登录身份后保存连接。实际可读仓库取决于应用安装范围和授权用户的权限。[GitHub App 用户授权文档](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app)

从旧版本升级时先停旧 Backend，由 Event Hub 执行迁移 `000082`，再运行目录同步并启动新版 Backend。迁移按每条连接固定的插件版本填入认证方式。连接 ID、加密凭据、员工的版本绑定及能力包权限均保留。已有员工可在原插件版本内切换认证。

编辑共享或独享连接时，表单显示当前方式。切换到 PAT 时必须提交新令牌；OAuth 走网页授权回调。两种方式均在身份验证成功后按原连接版本写入。取消授权、令牌验证失败或晚到的回调会保留当前有效连接。切换成功后，新任务取得更新的连接，旧任务的凭据快照失效。撤销独享连接后，其来源仍是独享。

### 配置 Tavily API Key

1. 在 Tavily 控制台创建或复制 API Key。[Tavily 控制台](https://app.tavily.com/)
2. 同步官方目录后，空间管理员打开「Tavily 联网搜索」的共享或数字人独享连接，填写原始 API Key，点击「测试并保存」。Backend 验证成功后加密保存密钥。
3. 在同一能力包中添加插件「Tavily 联网搜索」和技能「Tavily 资讯检索与核实」。如果曾绑定旧的网页授权版本，升级插件和技能后为新版配置 API Key。

技能固定到 Tavily 上游的 MIT 版本，包内保留原始说明和许可证。技能通过能力包调用工具，密钥由 Backend 随请求发送。[上游技能](https://github.com/tavily-ai/skills/tree/90de383ad775f769692656ee8da4f935c2fb630b/skills/core/tavily-search) · [官方 MCP 认证](https://github.com/tavily-ai/tavily-mcp#remote-mcp-server)
