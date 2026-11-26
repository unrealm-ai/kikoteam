# 部署与环境变量配置指南

本文档面向自托管与运维人员，说明 KIKO 企业级AI员工云平台的本地单机部署、生产多实例容器化拓扑、反向代理安全路由、全量环境变量参考以及升级与备份策略。

## 1. 部署拓扑概览

生产环境由三个容器镜像（`backend`、`event-hub`、`agent-engine`）加一个 **PostgreSQL 16+** 数据库组成：

```mermaid
flowchart LR
    User["浏览器 / Webhook 调用方"] -->|"HTTPS (单一公网域名)"| Proxy["反向代理 (Nginx / Caddy / Ingress)"]
    Proxy -->|"放行公开路由 / 拦截内部路由"| Backend["backend (含 Web 静态资源，可多副本)"]

    subgraph Internal["内部受控网络"]
        Backend <--> PG[("PostgreSQL 16+")]
        Hub["event-hub (全局单活 + 可选热备)"] <--> PG
        Hub <-->|"内部控制与回调"| Backend
        Engine["agent-engine (含 Pi / Runner / builtin-mcp，可多实例)"] <--> PG
        Engine <-->|"注册心跳 / 领取配置 / 内部 MCP"| Backend
        Backend --- SharedFS[("持久共享目录 /mnt/kikoteam (+ 可选 S3)")]
        Engine --- SharedFS
    end
```

### 各服务角色与扩缩容规则

| 服务 | 镜像 | 默认监听端口 | 副本策略 | 说明 |
| --- | --- | --- | --- | --- |
| `backend` | `ghcr.io/unrealm-ai/kikoteam/backend` | `8080`（HTTP API、Web 静态资源、`/healthz`、`/readyz`） | **可水平扩展多副本** | 唯一对外接收公网流量的服务；多副本必须共用同一个 `DATABASE_URL` 与 `KIKO_AUTH_SECRET` |
| `event-hub` | `ghcr.io/unrealm-ai/kikoteam/event-hub` | `8081`（`/healthz`、`/readyz`）<br>`8082`（内部控制端口） | **全局单活（可部署热备实例）** | 通过 PostgreSQL 会话级 Advisory Lock 自动选主；当选实例自动执行数据库迁移、维护飞书/钉钉/企微长连接与定时触发 |
| `agent-engine` | `ghcr.io/unrealm-ai/kikoteam/agent-engine` | `8083`（本机 `/readyz`） | **可多实例扩展** | 每个实例需设置唯一且稳定的 `KIKO_ENGINE_HOST_ID`，通过 `KIKO_ENGINE_CAPACITY`（默认 `100`）控制单实例同时绑定的会话数 |
| `postgres` | `postgres:16.6` | `5432` | 主从高可用或托管 PG | 存储所有业务事实、配置版本、加密凭据引用与会话事件 |

## 2. 反向代理与必须屏蔽的内部路径

**公网只需暴露一个 HTTPS 域名并指向 `backend:8080`**（不要将 `event-hub`、`agent-engine` 或 `postgres` 的端口暴露到公网）。

由于 `backend` 在同一个 HTTP 监听器上同时提供公开接口和供内部组件调用的协作端点，**公网反向代理（Nginx / Envoy / Cloudflare / Ingress）必须在入口层拦截并拒绝以下内部路径的外部访问**（仅允许内部网络中的 `agent-engine` 和 `event-hub` 访问 `backend:8080` 的这些路径）：

| 必须在公网反向代理拦截的路径前缀 | 内部用途 |
| --- | --- |
| `/api/internal/v1/` | Engine 注册心跳、Session 分配、Runner 运行时配置领取、Event Hub 内部回调 |
| `/mcp/task-delegation` | 主对话（`chat`）委派、查询、追加、停止后台任务的内部 MCP |
| `/mcp/task-space-history` | TaskSpace 执行任务读取同空间任务历史的内部 MCP |
| `/mcp/human-confirmations/` | 对话与后台任务发起或处理人工确认的内部 MCP |
| `/mcp/work-items/` | 跨员工事项交接与验收的内部 MCP |

### Nginx 反向代理配置示例

任务详情链接启用前须检查所有入口。初次访问的 `/tasks/<token>` 路径自身含有效 bearer；不在 CDN、WAF、Ingress、反向代理、APM、访问/错误日志、追踪或分析系统记录原路径、查询串或 Authorization。下例关闭该路径的 Nginx 访问日志并丢弃该 location 的错误日志；其他层仍需独立关闭、清洗或只记录固定路由名，并用合成令牌核对各层日志。应用的 `no-store` 和 `no-referrer` 响应头不能替代代理日志配置。

任务详情的独立地址可以配置为 HTTP/HTTPS 的域名或 IP 加端口。公网使用 HTTPS；只有可信内网单机才使用纯 HTTP，HTTP 不保护链接或请求凭证。是否能打开由点击设备的实际网络和 IM 客户端决定，保存设置不探测可达性。此地址不会替换 `KIKO_PUBLIC_ORIGIN`。

```nginx
server {
    listen 443 ssl http2;
    server_name kiko.example.com;

    # SSL 证书配置略...

    # 严禁从公网访问内部协作 API 与内部 MCP 端点
    location ~ ^/(api/internal/v1/|mcp/(task-delegation|task-space-history|human-confirmations|work-items))(/|$) {
        return 403;
    }

    # 任务详情入口 URI 包含 bearer；不要记录原始 URI 或这些请求的错误详情。
    location ^~ /tasks/ {
        access_log off;
        error_log /dev/null crit;
        proxy_pass http://kiko_backend;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_cache off;
    }

    # 匿名任务详情 API 的本身路径不含 bearer，仍不得记录 Authorization。
    location ^~ /api/v1/public/task-detail {
        access_log off;
        error_log /dev/null crit;
        proxy_pass http://kiko_backend;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Authorization $http_authorization;
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600s;
    }

    # 现有登录态 SSE 实时事件流关闭缓冲
    location ~ ^/api/v1/.*/events/stream$ {
        proxy_pass http://kiko_backend;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600s;
    }

    location / {
        proxy_pass http://kiko_backend;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        client_max_body_size 100m;
    }
}
```

## 3. 全量环境变量参考

根目录的 [`.env.example`](../.env.example) 提供了配置模板。以下按组件列出启动环境变量和旧部署的兼容项：

### 3.1 核心与 Backend 环境变量

| 变量名 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `DATABASE_URL` | **是** | — | PostgreSQL 连接 DSN（如 `postgres://kiko:pass@postgres:5432/kikoteam?sslmode=disable`） |
| `KIKO_AUTH_SECRET` | **是** | — | **至少 32 字节**的高熵随机密钥，用于会话签名及数据库中模型/插件凭据加密。**一旦初始化严禁随意更换** |
| `KIKO_PUBLIC_ORIGIN` | **是** | `http://localhost:18080` | 浏览器访问平台的外部 Origin（生产环境填 `https://kiko.example.com`，末尾不带 `/`） |
| `KIKO_HTTP_ADDRESS` | 否 | `0.0.0.0:8080` | Backend HTTP 监听地址 |
| `KIKO_WEB_DIR` | 否 | `/app/web` | Web 前端静态产物目录路径 |
| `KIKO_ENGINE_TOKEN` | **是** | — | **至少 32 字节**的内部密钥，供 `backend` 与 `agent-engine` 互相认证（两端须一致） |
| `KIKO_HUB_TOKEN` | **是** | — | **至少 32 字节**的内部密钥，供 `backend` 与 `event-hub` 互相认证（两端须一致） |
| `KIKO_HUB_CONTROL_URL` | 否 | `http://event-hub:8082` | Backend 调用 Event Hub 内部控制接口的地址 |
| `KIKO_MODEL_PROXY_ORIGIN` | 否 | 同 `KIKO_PUBLIC_ORIGIN` | Engine 访问 Backend 官方模型代理的内部地址（容器内通常设为 `http://backend:8080`） |
| `KIKO_TRUSTED_PROXY_CIDRS` | 否 | 空 | 可信反向代理网段（逗号分隔 CIDR），用于安全解析 `X-Forwarded-For` |
| `KIKO_ALLOW_HTTP_IP_ORIGIN` | 否 | `false` | 内网使用纯 HTTP + IP 地址（如 `http://192.168.1.10:18080`）访问时需显式设为 `true` |
| `KIKO_MCP_PRIVATE_CIDRS` | 否 | 空 | 允许远程 HTTP MCP 访问的私网网段白名单（逗号分隔 CIDR）；永不允许环回与链路本地地址 |
| `KIKO_GITHUB_OAUTH_CLIENT_ID` | 否 | 空 | 官方 GitHub 插件 OAuth App Client ID（回调地址为 `<KIKO_PUBLIC_ORIGIN>/api/v1/auth/callbacks/plugin-oauth`） |
| `KIKO_GITHUB_OAUTH_CLIENT_SECRET` | 否 | 空 | 官方 GitHub 插件 OAuth App Client Secret |
| `KIKO_DINGTALK_REGISTRATION_SOURCE` | 否 | `kikoteam` | 钉钉扫码创建机器人时提交的来源标识 |

### 3.2 SMTP、注册策略与 GitHub 插件 OAuth

新部署从「设置 → 平台管理」保存实例级配置，不需要给启动模板填 SMTP 或 GitHub 凭据。无数据库配置时，旧部署继续使用既有 env/旧 JSON 值。管理员保存后，以数据库为准；停用不是恢复 env。迁移后如需移除旧环境变量，整组移除并核对重启前后能力一致。

以下邮件环境变量仅用于兼容已有部署。GitHub 插件旧变量见上表；两组配置都不用于网站第三方登录。


| 变量名 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `KIKO_REGISTRATION_VERIFICATION_REQUIRED` | 否 | `false` | 是否强制要求邮箱验证码才能注册（开启时必须同时开启 `KIKO_MAIL_ENABLED=true`） |
| `KIKO_MAIL_ENABLED` | 否 | `false` | 是否启用 SMTP 邮件发送（用于注册验证码、密码重置与邮件邀请） |
| `SMTP_ADDRESS` | 启用邮件时必填 | 空 | SMTP 服务器 `host:port`（如 `smtp.example.com:587`） |
| `SMTP_HOST` | 启用邮件时必填 | 空 | TLS 证书校验主机名（如 `smtp.example.com`） |
| `SMTP_FROM` | 启用邮件时必填 | 空 | 发件人纯邮箱地址（如 `noreply@example.com`，不含显示名） |
| `SMTP_USERNAME` | 条件必填 | 空 | SMTP 认证用户名（与 `SMTP_PASSWORD` 同时提供或同时省略） |
| `SMTP_PASSWORD` | 条件必填 | 空 | SMTP 认证密码 |
| `SMTP_IMPLICIT_TLS` | 否 | `false` | `465` 端口隐式 TLS 设为 `true`；`false` 时强制要求 `STARTTLS`，绝不回退明文 |

### 3.3 存储配置变量（Backend & Agent Engine）

容器以本地磁盘挂载整个 `KIKO_STORAGE_LOCAL_ROOT`，Mac 原生默认 `~/.kiko`，容器内默认 `/mnt/kikoteam`。开发环境一次性切换及验收见[存储目录方案](architecture/storage-paths.md)。

| 变量名 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `KIKO_STORAGE_LOCAL_ROOT` | 否 | `~/.kiko/`（容器内 `/mnt/kikoteam`） | 持久状态根目录，保存项目文件、Pi 历史、Runner 账本与上传回执。本地资产模式还保存 Skill 和固定交付文件 |
| `KIKO_STORAGE_S3_ENDPOINT` | 否 | 空 | S3 兼容对象存储 Endpoint（**任一 `KIKO_STORAGE_S3_*` 非空即启用 S3 模式保存 Skill 资产和已交付的固定文件版本，配置不完整直接报错拒绝启动，不静默回退本地**） |
| `KIKO_STORAGE_S3_BUCKET` | S3 模式必填 | 空 | S3 Bucket 名称 |
| `KIKO_STORAGE_S3_REGION` | S3 模式必填 | 空 | S3 Region |
| `KIKO_STORAGE_S3_PREFIX` | 否 | 空 | 对象 Key 前缀 |
| `KIKO_STORAGE_S3_ACCESS_KEY_ID` | 使用静态凭据时必填 | 空 | S3 Access Key ID，配对设置。也可使用 SDK 凭据链 |
| `KIKO_STORAGE_S3_SECRET_ACCESS_KEY` | 使用静态凭据时必填 | 空 | 与 Access Key ID 配对设置，凭据只提供给资产存储消费者 |
| `KIKO_STORAGE_S3_SESSION_TOKEN` | 否 | 空 | 可选临时 STS Session Token |
| `KIKO_STORAGE_S3_FORCE_PATH_STYLE` | 否 | `false` | MinIO 等要求 Path-Style 访问的对象存储设为 `true` |

> [!IMPORTANT]
> 即使启用了 S3 存储不可变 Skill 资产，沙箱的工作目录（`workspaces/<W>/projects/<P>/`）和 Pi 原生会话历史依然使用 POSIX 文件系统（`KIKO_STORAGE_LOCAL_ROOT`）。在生产环境部署时，`backend` 与 `agent-engine` 需挂载同一份支持文件锁的持久存储到 `/mnt/kikoteam`，并以相同的 UID/GID 运行。

### 3.4 Event Hub 环境变量

| 变量名 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `EVENT_HUB_DATABASE_URL` | **是** | — | PostgreSQL 连接 DSN |
| `EVENT_HUB_BACKEND_URL` | **是** | `http://backend:8080` | Event Hub 回调 Backend 的内部地址 |
| `KIKO_HUB_TOKEN` | **是** | — | 与 Backend 一致的内部认证 Token（32 字节以上） |
| `EVENT_HUB_HEALTH_ADDR` | 否 | `0.0.0.0:8081` | 健康探针（`/healthz`、`/readyz`）监听地址 |
| `EVENT_HUB_CONTROL_ADDR` | 否 | `0.0.0.0:8082` | 内部控制接口监听地址 |
| `EVENT_HUB_CONNECT_TIMEOUT` | 否 | `5s` | 数据库连接超时 |
| `EVENT_HUB_HEARTBEAT_INTERVAL` | 否 | 默认内置 | 选举锁连接心跳间隔 |
| `EVENT_HUB_MIGRATION_TIMEOUT` | 否 | 默认内置 | 启动自动迁移超时时间 |
| `EVENT_HUB_SHUTDOWN_TIMEOUT` | 否 | 默认内置 | 优雅停机收尾超时时间 |

### 3.5 Agent Engine 环境变量

| 变量名 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `DATABASE_URL` | **是** | — | PostgreSQL 连接 DSN |
| `KIKO_BACKEND_URL` | **是** | `http://backend:8080` | Engine 访问 Backend 的内部地址 |
| `KIKO_ENGINE_TOKEN` | **是** | — | 与 Backend 一致的内部认证 Token（32 字节以上） |
| `KIKO_ENGINE_HOST_ID` | 推荐显式设置 | 宿主机 hostname | **每个 Engine 实例必须稳定且唯一**；容器重建后保持相同 `KIKO_ENGINE_HOST_ID` 才能继续关联原有的本地沙箱与 Pi 历史 |
| `KIKO_ENGINE_ADDRESS` | 否 | `127.0.0.1:8083` | Engine 探针（`/readyz`）监听地址 |
| `KIKO_ENGINE_CAPACITY` | 否 | `100` | 单实例同时绑定的 Session 上限。已绑定的空闲或受阻 Session 在释放前仍占一个名额 |
| `KIKO_ENGINE_IDLE_TIMEOUT` | 否 | `1m` | 无后台进程活动时释放空闲 Pi RPC 进程的等待时间 |
| `KIKO_STORAGE_LOCAL_ROOT` | 否 | `~/.kiko`（容器内 `/mnt/kikoteam`） | 持久沙箱与原生会话根目录 |

## 4. 数据库迁移与平滑升级流程

1. **迁移由 `event-hub` 统一执行**：`backend` 和 `agent-engine` 不会在启动时执行 SQL 迁移，也不会因数据库 schema 版本尚未升级而崩溃退出。
2. **启动与升级顺序**：
   - 升级版本前，先备份 PostgreSQL 数据库、`.env`、`.kiko-local/` 和持久存储目录；
   - **升级 `event-hub` 时**：先让旧版本 `event-hub` 实例正常收到 `SIGTERM` 停止新触发、完成在途操作并释放 PostgreSQL Advisory Lock，再启动目标新版本的 `event-hub`；新实例获锁后会在同一连接上自动执行增量 SQL 迁移；
   - 迁移完成后，运行一次性 `backend sync-catalog`（Compose 中的 `template-catalog` 服务会自动完成）同步最新内置官方模板与资源目录；
   - 滚动更新 `backend` 与 `agent-engine`（建议为 `agent-engine` 配置 90 秒以上的 `stop_grace_period` 以便在途轮次和工具进程安全收尾）。

## 5. 备份与灾难恢复清单

备份 KIKO 时，以下三项**缺一不可**：

1. **PostgreSQL 数据库完整转储**（`pg_dump` 或数据卷快照）；
2. **配置文件与密钥**：`.env`（特别是 `KIKO_AUTH_SECRET`）与 `.kiko-local/`（包含 `runtime.env` 与 `config.sha256`）；
3. **持久化目录与对象存储**：备份当前 `KIKO_STORAGE_LOCAL_ROOT` 中的项目、Pi 历史、Runner 状态和上传回执，同时备份本地或 S3 上的 Skill、仍有效的导入和已交付文件版本。可写状态应采用停写备份或一致性快照。
