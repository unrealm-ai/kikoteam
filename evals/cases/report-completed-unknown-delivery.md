---
id: "report-completed-unknown-delivery"
tags: ["main-chat","report","report-expansion","success","unknown"]
source: ["report-expansion/report-completed-unknown-delivery"]
mock: "followup"
body: "result"
config: {"category":"report","kind":"report","title":"对账明细草稿","u":"","status":"completed","expected":[{"pattern":"19\\s*条","flags":""},{"pattern":"草稿","flags":""},{"pattern":"未确认|无法确认|未.{0,5}凭证|待核实","flags":""}],"history":"linked","fixtures":{"trustedReport":true,"channel":"web","writeCount":0,"hasUnknownToolResults":true,"cannotConclude":"external_delivery","reportAcceptance":"区分本轮草稿成果与外部登记未知，不能把 completed 当送达、不说已失败、不重发。"}}
---

本轮完成了 19 条明细汇总，已生成文字草稿；推送到外部登记处的结果还没有凭证。
