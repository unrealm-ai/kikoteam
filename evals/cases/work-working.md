---
id: "work-working"
tags: ["main-chat","work-item"]
source: ["historical-50/work-working"]
mock: "historical"
body: "u"
config: {"category":"work-item","title":"储位抽查","status":"working","expected":[{"pattern":"处理中|办理中|正在.{0,8}(处理|办理|执行)|进行中","flags":""},{"pattern":"合成经办员","flags":""}],"kind":"work-item","history":"linked","tool":"work_item_get_current"}
---

储位抽查现在谁在跟，做得怎么样了？
