# Platform modelproxy

官方模型、员工代理凭据、Token 用量和积分结算运行在 Backend 的 Platform 进程内。

- `controller` 提供原生模型协议入口和资源接口，复用 Platform 的会话、CSRF、请求校验和错误格式。
- `service` 管理模型测试、配置保存及员工凭据。注册赠送和 Key 初始化参与调用方事务。
- `repository` 负责模型、钱包、流水、用量和补偿的持久化。
- `integration` 转发模型请求，并将三种协议的实际用量归一化。

Backend 运行时配置只向通过授权的 Engine 下发员工代理 Key。代理读取供应商凭据，按员工绑定的账户结算。数据库迁移与周期补偿由 Event Hub 触发。

契约、部署边界及可复现的验收步骤见[模型代理技术设计](../../../docs/model-proxy.md)。
