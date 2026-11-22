# 飞书按需历史预处理（开发中）

取代实时滑窗的按需路径：Manager 先完成使用权准入并登记持久 job，Hub 在每页请求前与最终提交前核验 job 租约和连接代次，使用当前员工的官方 SDK tenant 身份读取。此包不创建会话、不提交 Input、不存缓存、不自行修改业务水位。

Reader 只接受已确认的 `strategy=since_last_trigger`：取上次有效触发之后、本次触发之前最多 N 条；超过上限取区间最近 N 条，两个触发本身均不进入历史，本次触发由 Manager 在与历史合并的一条 user 输入中追加一次。首次没有前界时才取近期 N 条。Manager 在原生模型上下文提交得到确认后推进有效触发边界；仅队列受理不推进。后续 job 冻结已确认前界，重试复用原 job/快照，模型生成失败与上下文是否提交分别判断。Reader 不接受旧 recent 策略替代。

普通群倒序分页定位本次消息 ID，秒级 end_time 只是粗边界，两个锚点按真实返回消息顺序排除，不按 ID 字典序猜时间。同毫秒依靠 ID 定位；不足 N 却找不到已知前界时 unavailable，仅处理当前消息，不偷偷 recent。若已有 N 条且下一条仍严格晚于已知前界时间，可安全截断而不继续拉取很旧前界。找不到本次锚、分页重复/乱序、超预算、跨群、撤权均明确失败。原生 thread 暂不支持，不能回退读取群主线。

限制：每页50条、最多10页、最终100条/32KiB正文、单页1MiB。调用者另设请求与job总超时。结果按旧到新排列，保留来源ID/发送者类型；撤回和非文本消息保留占位但不下载附件。读取过程不是飞书服务端原子快照，不承诺跨页期间编辑/撤回的一致性；Manager首次接受的快照才是后续幂等执行事实，重试不得覆盖。

Manager 保存原始触发文本，历史作为带来源的引用上下文准备；历史不是授权事实或新的用户指令。服务端授权失败不得请求飞书，不把 app_secret/token 返回模型。Manager/Engine/模拟Hub的集成验收与真实租户读取须分别记录，不以保存配置或模拟通过宣称真实飞书权限已验证。

官方依据：https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/reference/im-v1/message/list
接口 `GET /open-apis/im/v1/messages`，page_size 1..50、分页期间排序不变；thread 不支持时间范围参数。实时 group_live_messages 滑窗不复用。

验证：`go test -race ./internal/channelhistory`。覆盖先授权、锚点/同秒后续消息排除、跨页去重、预算、撤权丢弃、错误游标、thread不越界、快照digest与lease无关；不代表真实租户授权或消息读取已验收。
