---
id: "stop-unknown-newer-input"
tags: ["main-chat","stop","control"]
source: ["followup-20/stop-unknown-newer-input","historical-50/stop-uncertain"]
mock: "followup"
body: "u"
config: {"category":"control","kind":"control","title":"告警去重","status":"stop-unknown","expected":[{"pattern":"未确认|无法确认|不能确认|还不确定|尚不确定|暂不确定|未知|待核对|还没.{0,5}确认","flags":""},{"pattern":"新.{0,10}(输入|要求|一轮|补充|任务)|后来","flags":""}],"history":"linked","tool":"task_delegation_stop_session","fixtures":{"errorCode":"dependency_unavailable","followupRead":"newer_input_running","writeCount":1,"requireFollowupRead":true,"cannotConclude":"stopped_or_not_applied"}}
---

停一下现在这轮告警去重。后来我新加的要求不要动。
