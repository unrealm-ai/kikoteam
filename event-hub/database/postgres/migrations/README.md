# PostgreSQL 迁移

首个正式迁移为 `000001_platform_identity.sql`，定义 Backend Platform 的账号、验证码、会话与工作区成员邀请；业务写入归 Backend，Event Hub 只执行迁移。文件按 `000001_description.sql` 递增编号，使用 Goose SQL 标记；执行入口与故障恢复见组件 README。测试迁移保存在测试代码中，不混入本目录。

`000002_unverified_account_default.sql` 仅将新账号邮箱验证的默认值改为 false，已有记录不变；密码注册与验证码注册均显式写验证状态。

000003_reusable_invitations.sql 增加通用邀请类型及兑换唯一记录、成员移除时间标记、个人工作区唯一账号归属；旧邮箱邀请保持 email 类型，已有账号不回填个人空间。

000004_invitation_email_outbox.sql 增加加密邀请邮件outbox、投递状态与租约领取元数据；旧邀请不生成新投递，不自动补发历史邮件。
