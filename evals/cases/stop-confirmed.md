---
id: "stop-confirmed"
tags: ["main-chat","stop","control"]
source: ["followup-20/stop-confirmed","historical-50/stop-confirmed"]
mock: "followup"
body: "u"
config: {"category":"control","kind":"control","title":"货架校对","status":"stopped","expected":[{"pattern":"已.{0,8}(停止|停下|结束|终止)|当前.{0,12}(没|无|没有).{0,10}(运行|排队)|没有.{0,6}在跑","flags":""}],"history":"linked","tool":"task_delegation_stop_session","fixtures":{"controlResponse":"stopped","noActiveOrQueued":true,"writeCount":1,"cannotConclude":"external_rollback"}}
---

把货架校对停掉，不用保留我这轮之前排的活。
