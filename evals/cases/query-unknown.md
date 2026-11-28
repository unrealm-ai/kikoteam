---
id: "query-unknown"
tags: ["main-chat","query"]
source: ["historical-50/query-unknown"]
mock: "historical"
body: "u"
config: {"category":"query","title":"告警复核","status":"unknown-result","expected":{"pattern":"未知|还.{0,5}(确认不了|不确定)|无法确认|未核实|待核对","flags":""},"kind":"query","history":"linked","tool":"task_delegation_get_session"}
---

能确认上次告警复核工具是否已产生结果吗？
