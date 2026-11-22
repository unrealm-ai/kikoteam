# KIKO 前端 UI/UX 设计与工程规范

> **定位**：面向企业级 AI 员工协同与 AI Agent 控制面的高密度工程界面规范。
> **技术底座**：**Vue 3 + Naive UI (`NConfigProvider` `themeOverrides`) + CSS Design Tokens (`--id-*`) + Scoped CSS**。
> **设计原则**：借鉴 Linear / Vercel / Raycast 的克制、精准与信息密度，同时针对**中文企业后台阅读习惯**、**多 IM 渠道品牌共存（飞书 / 企微 / 钉钉）**与**AI 员工拟人识别**做本土化校准。

---

## 1. 色彩系统与表面层级（90 / 8 / 2 克制法则）

### 1.1 比例分配
- **90% 中性画布与表面**：减少视觉疲劳，把识别空间留给业务数据、IM 官方图标与 AI 员工头像。
- **8% 结构边界与次级信息**：以 `1px solid` 极细边框（hairline border）和微弱的明度落差划分区域。
- **2% 品牌主色与语义微高亮**：品牌主色 `#2563EB`（Cobalt）仅用于核心交互动作与导航激活；红/琥珀/绿严格保留给风险拦截、告警与健康状态。

### 1.2 Token 唯一真源
所有页面和组件禁止分散硬编码灰阶或语义 Hex，统一引用 [`src/direction2/styles/design-tokens.css`](../src/direction2/styles/design-tokens.css)：

| 语义角色 | CSS Token | 色值 (Light Mode) | 适用场景 |
| --- | --- | --- | --- |
| 主画布背景 | `--id-bg-canvas` | `#FAFAFA` | 应用外壳底色 |
| 主内容表面 | `--id-bg-surface` | `#FFFFFF` | 卡片、抽屉、表格容器 |
| 次级表面 | `--id-bg-subtle` / `--id-bg-muted` | `#FAFAFA` / `#F4F4F5` | 表头、内嵌分组区、Hover 态 |
| 结构细线 | `--id-border-subtle` / `--id-border-hairline` | `#E4E4E7` / `#F4F4F5` | 卡片外框、行分割线 |
| 主文本 | `--id-text-primary` | `#09090B` | 页面标题、正文核心信息、KPI 主数值 |
| 次级文本 | `--id-text-secondary` | `#52525B` | 描述说明、副标题、表单标签 |
| 弱化文本 | `--id-text-muted` / `--id-text-placeholder` | `#71717A` / `#A1A1AA` | 时间戳、元数据、Placeholder |
| 主交互色 | `--id-accent-primary` | `#2563EB` | 页面主按钮、Focus Ring、折线趋势主色 |

> **关于 Dark Mode**：当前阶段优先完成全站 Light Mode Token 化与局部灰阶收敛（消除 Slate 与青灰 `#ECEFF1` 混用），暂不强制同步开发双主题；新增 CSS 须始终通过 `--id-*` 变量引用颜色，为后续扩展预留接口。

### 1.3 边框优先（Border-First Elevation）
- **常驻面板与卡片禁止使用 `box-shadow` 营造层级**，一律采用 `1px solid var(--id-border-subtle)` + 轻微底色差。
- Hover 交互仅通过 `border-color` 深化（如 `--id-border-strong`）或底色切换（`--id-bg-subtle`）反馈。
- 仅浮层元素（`NPopover` 下拉、移动端抽屉导航）保留轻量级阴影。

### 1.4 语义微高亮与按钮节制
- **KPI 大数字禁止“红绿灯式染色”**：监控指标主数值统一使用 `--id-text-primary`（深墨色），状态仅通过 **`6px` 语义圆点 + 简短状态词 / 微标签**表达（例如 `42ms` 保持中性深墨色，仅在旁侧标出红点 `1 名员工异常`）。
- **每区块最多 1 个 Primary 实心主按钮**：同一工具栏、卡片或表单区域内只允许出现 1 个 `type="primary"` 按钮；其余次要入口使用默认描边按钮或 `secondary` / `quaternary` 轻量按钮。
- **状态徽标（Micro-Badge）**：优先使用直角/小圆角（`border-radius: 4px`）微透明底 + `1px` 同色系半透明描边（如 `--id-status-danger-soft` + `--id-status-danger-border`）或 `6px` 实心圆点，避免大面积高饱和 `round` 胶囊抢夺正文视线。

---

## 2. 排版层级与中文信息密度

### 2.1 字体栈
- **UI 字体**：`--id-font-sans`（`Inter` 优先处理英文/数字，`"PingFang SC"` / `"Hiragino Sans GB"` / `"Microsoft YaHei"` 保障中文清晰度）。
- **等宽字体**：`--id-font-mono`（`JetBrains Mono`），严格用于 AI 员工编号（`#EMP-001`）、事项/任务 ID（`#WI-1042`）、API Endpoint、Cron 表达式、时间戳、指标数字和终端预览。

### 2.2 中英文混合字号梯度（针对中文可读性校准）
> 英文 UI 常用的 `11px` / `10px` 与负字距 `-0.02em` 在汉字下会出现笔画糊团与拥挤感，本规范明确禁用 `12px` 以下的正文/标签字号。

| 层级 | 字号 / 行高 / 字重 | 字距 | 适用元素 |
| --- | --- | --- | --- |
| **Page Title** | `18px–20px` / `28px` / `600` | 中文 `0`，纯英文可 `-0.015em` | 各一级页面顶部标题（禁止 `28px+` 营销大字） |
| **Section / Card Title** | `14px` / `20px` / `600` | `0` | 卡片标题、分区标题、抽屉标题 |
| **Body & Table Cell** | `13px–14px` / `20px–22px` / `400–500` | `0` | 列表行、表单项、表格单元格、对话消息 |
| **Meta / Label / Badge** | **`12px`（下限）** / `16px–18px` / `400–500` | `0` | 表头、辅助说明、时间、微徽标、坐标轴刻度 |
| **KPI Metric Value** | `20px–22px` / `28px` / `600` | `-0.02em` | 仅限纯数字/百分比/延迟指标 |

### 2.3 数字对齐与溢出防破版
- **强制等宽对齐**：所有数字指标、百分比、日期时间、金额/配额、列表数值列**必须**声明 `font-variant-numeric: tabular-nums;`。
- **单行截断**：动态标题、群聊名、长路径、表格单元格必须具备截断保护（`overflow: hidden; text-overflow: ellipsis; white-space: nowrap;`）并配合原生 `title` 属性悬浮展示全文。

---

## 3. 空间结构、对象差异化与交互模式

### 3.1 4px 栅格节奏
所有内边距、外边距与网格间距严格对齐 `4px` 倍数：`4 / 8 / 12 / 16 / 20 / 24px`（消除 `10px`、`14px`、`18px` 等零散像素值）。

### 3.2 「扁平分割列表」vs「对象化轻卡片」的边界
为兼顾高密度配置与“AI 员工/协同事项”对象心智，按场景区分容器形态：
- **配置表单、规则列表、审计活动流、任务流水表**：采用统一白底画布 + `1px solid var(--id-border-hairline)` 行分割线（`divide-y`），不套多层灰色嵌套小卡片。
- **AI 员工名录（`D2EmployeeAvatar`）、协同事项看板（Work Items）、高优待裁决卡片**：保留 `1px` 细边框与 `8px` 圆角（`--id-radius-lg`），禁止重度投影与 `3px` 粗彩条边框。

### 3.3 导航与弹层原则
- **页面主 Tabs**：优先使用无外框的极简下划线标签（`NTabs type="line"`）；卡片内部小型筛选（如事件分类）使用无投影的轻量胶囊或下划线 Tab，去掉厚重外框和阴影。
- **Overlay Drawer 优先于 Modal**：
  - 详情查看、日志排障、触发器/能力包/模型/渠道多字段配置，一律使用右侧 **`NDrawer`**（桌面宽度 `560px–680px`，移动端 `min(..., 95vw)`），保留主列表上下文。
  - **`NModal` 严格收敛**：仅用于破坏性拦截确认（拒绝审批、停用/删除、未保存草稿离开确认）或极简新建入口（<= 4 个字段）。

---

## 4. 四态闭环与反馈纪律（必检项）

任何数据驱动视图或异步面板必须完整实现以下四态，严禁留空或仅靠 Toast 报错导致白屏：

1. **Loading State（结构化骨架屏）**：
   - **首次加载**必须渲染匹配目标几何布局的 `<NSkeleton />`（防止 CLS 布局抖动），禁止仅展示 `"加载中..."` 居中文字或显示假 `0` 值。
   - **后台静默轮询刷新**不得触发骨架屏闪烁（通过 `hasLoadedOnce` 状态门控）。
2. **Empty State（可行动空状态）**：
   - 筛选无结果或无数据时展示 `<NEmpty />`：1 句清晰原因 + 1 个直接恢复动作（如 `[重置筛选]` 或 `[＋ 新建]`），禁止使用大型卡通插画。
3. **Error State（可恢复错误态）**：
   - 首次加载失败必须展示页面内联错误卡片/横幅，给出明确错误原因与 `[重试]` 按钮；已有缓存数据时刷新失败则展示轻量级非阻断提示条，保留旧数据可读。
4. **Interactive & Keyboard Feedback**：
   - 所有可交互行、按钮、指标卡必须有 `150ms` 颜色/边框过渡、清晰 `:focus-visible` 焦点环（`--id-focus-ring`）和禁用态。
   - 异步提交按钮使用 Naive UI `:loading` 并保持防连点；支持 `Esc` 关闭浮层/抽屉，顶栏全域搜索支持 `Cmd/Ctrl + K` 快捷聚焦。

---

## 5. Agent 控制面专属规范（业务扩展）

1. **高复杂配置页的渐进式披露（Progressive Disclosure）**：
   - 像 AI 员工详情这类多 Tab 重配置页，主面板只暴露核心开关、摘要胶囊（如 `已绑 3 群 · 12 成员`）与状态指示；正则模板、Cron 预览、沙箱细粒度权限、分源目录树等低频高级项默认折叠或下沉到二级 `NDrawer`。
2. **控制面异常与异步长状态统一表达**：
   - 乐观锁 `409` 版本冲突、未保存草稿提醒、幂等操作恢复提示，统一采用顶部固定高度 Inline Status Strip；执行流运行态统一显示 `6px` 脉冲状态点 + `tabular-nums` 耗时。
3. **1:1 对话流与终端日志排版**：
   - Web 1:1 对话框正文区域限制舒适阅读宽度（建议 `max-width: 780px`），Thinking 思考过程默认可折叠且使用 `--id-bg-subtle` 弱对比容器。
   - 指令拦截预览与执行日志终端（`.term-box`）统一采用 `--id-bg-terminal`（`#09090B`）+ `--id-font-mono` + 自动折行 + 右上角一键复制。

---

## 6. 工程与设计反模式清单（Negative List）

- **禁止**引入 Tailwind/shadcn 等非本仓库技术栈语法或手写第二套 UI 基础组件；一切优先基于 **Naive UI + [`d2ThemeOverrides`](../src/direction2/Direction2App.vue) + `--id-*` Tokens** 组合实现。
- **禁止**使用紫蓝霓虹渐变、毛玻璃卡片、`3px` 粗顶边彩条或大面积常驻 `box-shadow`。
- **禁止**在工作区内部放置无操作价值的营销型 Hero 横幅（如大块“欢迎使用某某智能中台”宣传框）或装饰性 Emoji。
- **禁止**使用 `10px`/`11px` 小字号展示业务信息，禁止在表格/KPI 中遗漏 `tabular-nums`。
- **禁止**使用原生 `alert()` / `confirm()`，禁止回归 Wave 3 测试禁用的旧版翡翠绿 Hex 色值，禁止写死会破坏 `< 1280px` 及移动端响应式的固定宽度。
