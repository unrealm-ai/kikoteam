# 主对话题库

一套主对话题库，初始保留 **42 道**。Markdown 正文只写真实用户会说的话，或平台内部回告原文；身份、附件、工具契约、状态与验收规则放在场景配置及模拟器中，不塞进题面。来源见各题的 `source` 与[筛选说明](AUDIT.md)，全部是合成数据。

## 本地执行（Node 24，无 npm 依赖）

```sh
node evals/run.mjs --dry-run
node evals/run.mjs --tag query --dry-run
node evals/run.mjs
node evals/run.mjs --tag report
node evals/run.mjs --tag report-expansion --dry-run
node --test evals/tests/*.test.mjs
```

不加标签选全部；重复传标签取交集。标签示例 query、append、stop、report、create、work-item。默认是脚本化离线回放，不给出模型文风结论。结果默认写系统临时目录；--out 只接受 checkout 外、父目录已存在的全新目录。模拟器不会操作项目文件、启动任务、访问数据库或发送消息。CI 只做本地确定性回放及 dry-run，不使用模型凭据、不上传结果。

任务查询、补充、停止、待办取消和可信回告使用当前主干的 `task_id` 契约；工具结果不带内部 `session_id`。创建成功回执只给 `accepted`、`task_id` 与 `input_id`。

`report` 覆盖全部回告；`report-expansion` 只选后增的 6 道。同一题可兼有成功、未知、等待用户和补充历史等标签。普通回告没有独立的超时或具体错误字段：中断不能说成超时，工具结果未知不能说成成功或失败；有同会话、同轮次且经授权的历史查询证据时，才可说明可证实的具体原因。

## 改题

cases/<id>.md 一题一文件。开头 front matter 采用可无依赖解析的 YAML 子集：每行一条，冒号后的值为 JSON，例如 `tags: ["main-chat","query"]`。id 必须与文件名一致；source 保存不重叠的原始题目身份；mock 是内部模拟器选择，不是分集；body 指 Markdown 正文写用户文本 u 还是可信报告中的合成 result。结构化场景配置与辅助正则放在 config（正则用 `{"pattern":"...","flags":""}`）。报告授权依据夹具可信状态，不依据正文标题。

## 已授权才用真实模型

只有确认费用和数据范围后，才显式提供 OpenAI-compatible HTTPS base URL（如带 /v1 的根路径）、模型 ID 与凭据三项环境变量：KIKO_EVAL_BASE_URL、KIKO_EVAL_MODEL、KIKO_EVAL_API_KEY，并传 --live。runner 不读产品数据库、产品 .env、登录缓存或备用凭据，不跟随重定向。不要将任何凭据写进命令历史或评审包。

```sh
node evals/run.mjs --live --tag report --max-requests 30 --concurrency 2 \
  --profile /tmp/profile-a.json --profile /tmp/profile-b.json
```

profile 放在 checkout 外，内容仅使用合成上下文，JSON 字段：id 唯一，可选 systemPrompt、reportPrompt、toolDescriptions（按现有工具名）、createReceipt（accepted 或 legacy）。省略时使用当前 checkout 的提示与工具描述及 accepted 回执。工具名和参数 schema 不可覆盖。两组共享题面、历史、可信来源、模型参数、模拟事实与停止规则；只有显式选择旧回执时，立即创建的成功回执才有指定差异。真实模型模式仍只模拟聊天补全，不是实际 Pi、人工确认或渠道投递。

design.json 记录 git SHA、题面／mock／prompt／工具指纹、标签、参数和预算。runs.jsonl 含响应、工具事件及结束原因；summary.json 仅汇总客观调用。真实模型才输出 review.json（供盲评）与 private-review-key.json（对照键，不转给审阅人）。真正的模型输出、评审和对照键不入库。本轮、模型输出和整批费用截断分开，不用三步当成产品上限。见[事实和自然度规则](rubrics/main-chat.md)。
