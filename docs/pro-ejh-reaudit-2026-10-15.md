# Pro 自动补货、Pro 管理与 EJH 再次审计（2026-10-15）

原始审计基于 `b22b70a` 完成；随后按用户要求，将本轮修复及回归测试同步到 `chapt-space-user`，保留该项目的模块名与原有业务差异。两个项目分别提交至各自仓库。本次未部署、重启业务服务或执行真实开卡付款。此前审计记录见 `pro-ejh-audit-2026-10-15.md`。

## 已复现并修复

| 问题 | 修复及业务影响 | 回归测试 |
| --- | --- | --- |
| 预计就绪耗时或时间目标超出预测范围，会连带阻断并发和最低备用规则 | 只暂停时间侧判断并显示原因，并发和备用规则继续独立计算，仍受在途及预算上限约束 | `TestForecastHorizonDoesNotSuppressOtherRules` |
| Sub2 模型白名单只按精确名称匹配，导致通配符模型账号被误判不可用 | 支持精确匹配和末尾星号前缀匹配，清理请求模型两端空白，并要求覆盖所有配置模型 | `TestMonitorReadySupportsSub2ModelPatterns` |
| 原订单读取或解密失败时仍可能启动全自动流程 | 启动前及锁内的二次检查均仅将无记录视为可继续；读取失败直接拒绝启动，避免继续领取或购买卡片 | `TestProAutoOrderReadFailureBlocksAdmission` |
| 调度直接比较带不同时区的时间字符串，导致恢复任务提前或延后 | 按实际时间点比较与排序，增加对应表达式索引 | `TestDueProAutomationComparesInstantsAcrossTimezones` |
| EJH 同步接口缺少列表或返回 null 时仍被记为成功 | 缺少有效卡片或流水列表则记录同步失败，保留上次成功时间；显式空数组仍合法 | `TestEJHSyncRejectsMissingLists` |
| 手动或旧定时任务的在途统计与付款结果不一致 | 付款前失败且已停止的任务释放统计占位；已付款或付款未知的停止任务保留在途；恢复已落盘但尚未关联的订单；未知付款不承诺未来容量 | `TestRestockExternalInflightTracksPaymentOutcome` |

以上六类问题均先由定向测试复现，再验证修复。本轮仅调整新补货对外部任务的统计，原定时任务的数量算法保留。登录、开通、推送到 Sub2 核验就绪的时间仍计入在途，未知结果不会因超时自动重复付款。

## 已有保护验证

`TestEJHImportCannotRebindOrderToDifferentPhysicalCard` 验证同一 EJH 订单无法改绑另一张实体卡，凭据不被替换，异常期间禁止领取，正确刷新后恢复。这是对既有保护的确认，不是本轮新增业务修复。

## 验证与边界

- 新增定向回归测试通过。
- `go vet ./...` 通过。
- `go build -o ../.codex-test-artifacts/space-merge-pro-audit.exe ./cmd/server` 通过，未启动产物。
- `gofmt` 和 `git diff --check` 通过。
- 全量 `go test ./... -count=1 -timeout 15m` 通过，退出码 0；HTTP API 包用时约 434 秒，数据库包约 161 秒。日志：`../.codex-test-artifacts/space-merge-pro-audit-full.log`。
- 本轮未修改前端，未重复前端构建或浏览器验收。环境 `CGO_ENABLED=0` 且无 gcc，未运行竞态检测。测试使用临时数据库与模拟接口。
- 自动执行依然依赖 Sub2 的 `strict=true`、`quality` 和 `load_known` 采集质量信息。本轮未修改或部署 Sub2，不能据本地模拟测试认定生产接口已经满足这些条件。
