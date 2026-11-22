# Goose 外部持锁连接适配

这是随 Event Hub 提交的受控源码副本，通过组件 `go.mod` 的 `replace` 使用，不是上游已经提供的 API。基线为 [pressly/goose v3.30.1](https://github.com/pressly/goose/tree/v3.30.1)，提交 `ded3331f49ba36e8986082b8b74918571d420d0e`。保留原始 MIT [LICENSE](LICENSE)。

原模块校验值：

```text
github.com/pressly/goose/v3 v3.30.1 h1:KO28meBdbAUIdMbTP7bl9dp1FYZGXcYWb2O7miT09lE=
github.com/pressly/goose/v3 v3.30.1/go.mod h1:CHGwuG2acDerUaXwz2i5T18VSauXpYDiL4WxzDvcOIM=
```

仅导入根库及其本模块依赖包的非测试 Go 文件、原始 `go.mod` 和许可证；不导入 CLI、上游测试及其夹具。`upstream-files.json` 记录导入文件的原始 SHA-256；`connection.patch` 是完整本地差异。Event Hub 的真实 PG 测试验证本项目所需行为，不代表运行了上游全部测试。

本目录 `.gitattributes` 仅对补丁文本以及上游原本带行尾空格的 `internal/dialects/dsql.go`、`ydb.go` 关闭 Git 空白告警，避免改变补丁上下文或上游原文。源码哈希和补丁完整性仍由 `make check-goose` 核验，自有及适配 Go 文件继续执行 gofmt 检查。

## 适配范围

新增 `NewProviderWithConn(goose.DialectPostgres, conn, fsys, opts...)`：

- 借用调用方已经持锁的 `*sql.Conn`；版本表初始化、状态读取、迁移 SQL、版本写入及 Ping 全部使用它。
- 操作清理和 `Provider.Close` 均不归还、关闭或解锁该连接；只由调用方管理寿命。失败不重连，不从连接池取替代连接。
- 保留 Goose SQL 解析、事务、版本排序及执行逻辑。默认单条 SQL 迁移与版本记录在同一事务，支持显式 `NO TRANSACTION` 和 `StatementBegin/End`。
- 外部连接模式只支持 PostgreSQL SQL 迁移，禁用全局 Go 注册；拒绝显式 Go 迁移、额外锁、关闭版本记录、DDL 隔离和自定义 store。Event Hub 不调用 Down/Reset 等回退入口。
- Provider 的互斥仅保护其自身操作；调用方仍须串行化连接的全部外部使用。初始化失败不缓存成功状态，下一次操作重新核对版本表。
- 外部连接模式的 SQL 解析失败返回带版本号的 `PreparationError`，日志可定位文件编号而不暴露 SQL 原文；执行失败沿用上游 `PartialError`。
- 原始 `NewProvider(*sql.DB, ...)` 路径保持原语义。不能用 `SetMaxOpenConns(1)` 代替外部连接 API；那不能表达连接所有权，且可能死锁。

## 升级与核验

在 Event Hub 目录运行 `make check-goose`，脚本在临时目录反向应用补丁，逐文件核对上游哈希与文件清单。升级时重新下载指定版本、确认模块校验值，按相同依赖范围更新源码及清单，再重放并审阅适配差异、重新生成补丁。运行 `make check`，尤其核对借入连接的成功、失败、取消、状态查询、Close、事务外 SQL 和锁数量。新增上游选项或数据库访问路径须重新审查，不能仅以编译成功视为兼容。

若将来上游提供满足同等连接所有权约束的接口，再移除本地副本；当前不依赖未发布的上游修改或远程私有 fork。
