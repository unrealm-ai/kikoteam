---
id: "append-received"
tags: ["main-chat","append","supplement"]
source: ["followup-20/append-received","historical-50/supp-received","historical-50/supp-preserve"]
mock: "followup"
body: "u"
config: {"category":"supplement","kind":"supplement","title":"采购对账","status":"received","expected":[{"pattern":"币种","flags":""},{"pattern":"收到|接收|传达|转达|送达|交给|补上|加上|记下","flags":""}],"history":"linked","tool":"task_delegation_send_message","change":"按币种分组，其他限制不变","needed":[{"pattern":"币种","flags":""},{"pattern":"分组|归类|汇总","flags":""},{"pattern":"(?:不|不许|禁止|不得|别).{0,12}(?:外发|发送|分享)|(?:既有|原来|之前|原有|原先).{0,15}(?:限制|约束|要求).{0,10}(?:不变|照旧|保留)|(?:只读|不外发)","flags":""}],"fixtures":{"recorded":true,"delivery":"received","application":"unconfirmed","writeCount":1,"originalRestrictions":["只读","不外发"]}}
---

刚才的采购对账再按币种分一下，结果还是先给我看。
