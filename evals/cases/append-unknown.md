---
id: "append-unknown"
tags: ["main-chat","append","supplement"]
source: ["followup-20/append-unknown"]
mock: "followup"
body: "u"
config: {"category":"supplement","kind":"supplement","title":"异常明细","status":"unknown","expected":[{"pattern":"未确认|无法确认|不能确认|还不确定|尚不确定|暂不确定|未知|待核对|还没.{0,5}确认","flags":""}],"history":"linked","tool":"task_delegation_send_message","change":"按异常等级分组","needed":[{"pattern":"异常等级","flags":""},{"pattern":"分组|分类","flags":""}],"fixtures":{"errorCode":"dependency_unavailable","followupRead":"no_matching_receipt","writeCount":1,"requireFollowupRead":true}}
---

异常明细那条任务，加上按异常等级分组。
