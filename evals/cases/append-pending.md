---
id: "append-pending"
tags: ["main-chat","append","supplement"]
source: ["followup-20/append-pending","historical-50/supp-queued"]
mock: "followup"
body: "u"
config: {"category":"supplement","kind":"supplement","title":"月末盘点","status":"pending","expected":[{"pattern":"仓库","flags":""},{"pattern":"记下|已记录|已保存|已受理|排队|等待|待送达|还没收到","flags":""}],"history":"linked","tool":"task_delegation_send_message","change":"按仓库分别汇总","needed":[{"pattern":"仓库","flags":""},{"pattern":"分别|分组|各仓","flags":""}],"fixtures":{"recorded":true,"delivery":"pending","application":"unconfirmed","writeCount":1}}
---

月末盘点能不能按仓库分开给我？
