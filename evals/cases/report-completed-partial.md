---
id: "report-completed-partial"
tags: ["main-chat","report"]
source: ["followup-20/report-completed-partial","historical-50/auto-result"]
mock: "followup"
body: "result"
config: {"category":"report","kind":"report","title":"容量巡检","u":"","status":"completed","expected":[{"pattern":"7\\s*个","flags":""},{"pattern":"2\\s*个","flags":""},{"pattern":"N7|数据缺口","flags":"i"}],"history":"linked","fixtures":{"trustedReport":true,"channel":"web","writeCount":0,"cannotConclude":"overall_complete"},"queued":1}
---

本轮扫了 7 个节点，2 个接近阈值；N7 的数据缺口仍要下轮核对。
