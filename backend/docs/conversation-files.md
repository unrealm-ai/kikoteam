# Web 私聊项目文件

**当前部署**　Backend 与 Runner 使用同一份持久 POSIX 文件系统，部署时将同一持久卷配置为 `KIKO_STORAGE_LOCAL_ROOT`，Backend 与 Runner 均需读写权限。该卷需要支持文件锁、同卷原子重命名（目标已存在时失败）和目录同步。Backend 与 Engine 都将 `~/` 展开到各自运行账号的主目录，账号或挂载点不同时应显式配置指向同一卷的路径。每个 Web 私聊使用 Runner 的 `state/sandboxes/workspaces/<workspace_id>/employees/<agent_id>/conversations/<conversation_id>/project` 工作目录。首次上传可以先于第一个 Session 和 Runner。归档会话保留文件供原用户读取。

**重构目标**　会话项目根为 `workspaces/<W>/projects/conv-<conversation_id>/`，暂存的 `payload` 和文件回执归入同一挂载盘的 `temp/uploads/`。默认附件目录、重名处理、幂等键及同卷排他重命名沿用现行语义。[目录与回执设计](../../docs/architecture/storage-paths.md#上传发布和回执) · [异步附件规范](../../docs/spec/async-inbound-attachments.md)

## 文件接口

三个路由均以 `/api/v1/workspaces/{workspace_id}/conversations/{conversation_id}` 为前缀。`GET /files` 接受可选 `path`，返回 `path` 和 `entries`。根路径为空串，每次只列出一层，目录优先。条目包含 kind、name、相对项目根的 path，普通文件还含 size_bytes 和 modified_at。

`POST /files` 接受 multipart 的 `file` 和可选 `directory`。省略 directory 写入 uploads，传空串写入根。客户端使用登录 Cookie、CSRF 头和 Idempotency-Key。先暂存完整文件，再原子发布。重名依次采用「名称 (2).扩展名」。201 返回 path、name、media_type、size_bytes。同键、同目录、同文件名和相同字节重试返回原结果和 200，内容冲突返回 409。键区分大小写。上传请求体的接收时限为两分钟，超时返回 408。聊天框和目录树里的「重试」都会保留原请求的键、文件和目标目录。

`GET /files/content` 接受必填 `path`，返回下载字节和原文件名。操作重新检查工作区、私聊归属和项目范围，路径仅接受规范的相对路径。项目外的链接和特殊文件不能通过接口读取。上传上限为 32 MiB，每条消息最多引用 32 个文件。目录树仅展示目录和单链接普通文件，路径组件拒绝符号链接。上传超限返回 413，归档后新增上传返回 409。

现行 Backend 启动时可升级旧格式上传回执，并清理已发布文件在旧暂存目录中的硬链接。本地磁盘目录重构采用一次性开发环境切换，旧附件缺失只影响该附件。切换后的新上传应可从原业务会话及其任务读取。[旧会话验收](../../docs/architecture/storage-paths.md#7-验收与旧设计协调)

以下示例在同一个私聊中操作。

| 操作 | 请求 | 成功返回示例 |
| --- | --- | --- |
| 展开目录 | `GET /files?path=outputs` | `{"path":"outputs","entries":[{"kind":"file","name":"地区汇总.csv","path":"outputs/地区汇总.csv","size_bytes":42,"modified_at":"2026-11-19T09:30:00Z"}]}` |
| 上传图片 | `POST /files`，multipart 字段 `file` 传入「趋势图.png」，携带 `Idempotency-Key` | `{"name":"趋势图.png","path":"uploads/趋势图.png","size_bytes":1024,"media_type":"image/png"}` |
| 下载图片 | `GET /files/content?path=uploads%2F%E8%B6%8B%E5%8A%BF%E5%9B%BE.png` | `200`，响应头包含 `Content-Type: image/png` 和 `Content-Disposition: attachment`，响应体是图片字节 |

## 输入、委派与工作台

首条及后续普通 Input 可传有序 attachment_paths，正文与附件至少提供一项。服务端验证普通文件，按发送顺序将 path、name、size_bytes 快照与 Input 和消息事件一起提交。同键重试保留原输入，换序或更换附件视为不同内容。

主 chat 使用相同路径委派给共享项目的 task，创建任务或向已有任务追加输入时携带 `attachment_paths`。创建待办或补充待办时保存引用，显式启动时带入第一条执行输入。文件随时可能被移动或修改，工具使用时重新打开路径；用户从历史卡片下载当前内容，路径消失时提示文件不可用。任务的 `builtin read` 按附件路径原样打开文件，支持读取最多 32 MiB、4000 万像素的受支持格式图片。Pi 按模型输入限制缩放图片，项目保留上传原图。Engine 仅为通过视觉探测的模型配置版本启用图片输入；当前官方模型目录没有该探测结果，按文本模型处理。

顶部「项目文件」和面板收起按钮控制右栏，初始收起。同会话收起再打开保留目录展开状态与草稿。聊天区拖入桌面文件时显示绿色虚线「松开以添加附件」，离开或松开收起；首条和后续消息共用逐文件上传进度、独立重试及可移除草稿卡片。选中树中文件后点「引用到对话」，可与拖入的图片一起发送。

上传成功后采用服务端返回的实际路径。移除草稿引用仅影响本条消息。任务回报后刷新已展开目录，用户也可手动刷新。

例如，在右栏引用「地区汇总.csv」并拖入「趋势图.png」后，首条消息通过工作区的 `POST /sessions` 提交。

```json
{
  "conversation_id": "<conversation_id>",
  "mode": "chat",
  "input": {
    "text": "核对汇总表与趋势图",
    "attachment_paths": ["outputs/地区汇总.csv", "uploads/趋势图.png"]
  }
}
```

续聊向 `POST /sessions/{chat_session_id}/inputs` 发送上例的 `input` 对象。主 chat 调用 `task_delegation_create_session` 时，将任务要求写入 `instruction`，两条路径按原顺序写入 `attachment_paths`。给已启动的同一 task 追加文件时，使用 `task_delegation_send_message`，携带 `session_id` 和本次有序的 `attachment_paths`。补充文字可省略，文字和附件至少提供一项。Manager 按来源用户 Input、目标任务、文字和附件顺序去重，在会话事务内检查输入版本。

飞书、钉钉和企微收件经 Manager 鉴权后，按 Inbox 与附件序号复用项目上传回执。任务通过 `artifact_publish(path)` 发布固定版本；Manager 在任务回报里提供本轮 `artifact_candidates`。主对话可调用 `artifact_select(artifact_ids)` 为本轮回复选择候选。每项选择绑定自身的来源 Inbox，回复完成后逐文件独立投递和记录回执。
