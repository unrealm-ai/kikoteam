# 架构手稿维护记录

根 README 使用 [architecture-sketch-v5.png](architecture-sketch-v5.png)，局部更新三端办公渠道、事件回执、原生 Pi 会话、Task 的三条 MCP 连线及持久 Sandbox 的规划标记。

架构速览中的历史示意使用 [architecture-sketch-v4.png](architecture-sketch-v4.png)，更新于 2026-11-16。使用内置 image_gen 编辑工具，在 v3 上将组件标签统一为 backend，并保留所有组件边界及交互箭头。

旧 [architecture-sketch-v3.png](architecture-sketch-v3.png)、[architecture-sketch-v2.png](architecture-sketch-v2.png) 与 [architecture-sketch.png](architecture-sketch.png) 仅保留为历史素材，其中的旧组件名称或说明不代表当前设计。V4 与更早资源继续归档，不覆盖原图。

配图表达 backend 的业务入口与 Session 分配、Engine 托管 Pi、Runner 常驻管理及三条 MCP 路径。builtin MCP 的工具与接入详见[专项设计](../../../sandbox-runner/docs/builtin-mcp.md)，图文语义以[整体架构速览](../README.md)为准。

2026-11-16 后续设计已明确业务会话共享 Sandbox；v4 的“Session A 的 Sandbox”仅作 A 的访问通路示意，不能据此推导环境独占或停止整个共享环境。最新所有权及执行 Session 的独立后台进程见[存储与隔离约束](../storage-paths.md)，速览中的 Mermaid 已按共享关系调整。图中未定义 Token 轮换或检查点，相关执行约定以当前组件文档为准。

业务对象关系图及创建、停止、归档和回收职责见[生命周期设计](../conversation-lifecycle.md)。Backend 登记唯一环境和管理 Engine，首版同会话 task 固定到该 Engine/宿主；单个任务停止不关闭共享环境，父会话归档后收尾全部工作并回收运行资源，文件保留。本轮更新 Markdown/Mermaid，手稿继续作为组件通路示意。

## 本次编辑提示

2026-11-16 首版进一步确定为[Mac/Linux 裸 Sandbox](../../../sandbox-runner/docs/local-sandbox.md)：Engine、Pi、Runner 和工具同宿主同账号，Sandbox 是稳定项目目录，无安全隔离。手稿中的分框表示组件职责，不表示容器或独立操作系统；容器仅保留扩展。最新运行边界以速览 Mermaid 和正文为准，本批不重绘历史手稿。

输入图片：architecture-sketch-v3.png。使用内置 image_gen 编辑模式，非 CLI；背景不透明；结果另存为 v4。最终提示如下：

```text
Use case: text-localization / infographic-diagram edit.
Edit target: supplied architecture-sketch-v3.png. Rename the API deployment component to backend.
Change only two labels in the upper-left yellow container:
1. Replace "API + Web · 同一容器 · 可多副本" with the exact text "backend + web · 同一容器 · 可多副本".
2. Replace the inner component name "api" with the exact lowercase name "backend".
Allow slightly smaller handwriting in the container heading so the complete label fits inside the box.
Preserve all other text, arrows, containment, positions, colors, warm dotted paper, black hand-drawn ink, and landscape dimensions. In particular preserve the single-domain arrow, backend's Session allocation to Engine, Engine registration/heartbeat/configuration return arrow, Runner configuration arrow, event-hub messages, all three green Streamable HTTP routes, each Session's Pi ACP to Pi processes inside Engine, Sandbox with Go Runner and MCP Gateway, TS builtin MCP, stdio plugins, and footer "builtin MCP · TypeScript · 6 个 Pi 文件工具 + 5 个 Bash 工具". Do not change architecture, protocols, tool counts, or component boundaries. Opaque background.
```
