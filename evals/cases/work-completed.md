---
id: "work-completed"
tags: ["main-chat","work-item"]
source: ["historical-50/work-completed"]
mock: "historical"
body: "u"
config: {"category":"work-item","title":"对账归档","status":"completed","expected":{"pattern":"已通过验收|验收已通过|已经验收通过|已验收通过|验收通过了","flags":""},"kind":"work-item","history":"linked","tool":"work_item_get_current"}
---

查一下对账归档交接事项是否已经验收通过。
