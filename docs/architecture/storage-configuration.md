# 统一存储配置与跨模块资产约定

**当前配置与重构目标**　本页记录现行二进制的配置、资产适配器和备份方法。已确认的本地磁盘重构使用一个 `KIKO_STORAGE_LOCAL_ROOT`，顶层为 `official/`、`workspaces/` 和 `temp/`。各模式的默认路径、节点本地 Skill 入口和开发环境切换见[存储目录设计](storage-paths.md)。

## 1. 配置来源与后端选择

统一部署以项目根 `.env.example` 为模板、根 `.env` 为本地实例配置。启动层加载与校验配置，Compose/进程管理器仅向需要它的组件注入相关环境变量；模块使用解析后的配置，不在每次请求中重复读文件。独立部署可由秘密管理服务注入同名变量，不要求每个服务挂载完整 `.env`。

当前 `start.sh` 显式向 Compose 传根 `.env`；Backend 与 Event Hub 自身只读进程环境，不自动加载 dotenv 文件。Platform 的 Skill 资产适配及挂载见第 6 节；Engine/Runner 的本机目录和原生启动链已实现，Skill 缓存经授权内部接口下载并校验，复用 Platform 的本地/S3 权威资产。具体边界见[资源与能力包设计](../../backend/docs/platform-capabilities.md)，不因此承诺容器或跨机恢复。

当前启动配置如下。

| 配置 | 默认值与含义 |
| --- | --- |
| `KIKO_STORAGE_LOCAL_ROOT` | `~/.kiko/`，本地存储根目录；启动层显式展开当前部署用户的 `~`，再归一为绝对路径 |
| `KIKO_STORAGE_S3_ENDPOINT` | 空；S3 兼容 endpoint，OSS 等须按供应商 S3 接入方式配置 |
| `KIKO_STORAGE_S3_BUCKET` | 空；启用 S3 时必填的私有 bucket |
| `KIKO_STORAGE_S3_REGION` | 空；按供应商及签名方式校验，不能各组件猜不同默认 region |
| `KIKO_STORAGE_S3_PREFIX` | 默认空。可设置部署专用的对象前缀 |
| `KIKO_STORAGE_S3_ACCESS_KEY_ID` | 空；静态认证可选项，与 secret 成对配置 |
| `KIKO_STORAGE_S3_SECRET_ACCESS_KEY` | 空；秘密，不得输出至日志或普通接口 |
| `KIKO_STORAGE_S3_SESSION_TOKEN` | 空；使用临时凭据时与对应认证方式一起提供 |
| `KIKO_STORAGE_S3_FORCE_PATH_STYLE` | `false`；按供应商兼容性选择，使用一致的布尔解析 |

**选择规则**　所有 S3 配置为空时使用本地存储。任一 S3 字段显式非空即校验 S3 配置，包括前缀和 path-style。需要 bucket 和 region。静态凭据成对配置，也可以使用 SDK 凭据链。[配置解析](../../backend/internal/platform/integration/skillasset/store.go)

S3 配置不完整、认证失败或服务不可达时明确失败或报告依赖不可用，**不得静默退回本地**。未使用存储的组件不必要求 S3 就绪；需要存储的操作不得假成功。配置在进程启动时生效，变更后端按迁移流程执行，不热切换到空目录/空 bucket。

现行配置继续服务现有部署。项目归属、Session 目录与 Skill 正式版本目录的重构约定见[文件路径与隔离约束](storage-paths.md)。

**本机持久运行数据**：local/S3 选择适用于 Skill 等不可变资产。Pi 原生历史、Engine 身份映射、业务会话 project 和 Runner 账本都放本地根下稳定 `state/`，不放入可清除的 runtime/cache/tmp，不把 S3 当作 append 文件系统，也不建设逐轮检查点。首版按[Mac/Linux 裸 Sandbox](../../sandbox-runner/docs/local-sandbox.md)使用 Engine 本机目录、同账号进程，不做安全隔离或容器供应；后续容器扩展须保留这些数据。原生数据或已有项目缺失明确失败，不用空目录或公开事件冒充恢复。

## 2. 本地路径、容器与多机

现行本地资产位于 `KIKO_STORAGE_LOCAL_ROOT` 下的 `official/` 和 `workspaces/`，导入暂存在 `tmp/skill-imports/`，项目和执行状态在 `state/`。重构将项目与 Session 放入 `workspaces/<W>/`，上传暂存、回执和实例锁归入挂载盘的 `temp/`，进程私有临时文件放在根外。上传区与项目须位于同一文件系统。

当前启动器在宿主展开 `~/.kiko`，将宿主目录绑定到容器内的 `/var/lib/kiko`。目录重构实施后，Mac 原生默认 `~/.kiko`，Mac Compose 和 Linux 容器默认 `/mnt/kikoteam`，均由 `KIKO_STORAGE_LOCAL_ROOT` 指定。现行挂载参数见[Backend 配置](../../backend/docs/configuration.md#skill-资产配置与本地容器)。

当前 Engine 经授权接口下载 Skill 到执行 Session。重构后 Engine 为 Session 创建指向已选版本的目录链接。隔离沙箱只读挂载官方 Skill 和当前工作区 Skill 两个目录，并使本地 Session 入口与目标均按相同绝对路径可见。[Skill 挂载约定](storage-paths.md#skill-版本目录与沙箱)

## 3. 各自实现，但使用同一资产契约

不共享实现代码不等于允许各模块发明不兼容的格式。实现前共同确定并版本化以下契约，使用同一组测试夹具验证独立适配器互读：

- PostgreSQL 业务记录保存后端/存储身份、逻辑对象 key、内容大小、SHA-256 及资产格式版本；不把机器绝对路径、长期公开 URL 或带签名秘密的 URL 当永久引用。
- Skill 是固定版本完整目录，manifest 包含规范化相对路径、文件大小和内容摘要，目录打包/编码规则须统一；不能把 Skill manifest 当作所有业务资产的通用模型。Pi 原生 state 保持上游格式，不引入平台检查点格式。
- 最小互通语义为不可变对象写入、流式读取、元信息读取及受控删除；不依赖本地独有的 rename/append/文件锁模拟所有 S3 能力。不假设 S3 ETag 必然等于文件 MD5，也不以 bucket 列表替代业务数据库的已发布版本清单。
- 先写临时对象、校验完整性并发布不可变内容，再提交可见的业务引用。文件与 PostgreSQL 不构成同一事务，需明确中断恢复、重复请求和未引用对象清理；同 key 内容冲突不得静默覆盖。
- Platform 拥有 Skill 导入、版本和权限，Manager 消费获准业务引用，Engine 读取获准资产、保留 Pi 原生目录及映射。授权、删除依据与历史引用检查由业务所有者决定，不因存储配置相同而允许跨工作区/跨模块任意读写。

固定 Skill 的 manifest 与授权读取接口已进入内部协议并实现。其他跨进程资产协议仍需在对应实施契约中细化，不预建公开 File/Artifact 平台。

## 4. 秘密和数据库职责

`.env` 可承载数据库连接、应用根密钥和 S3 认证配置，但启动层按最小范围注入。S3 AK/secret 不进入 Skill、Plugin 定义、运行提示词、普通日志或明文配置快照；预签名地址若后续采用也按短期秘密处理。统一变量名不要求所有组件使用同一份高权限凭据；线上可为组件配置同一 bucket/namespace 下受限的身份。

存储配置不替代 PluginAuth。业务 OAuth/API Token 的受保护存储和运行分发仍由 Backend 管理；它们不能因启用对象存储而放进公共资产目录。现阶段保留 `KIKO_AUTH_SECRET` 根密钥方案，存储后端不得自行生成另一份根密钥或覆盖已有秘密。

Event Hub 继续统一 PostgreSQL 迁移 SQL、执行器、历史与结构快照；它不是配置中心、密钥保管服务或文件转发服务。Backend 和未来 Engine 按职责使用各自连接/查询；需要的存储元数据表仍进入全局迁移集。Event Hub 不使用文件存储时不注入其秘密。

## 5. 变更、备份与验收

需要保留历史的部署在更换资产后端、bucket 或前缀前，应控制写入，复制并校验已引用资产，验证消费者读取与回退。当前开发环境这次目录切换允许旧文件丢失，以原业务会话可继续工作为验收条件，采用[一次性切换步骤](storage-paths.md#7-验收与旧设计协调)。

备份覆盖 PostgreSQL、被引用的资产、仍有效的导入、项目工作文件、上传回执、Pi 历史与 Runner 账本，以及必要的部署配置和密钥。可写状态采用停写备份或一致性快照。恢复时核对固定 Skill 版本、历史会话续跑、项目相对路径和上传结果。

实现验收至少包含：无 S3 配置默认本地；部分配置和 S3 故障不回退；宿主路径到容器映射与重建持久化；本地/S3 独立适配器交叉读取；路径穿越和摘要错误拒绝；无明文秘密输出；多实例读写同一资产；后端切换及失败恢复；孤儿清理不破坏历史引用。OSS 等供应商须对所用 API 和签名/寻址方式真实测试，不以“S3 兼容”宣称所有特性通用。

## 6. 当前 Skill 资产实现的运行边界

- Skill 导入保存完整目录及 SHA-256 manifest；GitHub 在导入时解析 commit 后读取固定归档，不在使用时拉取，不自动跟随分支更新。当前下载仓库归档后筛选子目录，因此整个仓库的压缩/解压限制也适用（20 MiB / 100 MiB），大仓库中的小 Skill 可能被拒绝。暂不提供私有仓库凭据。
- `tmp/skill-imports/<workspace_id>/<import_id>/` 是服务生成的隔离暂存目录。导入收据有效期一小时；启动时及每十分钟批量回收已发布或过期收据的暂存文件。失败不标记完成，下轮重试；SQL 先筛选当前存储身份，旧后端记录不会阻塞新后端回收。本地回收限定精确收据目录，包含进程崩溃残留的 `.pending-*` 文件；S3 按 manifest 精确删除。已发布收据元数据保留以支持幂等重试。
- Import 全流程最多 55 秒，Publish 最多 15 秒。发布在工作区事务锁内先验证权限、收据、乐观版本，再复制和校验不可变文件；网络超时受同一 context 约束，避免无限占用工作区锁。业务冲突不会先创建正式版本目录。
- **尚未完成的回收边界**：存储写成功后 PostgreSQL 提交结果不确定、数据库故障或进程崩溃，仍可能留下无引用的正式版本对象。现有 GC 仅回收暂存，不自动删除正式版本，以免误删已提交但客户端未收到确认的历史版本。完整孤儿治理需要持久化 publication intent、宽限期和事务内引用核验；运维不能仅凭目录时间删除正式资产。
- S3 使用 SDK 签名及条件创建保证不可变写入。兼容服务必须支持相应条件请求；不支持时应失败，不退回本地。本轮 `TestS3CompatibleImmutableRoundTrip` 已对隔离的 SeaweedFS 4.54 真实 S3 服务通过写入、重复幂等、防覆盖、复制、读取校验及临时回收测试。该测试采用仅回环 HTTP 直连配置，不放宽部署配置的 HTTPS 要求；AWS、OSS 等部署目标仍须单独实测，不以 SeaweedFS 通过替代供应商验收。
