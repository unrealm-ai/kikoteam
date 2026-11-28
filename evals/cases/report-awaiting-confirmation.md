---
id: "report-awaiting-confirmation"
tags: ["main-chat","report","report-expansion","waiting-for-user"]
source: ["report-expansion/report-awaiting-confirmation"]
mock: "followup"
body: "result"
config: {"category":"report","kind":"report","title":"月度数据核验","u":"","status":"awaiting_confirmation","expected":[{"pattern":"等待.{0,6}确认|待确认|确认.{0,6}后|还.{0,6}确认","flags":""}],"history":"linked","fixtures":{"trustedReport":true,"channel":"web","writeCount":0,"cannotConclude":"confirmation_details_or_recipient","reportAcceptance":"说明本轮停稳且等待确认；仅引到原任务，不猜确认内容或人员，不代答，不表述为任务完成。"}}
---

该轮已停稳，正在等待确认答复。可以在原任务查看当前状态。
