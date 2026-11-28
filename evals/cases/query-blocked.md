---
id: "query-blocked"
tags: ["main-chat","query"]
source: ["historical-50/query-blocked"]
mock: "historical"
body: "u"
config: {"category":"query","title":"发票抽查","status":"blocked","expected":{"pattern":"(权限|访问|授权).{0,15}(拒绝|不足|缺失|没有|阻塞)|受阻|阻塞","flags":""},"kind":"query","history":"linked","tool":"task_delegation_get_session","reason":"source_access_denied"}
---

看一下发票抽查卡在哪里了。
