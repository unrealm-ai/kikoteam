---
id: "append-not-delivered"
tags: ["main-chat","append","supplement"]
source: ["followup-20/append-not-delivered"]
mock: "followup"
body: "u"
config: {"category":"supplement","kind":"supplement","title":"门店巡检","status":"not-delivered","expected":[{"pattern":"没送到|未送达|未收到|没有.{0,8}(收到|传到)|取消|未传|没传|未能.{0,4}(送达|交给)","flags":""}],"history":"linked","tool":"task_delegation_send_message","change":"先核对夜班和空岗记录","needed":[{"pattern":"夜班","flags":""},{"pattern":"空岗","flags":""}],"fixtures":{"recorded":true,"delivery":"not_delivered","application":"unconfirmed","writeCount":1}}
---

门店巡检补一下，夜班和空岗的记录也看看。
