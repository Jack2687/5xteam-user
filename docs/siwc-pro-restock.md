# Pro 与 SIWC

## 两种入口

- **Pro 管理 → 推送设置 → Sub2 / Sub2SIWC → 推送方式**：选择 Sub2SIWC，配置该站的地址、管理员、分组、模型和代理。普通 Sub2 与 Sub2SIWC 分别保存配置；切换后保存才生效。全自动开通保留首次登录会话、代理与出口，第三步改走 SIWC 授权。
- **Pro 管理 → 降智检测 → 降智后处理 → 推送 SIWC**：单独配置转入站及推送、监控分组。确认降智后先移除选中的原服务分组，再授权并推送。原本已经直接推送到 SIWC 的账号不会重复转入。

## 补货与退出

转入状态先落库，再操作上游。转入期间暂停新的自动补货和定时开通；已经购买的订单保留。推送成功后至少等待 10 秒，并取得新的、完整的可调度账号与并发数据，才恢复判断。

补货监控合并原监控组与 SIWC 监控组的并发、排队和容量。同一站的同一账号跨组只计一次，不同站相同数字 ID 分开计。空组、储备、并发、额度预测、退出补位、禁补放行、兜底及在途批次继续使用原规则。SIWC 数据不完整时暂停新增，不把查询失败当成零并发。

SIWC 5h、7d 可分别勾选“剩余 ≤ 阈值时退出”。原推送开始时间与推送超时不重置，退出后按现有合并／删除配置执行。SIWC 转入失败自动重试最多 3 次；页面可重试或结束转入，失败时不会偷偷恢复补货。

消耗额度显示 **原下游消耗 + SIWC 新消耗**，两种费用口径分别累计。重复读取、重试、重启不重复累加；原下游已在执行的请求晚结算仍会补入。

## SIWC 站

账号管理增加“智商情况”和 SIWC 5h／7d 进度条，工具栏配置检测题库、模型、周期、连续异常／恢复次数。检测请求走 SIWC 插件，不使用打票流程。网页用量使用同一登录流程取得的 Web AT，按授权 client ID 匹配应用；SIWC AT 本身不能用于网页用量接口。

管理员认证后可调用：

- `GET /api/v1/admin/account-quality/capabilities`
- `GET/PUT /api/v1/admin/account-quality/settings`
- `POST /api/v1/admin/account-quality/results`，body 为 `{"account_ids":[1]}`
- `GET /api/v1/admin/account-quality/accounts/:id/history`
- `POST /api/v1/admin/account-quality/run`、`run-selected`
- `GET /api/v1/admin/account-quality/progress`
- `GET/POST /api/v1/admin/siwc/accounts/:id/usage`（POST 强制刷新）
- `POST /api/v1/admin/siwc/usage`，body 为 `{"account_ids":[1]}`

读取失败保留上次数据并显示原因，Space 不据此执行额度退出。网页登录凭证到期时 Space 重新授权并同步凭证；独立导入且没有 Web AT 的账号需重新获取 SIWC 凭证。
