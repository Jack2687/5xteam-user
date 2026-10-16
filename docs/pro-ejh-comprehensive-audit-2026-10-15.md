# Pro 自动补货、Pro 管理与 EJH 本地全面复审（2026-10-15）

本轮在 `a0a3468` 已有修复基础上继续审计，并按用户后续要求同步到 `chapt-space-user`（基线 `040c256`），分别验证和提交至各自 GitHub 仓库。同步采用针对性补丁，保留模块名和项目业务差异。此前六项修复见 `pro-ejh-reaudit-2026-10-15.md`，不重复记为本轮发现。

## 本轮发现与修复

| 级别 | 问题及触发条件 | 修复后的行为 | 主要回归证据 |
| --- | --- | --- | --- |
| P1 | 启动流程等待 EJH 开卡时收到停止指令，随后旧启动请求清除停止状态，继续登录；显式续跑中再次停止也存在此问题 | 持久化停止版本，在启动、排队取卡、提交自动开卡前及最终启动时核对。旧请求被拒绝；新的显式启动仍可续跑，复用已经签发的可用卡。定时任务不能清除用户停止状态 | `TestProStopDuringCardIssuanceWinsOverStartup`、`TestProStopWhileWaitingForCardPreventsIssuance`，覆盖手动、定时、再次停止和两层卡片排队锁 |
| P1 | 下游返回负数或超过 100 的额度，仍被保存；全自动流程可能错误标记已就绪，超过阈值还会触发空间合并 | 5 小时及 7 天额度只接受有限的 0～100 百分比；失败保留有效历史额度与时间戳，本次不触发额度驱动合并；全自动尾程继续等待后续有效查询 | `TestProAutoRejectsOutOfRangeQuotaBeforeMerge`、`TestProQuotaEndpointRejectsInvalidPercentAndKeepsSnapshot` |
| P1 | 查询额度期间切换下游服务器或账号 ID，旧接口响应仍覆盖当前账号额度，并可能用于合并判断 | 保存前复核连接和账号身份；配置保存与额度接纳共用锁，落库时再次核对账号绑定；身份变化时丢弃旧响应。自动合并判断读取当前开关与阈值 | `TestProQuotaDiscardsResponseAfterConnectionChange`，修复前两种情况均复现旧额度落库，修复后不更新、不启动合并 |
| P1 | Sub2 返回缺失/null 账号列表或负载映射时，被当作真实空池；分页期间总数缩减也可能被接受 | 缺失或 null 字段、负数总数及分页总数变化均拒绝；显式空列表、空负载映射和稳定分页仍合法。错误结果不进入正常补货决策 | `TestMonitorAccountsRequiresExplicitCompleteList`、`TestMonitorAccountsRejectsChangingTotal`、`TestMonitorConcurrencyRequiresExplicitAccountMap` |
| P2 | 补货页后台刷新与暂停、评估、重置交叉完成，旧响应覆盖新状态；翻页响应乱序；首次加载未结束即离开页面时重新创建定时器 | 使用项目已有最新请求管理器，操作后旧状态响应失效，后台轮询不抢占操作，记录响应以当前请求为准；卸载取消读取并禁止重建轮询 | `frontend/scripts/check-pro-restock-requests.mjs`，模拟延迟与乱序响应，卸载计时器缺陷在修复前复现 |

迁移续跑初始化自动流程时一并保留停止版本，避免新加入的停止记录被初始化清空。前端静态资源已重新构建。

## 审计覆盖

- 自动补货：时间/并发/备用决策、预算与在途、原子领取、派发及重启恢复、付款未知和已付款恢复、Sub2 分页/负载质量、异步额度缓存。
- Pro 管理：启动与停止、登录/开通/OAuth/推送/额度/合并状态链、单号和批量查询、迁移续跑、账号和卡片占用。
- EJH：认证缓存、DES/ECB/PKCS5 请求封装、对象及 JSON 字符串形式的 `data`、余额/卡片/流水解析、开卡及卡片操作幂等、未知结果防重、自动开卡预算与排队。
- 前端：补货页面操作与后台刷新、卸载和分页；EJH、选卡、Pro 下游配置、迁移及消费展示的既有回归脚本。

EJH 通用解析器的对象/字符串兼容能力继续保留，本轮未改变供应商协议或增加资金操作自动重试。

## 本轮验证

- 新增后端定向回归通过，同时覆盖原有迁移续跑、消费统计与 EJH 客户端用例。
- `go vet ./...` 通过。
- `go build -o ../.codex-test-artifacts/space-merge-comprehensive-audit.exe ./cmd/server` 通过，未启动构建产物。
- 前端 `npm run check` 通过。
- 前端 `test:pro-restock`、`test:ejh`、`test:card-allocation`、`test:pro-vm`、`test:pro-cost`、`test:mail-pro-transfer`、`test:push-groups`、`test:list-requests` 通过。
- 前端 `npm run build` 通过；仅提示主 JS 包超过 500 kB，不影响构建完成。
- 修改的 Go 文件 `gofmt` 与 `git diff --check` 通过。
- 全量 `go test ./... -count=1 -timeout 15m` 通过，退出码 0；HTTP API 包 472.963 秒，数据库包 103.803 秒。日志 `../.codex-test-artifacts/space-merge-comprehensive-audit-full.log`。

## 边界

使用本地模拟 HTTP 服务和临时数据库；本轮未访问生产服务、真实开卡或付款，未部署或重启。代码按用户要求分别提交并推送各自 GitHub，具体提交结果见任务交付。已经发送给供应商的开卡请求不能由本地停止按钮撤销；修复保证旧启动请求不再覆盖停止状态，已签发结果保留供核对或后续复用。

未做浏览器视觉验收；前端验证为脚本行为检查、类型检查及构建。环境 `CGO_ENABLED=0` 且没有 gcc，未运行 Go race detector。生产 Sub2 仍须提供 `strict=true`、`quality`、`load_known`；本地通过不等同于生产联调或全部缺陷清零。
