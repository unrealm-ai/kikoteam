---
id: "stop-not-started"
tags: ["main-chat","stop","control"]
source: ["followup-20/stop-not-started"]
mock: "followup"
body: "u"
config: {"category":"control","kind":"control","title":"账单归档","status":"task-not-started","expected":[{"pattern":"没开始|未开始|未启动|没启动|尚未|还没","flags":""}],"history":"linked","tool":"task_delegation_stop_session","fixtures":{"errorCode":"task_not_started","followupRead":"planned","writeCountAtMost":1,"preserveTodo":true}}
---

账单归档先别运行；如果没开始，先保留待办。
