# 本地 Compose 启动器行为基线

适用于在 macOS 或 Linux 上通过 `./start.sh` 手动部署的一套本机实例。生产多实例及公网入口见[部署指南](../deployment.md)。容器内 Python、原生开发入口和测试驱动不属于本启动器依赖。

## 可观察行为

| 编号 | 契约 |
| --- | --- |
| LOCAL-01 | 无参数等同 `up`，构建当前源码，不拉取。Git 工作区 `--update` 与 `restart` 先快进拉取再从更新后的入口启动；拉取失败不构建、不停止旧服务。源码归档 `restart` 直接构建，`--update` 报错。 |
| LOCAL-02 | `init` 只初始化配置和内部令牌，不构建、启动或创建持久资产目录。首次 `up` 用模板产生 owner-only 配置，生成独立的数据库密码、认证密钥和两个不同内部令牌。已有凭据和指纹保持不变；启动器不搬迁既有配置或数据卷，应用 schema 迁移仍由 Event Hub 执行。 |
| LOCAL-03 | 不执行 dotenv 代码；按字节校验密钥及兼容旧 SHA256 指纹，拒绝重复和歧义字段、不安全的配置文件、来源不明的已有卷及丢失配置的旧状态。仅允许与端口一致的 localhost/127.0.0.1 origin；单个 Docker 引擎默认用一套 Compose 项目及数据卷。 |
| LOCAL-04 | `up` 先完整构建三个应用镜像，再停旧 Engine 和 Event Hub。等待 PostgreSQL、Backend、Hub 健康和迁移，先同步官方资源，再启动并等 Engine 就绪，成功后显示地址和构建版本。构建失败时旧服务不受影响。 |
| LOCAL-05 | `status` 查询 Compose 服务；`logs` 查每个服务最近 100 行；`down` 停止容器且不删数据、不修改凭据。三者不触发 Docker 安装或启动 Desktop。 |
| LOCAL-06 | 启动器报告阶段、耗时和错误，命令有超时预算。可变更命令在同一 checkout 持有互斥锁，异常退出不抢占不明锁。可捕获的信号触发本机进程清理；不承诺撤销 Docker daemon 已接收的操作，或终止已提权/脱离进程树的工作。 |
| LOCAL-07 | 缺 Docker 时按[原有安装流程](../../backend/docs/configuration.md#docker-安装辅助与可见进度)区分 macOS 与受支持的 apt 发行版。以官方来源和显式确认启动安装引导；不自动接受许可、授组、放宽 socket 或卸载冲突软件。Compose 缺失、连接失败和版本不兼容可区分诊断。 |

## 实现和兼容

- [入口](../../start.sh) 使用系统 `/bin/sh` 加载[启动器](../../tools/local-launcher.sh)和 [Docker 辅助](../../tools/docker-support.sh)。用原子发布和 owner-only 的 `.kiko-local/start.lock.d` 保护写入；已完成下载或启动的外部操作不回滚。恢复提示见[配置与数据保护](../../backend/docs/configuration.md#数据和已有部署保护)。
- 配置文件、持久目录、Compose 项目名与磁盘内容沿用原格式。配置指纹是 `database\0user\0password\0authSecret` 的 SHA256，无末尾分隔符。旧 `tools/local.py` 只为旧入口拉取新版后的进程跳转保留；新的 `start.sh` 从不调用它。升级前先等旧版正在进行的启停结束。
- Docker 端点应通过调用端的 Docker context/环境变量选择，不要在应用 `.env` 中定义 Docker 客户端的上下文/端点变量。不要把本地模板指向有生产数据的 Docker 引擎。

## 验证方式

| 契约 | 可重复验证 |
| --- | --- |
| LOCAL-01、04、05 | [CLI 黑盒用例](../../tools/test_local_launcher.py)记录命令顺序，验证快进及中途失败；[独立 Compose 工作流](../../.github/workflows/local-stack.yml)构建、检查健康、重启。 |
| LOCAL-02、03、06 | CLI 黑盒先移除启动路径中的 Python，再验证首次生成、旧版指纹、重复启动、缺失与非法状态、互斥、退出信号与超时。 |
| LOCAL-07 | CLI 黑盒替换平台、安装器和网络命令，验证确认、拒绝、官方来源与不接管旧安装。它不替代真实系统安装。 |

启动器回归命令：`python3 -m unittest discover -s tools -p test_local_launcher.py -v`。Python 是测试驱动的依赖，启动器进程的 PATH 不含 Python。

本次验收边界：Intel macOS 13.7.8 上用没有 Python 的启动 PATH，实测全新启动、注册和个人工作区、保留数据卷的 `down/up`、通过本地上游执行的 `restart`，以及有现存卷但缺少 `.env` 时拒绝启动且不停止现有容器。Linux 已跑命令替身用例，未验收独立 Linux Docker 主机的一键启动；未在宿主上真实安装 Docker，也未做旧版本数据迁移。
