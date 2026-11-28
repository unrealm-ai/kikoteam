---
id: "report-failed-no-cause"
tags: ["main-chat","report"]
source: ["followup-20/report-failed-no-cause","historical-50/auto-failed"]
mock: "followup"
body: "result"
config: {"category":"report","kind":"report","title":"单据核查","u":"","status":"failed","expected":[{"pattern":"未能|失败|没成功|没完成|未完成|受阻|无法完成","flags":""}],"history":"linked","fixtures":{"trustedReport":true,"channel":"web","writeCount":0,"cannotConclude":"cause_not_provided"}}
---

本次处理未成功完成。已有内容不代表最终成功；请说明实际状态。
