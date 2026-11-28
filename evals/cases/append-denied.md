---
id: "append-denied"
tags: ["main-chat","append","supplement"]
source: ["followup-20/append-denied","historical-50/supp-denied"]
mock: "followup"
body: "u"
config: {"category":"supplement","kind":"supplement","title":"退货复核","status":"denied","expected":[{"pattern":"权限|访问.{0,6}(被拒|受限)|无法|未能|失败|没能","flags":""}],"history":"linked","tool":"task_delegation_supplement_task","change":"只查断货订单","needed":[{"pattern":"断货","flags":""},{"pattern":"只|限定|仅","flags":""}],"fixtures":{"errorCode":"access_revoked","writeCount":1,"cannotConclude":"recorded"}}
---

退货复核那份，帮我只看断货的订单就行。
