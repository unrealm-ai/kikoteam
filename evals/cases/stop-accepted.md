---
id: "stop-accepted"
tags: ["main-chat","stop","control"]
source: ["followup-20/stop-accepted","historical-50/stop-active"]
mock: "followup"
body: "u"
config: {"category":"control","kind":"control","title":"测试汇总","status":"stopping","expected":[{"pattern":"停.{0,7}(止中|稳|请求|受理|处理中)|正在停|还没停|未确认|提交.{0,8}停止|收尾","flags":""}],"history":"linked","tool":"task_delegation_stop_session","fixtures":{"controlResponse":"stopping","writeCount":1,"cannotConclude":"stopped"}}
---

测试汇总先别跑了，停一下。
