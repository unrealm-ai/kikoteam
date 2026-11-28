---
id: "cancel-unstarted"
tags: ["main-chat","stop","control","todo"]
source: ["historical-50/cancel-unstarted"]
mock: "historical"
body: "u"
config: {"category":"control","title":"下周台账待办","status":"canceled","expected":[{"pattern":"取消","flags":""},{"pattern":"尚未启动|未开始|未启动|没开始|没有启动|待办","flags":""}],"kind":"control","history":"linked","tool":"task_delegation_cancel_todo"}
---

把这条没开始的下周台账待办取消，不要启动。
