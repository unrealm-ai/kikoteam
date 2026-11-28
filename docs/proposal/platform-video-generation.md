# 官方文生视频

员工从 Web 或办公渠道收到生成短片的要求后，主对话委派后台任务。平台默认插件 `video-generation` 负责生成单镜头素材，已有 `video-editing` Skill 可在同一项目内剪辑、加字幕和拼接。首版接入 Nbility MiniMax-H3，下一阶段再接入已发布图片作为首帧。

## 员工与管理员

- 平台管理员在「平台管理 → 默认插件 → 文生视频」独立配置视频服务、模型、Key、每秒积分、最大并发和每工作区每日秒数。新配置默认使用 `MiniMax-H3`，也可输入同一视频协议的模型 ID。Key 加密保存，员工只读查看启用状态、价格和付款绑定。
- 用户可指定 1–15 整秒、横屏、竖屏或方形，默认 5 秒横屏，输出 768P。每次生成一条 MP4。用户在等待期间可以继续使用主对话。
- Web 在原任务回报和详情中展示视频、播放控件和原片下载。渠道按原 Inbox 和话题交付文件，渠道失败可重投同一文件。单个产物上限沿用 32 MiB。

## 调用、计费和恢复

1. task 轮次通过内部 MCP 调用 `generate_video(prompt, seconds, aspect_ratio, request_key)`。Backend 校验当前任务主体、员工付款绑定、正余额、开关、并发及 UTC 受理日秒数。按工作区、任务及意图键查重并校验参数指纹，锁定配置、付款人、单价及秒数。
2. Backend 使用 Bearer Key 调用 `POST /v1/videos`，保存上游 ID，按原 ID 轮询。创建结果未知进入待核对，禁止自动重复创建。查询和下载可恢复。排队、生成和保存期间显示实际进度。
3. Backend 校验 MP4 的大小、H.264 视频轨、时长和画幅，保存固定原片并写入项目的 `outputs/videos/<generation_id>.mp4`。任务通过 `artifact_publish` 交付，读取需重新验证当前任务权限。项目副本丢失时，同键重放可以从固定原片恢复。
4. 固定原片保存后，按受理单价乘请求秒数写一笔 `video_charge`。账单和余额同事务提交。明确失败记零，未知保留待核对。停止、撤权和停用阻止新生成，已提交的原请求按证据收尾，停止的任务不因完成通知重新启动。

## Nbility 接口

| 环节 | 首版协议 |
| --- | --- |
| 创建 | `POST /v1/videos`，使用 Bearer 鉴权和 JSON。字段为 `model`（已保存的上游模型 ID，默认 `MiniMax-H3`）、`prompt`、整数秒 `duration`、`ratio`（`16:9`、`9:16`、`1:1`）和 `resolution: "768P"`。成功后记录 `id`。 |
| 查询 | `GET /v1/videos/{id}`。按原 ID 查询 `queued`、`in_progress`、`completed` 或 `failed`。 |
| 收片 | 从 `completed` 回包的 `url` 下载 MP4。下载使用独立请求，只在调用供应商 API 时携带 Key。任务与账单引用平台生成 ID，用户通过已授权的项目产物播放和下载。 |

这里的通用性来自平台内的 `generate_video` 工具。其他供应商接入时需要核对时长、画幅、状态和文件下载协议。

修改服务地址、模型或 Key 后，管理员先测试草稿。后端校验 1 秒 H.264 短片后，在已认证的测试响应中返回 `preview_mp4`（Base64）和十分钟回执。页面加载可播放的短片后可保存；更换连接参数使预览和回执失效。调整单价、限额不需要重新出片。测试和放弃草稿均不改变员工正在使用的配置。

## 验收

验证正常生成、同键重放与参数冲突、重启后按上游 ID 找回、限额和调价、失败零扣费、停止或归档的交付边界。真实短片需核对唯一钱包流水、浏览器实际播放进度、下载响应及文件摘要。管理员测试与员工钱包隔离。

## 首版实现与验证

官方目录的私聊、群聊、触发器三种场景挂载任务工具。工作区管理的插件和视频剪辑 Skill 继续使用各自的授权。服务根地址填写 `https://api.nbility.ai`。管理员配置、真实草稿测试及待核对记录位于 `/api/v1/platform/default-plugins/video-generation` 下；员工工具入口为 `/mcp/platform-plugins/video-generation`。服务端的每秒积分由平台管理员指定。

Event Hub 的结算巡检唤醒 Backend 的视频处理协程。生成记录保存供应商任务 ID 和短期处理租约。Backend 按原配置查询、保存及结算，进程替换后到期租约可重新领取。确认没有收回上游 ID 的创建请求进入待核对。已确认失败的记录释放原受理日秒数。管理员核对可重新查询原任务或说明依据后零收费关闭。

2026-11-28 的本地验收使用真实 MiniMax-H3 和隔离的钱包，员工提交一次 4 秒竖屏请求。成片为 H.264/AAC、768 × 1344，容器时长 4.459 秒，大小 522283 字节。按测试单价 2 积分/秒扣 8 分，余额从 20 变为 12。Chrome 在任务详情实际播放，下载文件与固定原片的 SHA-256 相同。管理端出片测试另用确定性供应商验证。这里的积分单价是验收配置。

回归入口为 Backend 的 `TestVideoGenerationAdmissionAndRecovery` 和 `TestManagerRuntimeVideoGeneration`。后者沿用 Engine/Pi 运行时测试环境。默认使用测试供应商，显式设置 `KIKO_TEST_NBILITY_VIDEO=1` 才从标准输入读取测试 Key 并提交一条付费短片。浏览器验收入口为 `web/test/browser-video-delivery.mjs`。

[文生图与默认插件](platform-default-plugins-and-image-generation.md) · [任务文件和渠道投递](../../backend/docs/conversation-files.md) · [Nbility 视频协议](https://nbility.ai/docs/api/videos/minimax-h3)
