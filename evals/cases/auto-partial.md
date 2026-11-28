---
id: "auto-partial"
tags: ["main-chat","report","auto-report"]
source: ["historical-50/auto-partial"]
mock: "historical"
body: "result"
config: {"category":"auto-report","title":"演习手册","status":"completed","expected":[{"pattern":"4\\s*条","flags":""},{"pattern":"附录.{0,18}(未|缺|待)|(?:还|仍|待).{0,12}附录","flags":""},{"pattern":"草稿|未定稿|并非定稿","flags":""}],"kind":"report","queued":1}
---

演习手册已列出 4 条检查项，风险附录尚未补齐；已有前两页备注仅为草稿。
