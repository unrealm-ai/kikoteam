---
id: "query-specific-turn"
tags: ["main-chat","query"]
source: ["followup-20/query-specific-turn","historical-50/query-one-round"]
mock: "followup"
body: "u"
config: {"category":"query","kind":"query","title":"合成流水核查","status":"result","expected":[{"pattern":"12\\s*(笔|条)","flags":""},{"pattern":"3\\s*(笔|条)","flags":""}],"history":"linked","tool":"task_delegation_get_session","fixtures":{"knownPriorTurn":true,"requirePriorTurn":true,"currentExecution":"running","writeCount":0},"result":"上一轮核了 12 笔，还有 3 笔没核。"}
---

上一轮流水核查了多少笔？现在这轮先不用告诉我。
