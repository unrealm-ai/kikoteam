# Backend 启动配置

新部署使用根目录 `.env.example` 和私密 `.env` 配置启动依赖，变更启动依赖需重启。邮件、注册策略及 GitHub 插件 OAuth 应用由平台管理员管理，生效值读 PostgreSQL；迁移仍由 Event Hub 管理。

## 一键本地启动

支持 macOS 和 Linux，依赖可运行的 Docker Engine + Compose v2（支持 `up --wait`）和系统自带的 `/bin/sh`。不需要宿主机 Python。macOS 可使用 Docker Desktop 或已配置好的兼容 Docker 环境；Linux 需要当前用户有 Docker daemon 访问权限。检测不到 Docker 时提供显式安装确认；不会静默安装或提权，也不修改用户组。源码和前端在容器内构建，不要求宿主安装 Go、Node 或 PostgreSQL。

```sh
./start.sh
./start.sh up
./start.sh --update
./start.sh status
./start.sh logs
./start.sh down
```

`./start.sh` 等同于 `./start.sh up`，使用当前本地源码构建并启动，不拉取 Git 更新。`./start.sh --update` 需要有上游分支的 Git 工作区，先快进拉取，再用更新后的启动器构建并启动。拉取失败时，启动器在构建和停止容器之前退出。原有 `./start.sh restart` 继续支持更新并重启，使用源码归档时则直接构建。

首次启动可先用 `init` 单独初始化配置。旧版启动器首次通过 `restart` 或 `--update` 升级时，仍由旧版使用它原有的 Python 进入新版；升级后直接运行新版 `./start.sh` 不用 Python。启动器生成权限为 0600 的 `.env` 和 `.kiko-local/runtime.env`，分别保存数据库及平台密钥、Hub 与 Engine 两套内部 Token。升级旧本地实例时，独立生成缺少的运行时配置并沿用已有 `.env` 和平台密钥。

应用镜像全部构建成功后才停止旧 Engine 和 Event Hub。Compose 先等 PG16 和 Backend 健康，再启动 Event Hub。Hub 当选后负责数据库迁移和渠道恢复。迁移就绪后同步内置员工模板，随后启动 Engine 并等待注册心跳确认。启动成功会显示本地访问地址和 Git 版本。Backend 提供 Web 静态文件，默认访问 `http://localhost:18080`。配置可用模型后可以运行对话与任务，绑定渠道机器人后可以通过对应渠道使用 AI 员工。Runner 在 Engine 容器内按任务启动。

`.env` 中填写普通值；含特殊字符的 SMTP 秘密使用单引号包裹，不支持变量插值或 shell 命令。启动器不 `source` 文件执行代码。Compose 命令不回显配置秘密；不要公开分享 `docker compose config` 输出。数据库密码要求 URL-safe 字符，默认生成十六进制；有特殊字符的已有数据库凭据不应直接导入本地模板。

## 数据和已有部署保护

- 本地 PG 使用持久卷 `kikoteam-local-postgres-data`，不发布数据库端口。此模板同一 Docker daemon 只管理一套本地实例。
- `down` 不带 `-v`，不删除数据。不要使用 `docker compose down -v` 除非明确要永久清空本地数据。
- `.env`、`.kiko-local/` 及配置的持久工作目录应与数据库一起安全备份。`.kiko-local/config.sha256` 是数据库身份及密钥摘要，`.kiko-local/runtime.env` 是两个内部 Token。Backend 与 Engine 共享持久目录，并使用同一宿主 UID/GID 写入。
- 检测到已有卷/初始化标记但 `.env` 丢失时，停止并提示恢复，不能生成新密钥让旧加密凭据失效。已有卷无标记也拒绝自动接管。
- 检测旧 `backend/.env`、`backend.local.json` 或已注入数据库/认证配置时，拒绝首次自动初始化。不会连接、迁移、覆盖用户原有 PG；现有部署继续按原方式运行。
- 已有 `.env` 内容错误或端口占用时明确失败，不换库、不删卷、不自动换秘密。
- 升级前等旧版正在运行的启停操作结束。新版对同一 checkout 的写入和启停加锁；如异常中断留下 `.kiko-local/start.lock.d`，确认启动器和它启动的命令均已退出后，再手动移除锁目录，不删除数据库卷、`.env` 或其他状态。

## 环境配置

`.env.example` 是本地 Compose 的配置模板。Compose 向 Backend、Event Hub 和 Engine 注入连接同一 PostgreSQL 的 DSN，并通过它们在 Compose 网络中的服务名建立互访。`KIKO_HUB_TOKEN` 供 Backend 与 Hub 的内部接口使用，`KIKO_ENGINE_TOKEN` 供 Engine 注册及执行接口使用。容器的内部接口不发布到宿主机。平台认证密钥只注入 Backend。

独立运行二进制时，由进程管理器注入 `DATABASE_URL`、`KIKO_AUTH_SECRET`（32字节以上）、`KIKO_PUBLIC_ORIGIN`，以及可选 `KIKO_HTTP_ADDRESS`、`KIKO_WEB_DIR`、`KIKO_TRUSTED_PROXY_CIDRS`。Hub 注入 `EVENT_HUB_DATABASE_URL`。容器内 `postgres` 服务名只用于本地 Compose 网络，宿主直跑须提供实际PG地址。

Engine 调用官方模型代理时默认使用 `KIKO_PUBLIC_ORIGIN`。本地 Compose 将 `KIKO_MODEL_PROXY_ORIGIN` 设为容器内 Backend 地址，浏览器仍使用 `.env` 指定的公开 Origin。

钉钉扫码登记的来源标识由 [Backend 源码默认配置](../internal/config/channel_defaults.go) 定义为 `kikoteam`。创建登记时提交给钉钉；修改后仅影响新登记，扫码组织的可用范围由钉钉校验。

SMTP、注册策略、GitHub 插件 OAuth 和 KIKO 助手发放开关仅在平台管理配置，读写数据库。升级迁移见[部署指南](../../docs/deployment.md#32-平台业务配置)。

## 文件存储配置

现行 Compose 的本地资产目录和 S3 参数见[Skill 资产配置与本地容器](#skill-资产配置与本地容器)。本地磁盘目标布局和开发环境切换见[目录设计](../../docs/architecture/storage-paths.md)。

## 公开部署

内测用 IP 直连时，Backend 可显式设置 `KIKO_ALLOW_HTTP_IP_ORIGIN=true` 和 `KIKO_PUBLIC_ORIGIN=http://<IP>[:端口]`。默认关闭此开关。浏览器省略默认端口 80，使用该端口时 Origin 也须省略 `:80`。HTTP 会明文传输登录凭据与会话 Cookie，入口按内测团队来源限制访问。

本地入口只接受匹配端口的 localhost/127.0.0.1 HTTP origin，端口固定绑定 127.0.0.1。禁止直接把它改为公网裸露。正式部署应独立提供环境变量和 TLS 入口，明确注册策略；建议开启邮件验证、配置可信代理、备份与持久密钥管理。多个 Backend 副本共用相同秘密与数据库。配置缺失必须失败，不允许每个实例自动创建本地库或不同认证秘密。

## 启动 JSON 与秘密引用

旧 `KIKO_CONFIG` / `backend.local.json` 仅支持基础启动配置；环境变量可覆盖对应启动字段。旧 JSON 内的 `mail` 与 `auth.require_email_verification` 已不再支持，需先在旧版将邮件设置保存至数据库再移除这些键。保留原数据库地址及**原认证秘密**。

`database.url` 和 `auth.secret` 是显式引用对象，每个只选一种非空来源：

    {"env":"KIKO_AUTH_SECRET"}
    {"file":"/run/secrets/kiko-auth-secret"}
    {"value":"仅限本地私密配置中的实际值"}

缺失或空引用、多个来源均失败。文件必须是私密普通文件（0600/0400），相对路径以配置文件目录为准。内联秘密要求配置文件自身仅所有者可读写。

## 启动器验收

启动器行为、兼容约束和验证矩阵见[本地启动器 Spec](../../docs/spec/local-launcher.md)。运行 `python3 -m unittest discover -s tools -p test_local_launcher.py -v` 进行黑盒回归；Python 仅供测试驱动，启动进程的 PATH 不含 Python。Linux Compose 流程见 [Local Compose Stack](../../.github/workflows/local-stack.yml)。

## Docker 安装辅助与可见进度

`./start.sh` / `./start.sh init` 会区分缺少 CLI、缺少 Compose、daemon 不可达。`status/logs/down` 不发起安装；非交互环境直接报出下一步，不下载、不启动系统服务。

- **macOS**：下载前检查系统版本：macOS 14+ 默认当前官方稳定版；macOS 13.3–13.x 提供经官方发布说明核实的历史兼容版 4.48.0（build 207573），提示该系统支持已结束、旧版缺少后续修复，用户明确确认后才下载，并核验官方 SHA256。不对所有系统统一降级。低于 13.3 或版本无法识别时停止，不猜测历史版本。已安装版本也按应用声明检查兼容性，不自动覆盖 app 或现有数据。明确输入 `yes` 后按 arm64/amd64 从 Docker 官方 HTTPS 地址下载安装镜像并打开。用户自行拖入 Applications、启动 Docker.app，核对订阅许可并处理系统权限，然后重新运行脚本。不会自动接受许可、安装 Rosetta 或使用管理员密码。已安装但引擎未就绪时，单独确认后可打开 Docker Desktop，最多等待 180 秒。
- **Linux**：目前仅支持 Ubuntu jammy/noble/resolute/stonking 与 Debian bullseye/bookworm/trixie。明确说明软件源、签名密钥、管理员权限和 daemon 网络影响后，输入 `yes` 才使用官方 apt 源安装 Engine、CLI、containerd、Buildx、Compose；非 root 用户显式通过 `sudo -v` 授权。不运行远程 shell 脚本，不自动卸载冲突包、不修改 docker 组/socket 权限或用户防火墙规则。发现旧运行时或已有 Docker 源则停止交给管理员处理。不支持的发行版给出官方文档。
- **权限问题**：安装成功不保证当前账号可访问 daemon。Linux docker 组具有 root 级权限，脚本不自动加组；需要管理员独立决定权限方案，或按官方 rootless 方案配置。
- **进度**：阶段开始/完成与每 5 秒实际耗时心跳，保留下载/包管理/Compose 原始输出；不显示无法计算的虚假百分比。Docker 检查有短超时，下载与构建有总超时，Ctrl-C 可中止等待。已完成的安装步骤或已启动容器不会自动回滚/删除，重试前检查状态。

官方参考（2026-11-16 核查）：[macOS](https://docs.docker.com/desktop/setup/install/mac-install/)、[Ubuntu](https://docs.docker.com/engine/install/ubuntu/)、[Debian](https://docs.docker.com/engine/install/debian/)、[Linux 权限](https://docs.docker.com/engine/install/linux-postinstall/)。

安装辅助的测试使用命令替身，验证拒绝、非交互、平台选择、官方来源与不提权边界，不会真的安装系统软件。运行 `python3 -m unittest discover -s tools -p test_local_launcher.py -v`。实际安装和镜像构建结果需要另外记录。

### 2026-11-16 本地验收记录

已在 Intel macOS 13.7.8 上使用官方 Docker Desktop 4.48.0、Engine 28.5.1、Compose v2.40.2-desktop.1 实测：隔离端口 18082、全新命名卷，Web/Backend/Event Hub 构建完成，三服务健康；浏览器注册、登录、创建工作区成功。`down` 后再次 `up` 耗时约 14 秒，复用构建缓存，不重新安装 Docker，不更改 `.env`，原账号、工作区及登录状态保留。原生进程的 18080/18081 服务仍正常，未修改原数据库。

此记录仅覆盖已实现的本地启动与账号/工作区基础链路，不代表待迁移业务已实现。首页概览接口尚未接入时显示明确说明，不展示模拟统计。没有验收真实 SMTP、模型推理或渠道收发。Linux 安装分支已做隔离单元测试，未实际执行包管理器安装；Linux Compose CI 的结果应以对应提交的实际运行结果为准。Docker Desktop 4.48.0 是为此旧系统选用的兼容版本，已停止支持，不建议作为长期安全部署方案。

构建仍有既有依赖警告：测试用 `happy-dom` 存在高危公告，未打入最终静态产物；`vdirs` 缺少上游版权归属信息，另有前端包大小提示。这些警告未通过无关依赖升级掩盖，应在依赖维护任务中处理。


## Skill 资产配置与本地容器

本页的启动命令使用 Compose。原生进程部署见[本机运行说明](../../docs/operations/minimal-agent-runtime.md)。两种方式下，裸沙箱的 Runner 都由 Engine 按需启动。

当前 Backend 使用 `KIKO_STORAGE_LOCAL_ROOT`（默认 `~/.kiko/`）或显式 S3 配置保存 Skill 和已交付的固定文件版本。下述配置适用于现行二进制。本轮[目录设计](../../docs/architecture/storage-paths.md)将统一持久根的容器内默认值调整为 `/mnt/kikoteam`，其下三个顶层目录共用本地磁盘。

`start.sh` 在宿主展开本地根目录，将它 bind mount 到 Backend 的 `/mnt/kikoteam`；容器只收到容器内路径。启动器注入宿主 UID/GID，Backend 以该身份读写目录，不自动对现有目录递归 chmod/chown。首次创建目录使用 0700；相对路径和直接符号链接被拒绝，目录必须可写。独立使用 Compose 时须显式提供 `KIKO_LOCAL_STORAGE_HOST_ROOT`、`KIKO_LOCAL_UID` 和 `KIKO_LOCAL_GID`，推荐通过启动器运行。原有 `.env` 不会被重写，缺少新增本地配置时沿用默认值；跨部署应配置不同的根。

S3 凭据仅注入 Backend，不注入 Event Hub；S3 模式下不将资产回退写入本地根。清空容器不删除bind mount内资产，数据库备份必须同时备份资产及原密钥。修改后端/根目录不是迁移：先复制并校验历史资产，再切换配置。资产逻辑key与版本参见[路径约束](../../docs/architecture/storage-paths.md)。
