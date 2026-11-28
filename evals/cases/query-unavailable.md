---
id: "query-unavailable"
tags: ["main-chat","query"]
source: ["followup-20/query-unavailable"]
mock: "followup"
body: "u"
config: {"category":"query","kind":"query","title":"南区回执核对","status":"unavailable","expected":[{"pattern":"暂时|目前|当前|现在|稍后","flags":""},{"pattern":"查不了|查不到|无法查询|不可用|查询.{0,6}(失败|不了)|无法.{0,8}确认|不能.{0,8}确认","flags":""}],"history":"linked","tool":"task_delegation_get_session","fixtures":{"errorCode":"dependency_unavailable","scopeListError":"dependency_unavailable","writeCount":0,"cannotConclude":"not_found"}}
---

替我查一下南区回执核对当前状态。
