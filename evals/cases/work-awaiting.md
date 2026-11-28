---
id: "work-awaiting"
tags: ["main-chat","work-item"]
source: ["historical-50/work-awaiting"]
mock: "historical"
body: "u"
config: {"category":"work-item","title":"交接灰度周报","status":"awaiting_acceptance","expected":{"pattern":"待验收|等待.{0,12}验收|尚未验收|送审|验收中","flags":""},"kind":"work-item","history":"linked","tool":"work_item_get_current"}
---

之前交给他们的灰度周报，到验收那步了吗？
