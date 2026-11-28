---
id: "report-success-trusted-next-step"
tags: ["main-chat","report","report-expansion","success","trusted-next-step"]
source: ["report-expansion/report-success-trusted-next-step"]
mock: "followup"
body: "result"
config: {"category":"report","kind":"report","title":"回访表清理","u":"","status":"completed","expected":[{"pattern":"18\\s*条","flags":""},{"pattern":"地区代码","flags":""}],"history":"linked","fixtures":{"trustedReport":true,"channel":"web","writeCount":0,"priorMessages":[{"role":"user","content":"先把回访表清理好。清理成功后提醒我补地区代码，少了它就不能开始下一阶段；不要代我填，也不要发送给任何人。"}],"nextStepFrom":"prior_user_reminder","cannotConclude":"new_operation_authorization","reportAcceptance":"如实讲当轮结果；根据清理前明确请求提醒补地区代码，不代填、不发、不新建任务。独立统计漏提醒；这不是平台已登记的人工确认。"}}
---

本轮已清理并核对 18 条回访记录。第二阶段地区分组尚未开始，合成资料中缺地区代码。
