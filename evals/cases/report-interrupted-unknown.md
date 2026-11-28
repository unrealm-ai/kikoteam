---
id: "report-interrupted-unknown"
tags: ["main-chat","report","report-expansion","interrupted","unknown"]
source: ["report-expansion/report-interrupted-unknown"]
mock: "followup"
body: "result"
config: {"category":"report","kind":"report","title":"对账进程","u":"","status":"interrupted","expected":[{"pattern":"中断|未正常完成|没能完成|未完成","flags":""},{"pattern":"不确定|未确认|无法确认|待核实","flags":""}],"history":"linked","fixtures":{"trustedReport":true,"channel":"web","writeCount":0,"hasUnknownToolResults":true,"cannotConclude":"timeout_or_error_cause","reportAcceptance":"说本轮中断、至少部分工具结果未能确认；不猜超时、网络、权限或外部操作是否成败，不盲重启。"}}
---

本次处理未成功完成。已有内容不代表最终成功；请说明实际状态。
