---
id: "query-queued"
tags: ["main-chat","query"]
source: ["historical-50/query-queued"]
mock: "historical"
body: "u"
config: {"category":"query","title":"灰度周报","status":"queued","expected":{"pattern":"排队|没开始|未开始|等待开始|等候执行","flags":""},"kind":"query","history":"linked","tool":"task_delegation_get_session"}
---

查一下灰度周报任务现在到哪一步？
