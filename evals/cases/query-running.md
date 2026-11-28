---
id: "query-running"
tags: ["main-chat","query"]
source: ["followup-20/query-running","historical-50/query-running"]
mock: "followup"
body: "u"
config: {"category":"query","kind":"query","title":"灰度周报","status":"running","expected":[{"pattern":"正在|还在|进行中|运行中|执行中|处理着","flags":""}],"history":"linked","tool":"task_delegation_get_session","fixtures":{"writeCount":0}}
---

灰度周报现在跑到哪了？我不想再建一份。
