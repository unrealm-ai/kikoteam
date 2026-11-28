---
id: "report-failed-trusted-context"
tags: ["main-chat","report","report-expansion","failure","supplemental-history"]
source: ["report-expansion/report-failed-trusted-context"]
mock: "followup"
body: "result"
config: {"category":"report","kind":"report","title":"测试导出","u":"","status":"failed","expected":[{"pattern":"本轮|这次|此次","flags":""},{"pattern":"没成功|未.{0,5}完成|失败","flags":""},{"pattern":"配置.{0,5}(不可用|有问题)|未能.{0,6}启动","flags":""}],"history":"linked","fixtures":{"trustedReport":true,"channel":"web","writeCount":0,"causeFrom":"prior_scoped_get_session_turn_error","cannotConclude":"unseen_backend_cause","reportAcceptance":"说明本轮失败；用此前对同一会话同一轮次的真实形状查询回执明确定位「执行配置不可用」，不说主动回告自身提供错误字段，不猜是哪项设置或凭据，不自动重试。","priorCauseCode":"configuration_unusable"}}
---

本次处理未成功完成。已有内容不代表最终成功；请说明实际状态。
