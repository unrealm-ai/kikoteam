---
id: "stop-ambiguous"
tags: ["main-chat","stop","control"]
source: ["followup-20/stop-ambiguous"]
mock: "followup"
body: "u"
config: {"category":"control","kind":"control","title":"库存整理","status":"ambiguous","expected":[{"pattern":"哪(一|两)?(条|个|项|份)|确认|要停|指的是","flags":""},{"pattern":"东区|西区|两(条|个|项|份)|同名","flags":""}],"history":"no-link","tool":"task_delegation_list_sessions","fixtures":{"scopeList":["库存整理·东区","库存整理·西区"],"writeCount":0}}
---

帮我把库存整理停了。
