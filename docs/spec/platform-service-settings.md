# 平台服务配置

## 入口与权限

「设置 → 平台管理」只向平台管理员显示，不依赖额外环境开关。工作区 owner/admin 角色不能获得平台权限。页面提供「邮件与注册」「集成设置」和「附件下载」，官方模型、积分和账号权限仍使用原入口与数据。服务端分别校验每个设置接口；带副作用的请求沿用会话、CSRF 和 Origin 检查。

启动配置继续由部署提供，见[部署指南](../deployment.md)。实例服务设置在 PostgreSQL 生效，无单独服务进程或重启步骤。还没有对应数据库记录时才读取兼容旧配置，接口将此视为版本 0。明确保存停用即覆盖旧值。符合当前配置要求的旧部署值可由管理员显式迁入；启动和 GET 不写表。移除遗留变量时需整组移除，避免旧启动配置校验失败；若使用旧 JSON，也要成对清理旧邮件和邮箱验证策略，而不是只移除 SMTP 密码。

## 邮件与注册

在 Web 邮件设置中，选择「网易 163 邮箱」时只填 `@163.com` 邮箱和客户端授权码，使用 `smtp.163.com:465`。选择「自定义 SMTP」时，填写邮件服务器的 DNS 主机名和端口、发件邮箱及 SMTP 密码或授权码。表单把发件邮箱也用作登录账号，并据服务器的 DNS 名和端口生成 TLS 参数：`465` 连接即启用 TLS，`587` 强制 STARTTLS。不提供独立账号、发件别名、免认证或跳过证书校验的表单选项。

管理 API 保留原底层字段，不新增供应商字段。所有试发及启用状态下的保存均由服务端校验以上连接约束；直接调用也不能使用 IP、其他端口、与服务器 DNS 名不符的证书校验名、不同的认证账号和发件地址，或免认证中继。SMTP 先建立经证书校验的 TLS 通道才认证或发送，不回退到明文。

不兼容的旧数据库或部署配置不被自动转换；已生效的配置仍按原设置运行，直到明确替换或停用。页面只读显示原地址和发件邮箱，不自动测试或迁入；启用状态下要保存，须填入符合新规则的连接和凭据。停用保留原连接及已保存凭据，不能借停用改写连接或凭据，原来来自部署的值可作为停用的数据库记录保留。停用同时取消强制邮箱验证，仍受防管理员失联规则约束。读取、切换草稿或试发不会改变生效设置；更换连接目标，或刷新后草稿合并使目标变化时，表单不会把草稿秘密随之发送。

- 无数据库记录、无旧 SMTP 连接或凭据且邮件与邮箱验证均未启用时，Web 初次配置草稿默认选中「启用邮件」「注册时必须验证邮箱」。草稿和试发不改变公开注册规则；无旧配置的全新实例在保存前仍关闭邮件、允许密码注册。已显式停用的数据库记录即使没有连接字段也不会被重新勾选。
- `platform_mail_settings` 单例保存 SMTP 地址、TLS 校验主机名、发件人、登录账号、加密凭据、邮件开关及验证策略。禁用邮件不能同时要求验证码注册。停用不要求试发，但保存停用前至少要有一名可密码登录的平台管理员；否则需先趁邮件可用为管理员设置密码。明确停用期间，也不能撤销最后一名可密码登录的管理员。
- 试发候选配置不保存，只发往当前平台管理员的账号邮箱。SMTP 服务器接受邮件后才返回 `test_receipt`，不保证到达收件箱。任何启用状态下的保存都需要此凭据；服务端校验其 10 分钟有效期、操作者、当前配置版本、连接地址、TLS 主机与模式、认证账号、发件邮箱和实际使用的密码或授权码。凭据只含期限和签名，不含明文连接秘密。保存不再次试发；缺少、过期或不匹配要求重新试发，不能覆盖现行配置。仅改变注册验证复选框不使同一连接的成功试发失效，但一旦保存，配置版本变化，旧凭据不能再次使用。
- 密码不回显。空密码只在同一地址、TLS 主机、认证账号、发件邮箱和 TLS 模式下沿用；更换有认证的服务器或账号须重新填写。敏感字段和加密材料不进入 API 响应。
- 公共认证选项、验证码注册/登录、密码重置、邮箱邀请及后台投递使用生效策略。关闭邮件不领取新投递；无配置、配置不可用、或 SMTP 投递失败的处理不绕过强制注册验证。

接口：[邮件设置](../../backend/contracts/modules/platform/mail-settings.yaml)。验证：

- [Web 组件回归](../../web/test/platform-admin-settings.test.mjs)以 HTTP 替身验证 163 和自定义 587 请求、首次草稿、试发不保存、试发凭据与策略复选框、凭据过期/失效、旧配置的只读和停用操作及冲突中的凭据隔离；未运行连接真实邮件服务的前端端到端测试。
- [本地 SMTP 协议夹具](../../backend/internal/platform/integration/smtp_test.go)验证隐式 TLS、先 STARTTLS 再用同一账号认证和发信、缺少 STARTTLS 时拒绝明文。
- [管理 HTTP／隔离 PostgreSQL](../../backend/internal/platform/repository/mail_settings_test.go)验证启用与试发拒绝非法 SMTP 字段组合、启用保存必须携带匹配的试发凭据、持久策略、权限及防锁定；[试发凭据单元测试](../../backend/internal/platform/service/mail_test_receipt_test.go)验证绑定、篡改、过期和不含明文；[部署来源兼容](../../backend/internal/platform/service/mail_settings_legacy_test.go)验证旧部署值显式停用并以密文保存原凭据。

本轮未连接真实 163 账号或自建 SMTP 服务，未验证外部邮箱送达。

## GitHub 插件 OAuth

- `platform_oauth_app_configs` 按用途和供应商区分，当前实际支持 GitHub 插件 OAuth，**不用于网站登录**。Runtime 不把插件的 OAuth App 当作站点登录供应商。GitHub PAT 连接不依赖它。
- 平台管理员保存 Client ID 和加密 Client Secret、启停状态。API 仅返回 Secret 是否配置；不填写 Secret 时仅同一 Client ID 沿用旧值；更换 Client ID 后启用须输入新 Secret，也可在停用时明确清除。不能启用没有凭据的配置。回调地址由部署的公开 Origin 生成。
- 授权尝试绑定配置版本与应用凭据标识；应用切换不会用另一套凭据继续原回调。环境值原样迁入后仍可移除旧 env。同应用兼容的既有授权可续期；轮换后需要新应用凭据的旧授权须重新连接。停用或轮换阻止新授权和不相容的续期，并不立即撤销已签发、仍在有效期内的访问令牌；要立即停止旧连接，请单独撤销连接，并按需在供应商端撤销授权。

接口：[GitHub 插件 OAuth 应用](../../backend/contracts/modules/platform/github-oauth-app.yaml)；验证：[服务/API 与隔离 PostgreSQL](../../backend/internal/platform/repository/oauth_app_config_test.go)。

## 附件下载

「设置 → 平台管理 → 附件下载」的「校验公网 IP」控制钉钉和企微入站附件下载。全新实例和升级后尚未保存此设置的实例均默认开启，读取返回版本 0。由平台管理员保存到 PostgreSQL 单例 `platform_channel_download_settings`。版本冲突需刷新核对后重新保存。

关闭时，下载可以连接附件域名解析得到的内网 IP。Event Hub 在每次领取附件下载尝试时取得设置，当前已领取的尝试使用领取时的值。钉钉和企微各自的协议、端口、重定向次数和其他附件规则见[入站附件规范](async-inbound-attachments.md)。接口见[附件下载设置](../../backend/contracts/modules/platform/channel-download-settings.yaml)。

回归入口包括[管理 HTTP、权限及真实 PostgreSQL 的任务领取](../../backend/internal/platform/repository/channel_download_settings_test.go)、[Event Hub 的内网 DNS 下载](../../event-hub/internal/channel/media_download_transport_test.go)和[Web 设置页](../../web/test/platform-admin-settings.test.mjs)。

## 跨模块验收

- 生效以数据库版本为准；旧版本保存失败，编辑器保留草稿并要求读新版本、人工确认后再提交。后台读取失败不回退到 env。
- 应用加密依赖同一部署的启动密钥；备份 PostgreSQL 要同时备份对应密钥。用户可配置、测试，自动回归只使用测试替身和隔离数据库。
- [Web 回归](../../web/test/platform-admin-settings.test.mjs)覆盖试发不保存、冲突处理及秘密输入行为；[入口与权限回归](../../web/test/employee-root.test.mjs)覆盖角色条件、旧链接和工作区详情加载失败。
