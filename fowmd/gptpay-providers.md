# Pro 开通供应商与 Pro 50x

Pro 全自动配置 → 开通设置中的「供应商套餐与支付地区」按三行列出 GPTPay · Tokenseek、GPTPay · CMSNav、序付 · XUPAY。打开时回填各家已有地址、密钥配置状态、套餐、支付国家和条款选择；当前使用的供应商显示「主启用」，可另选一家「备启用」，其余「未启用」。不设置支付顺序。每次新任务默认使用主供应商，已有订单沿用原快照。当前版本仅保存备用选择，尚未接入自动切备，页面明确提示。

至少保留一个主供应商，最多一个备用，两者不能相同。将备用改为主供应商时清空原备用选择，其他行保持原资料。修改点击底部「保存 Pro 配置」后生效，未保存时标明「待保存生效」。旧配置自动回显原供应商为主启用，默认不指定备用。

每行可直接修改套餐与地区；「配置」展开地址、API Key、条款、目录刷新和套餐默认国家。原密钥不回传，留空保留各自加密密钥。编辑未启用行不会切换当前供应商。三家配置和自动化设置在同一事务中保存，任一失败全部回滚；旧版单供应商保存接口继续兼容。银行卡名额上限与登录会话保留时间共用。序付配置和状态规则见 [序付开通供应商](xupay-provider.md)。

| 供应商 | 默认 API 地址 | 支持套餐 |
| --- | --- | --- |
| Tokenseek | `https://gptpay.tokenseek.app/api/v1` | Go、Plus、Pro 5x/20x/50x、Plus 升级、Codex 点数 |
| CMSNav | `https://gptpay.cmsnav.com/api/v1` | Pro 5x、Pro 20x、Pro 50x |
| 序付 · XUPAY | `http://104.160.172.162:5496/api/v1` | Pro 5x、Pro 20x |

Tokenseek 保持现有协议。CMSNav 依据 <https://gptpay.cmsnav.com/docs> 和 <https://gptpay.cmsnav.com/api/openapi.json>（2026-09-30）接入：

- `GET /api/catalog` 公开查询国家、货币、套餐积分价格及可用状态，不发送 API Key。打开列表时自动读取各家的目录，也可在行内点击「刷新国家与套餐」。支付国家下拉显示名称、代码及币种；Pro 套餐显示服务费，暂停的套餐不可选。目录请求失败保留原选择，不自动换国家或套餐。
- 列表仅使用已保存的地址和凭据读取目录；地址或密钥草稿改动后先保存，再重新读取。请求始终经过本站后端，互相独立且不会跨供应商覆盖目录。目录缺失仍允许保存地址和密钥，解决首次配置必须先有报价的循环限制；实际付款继续执行原供应商与套餐校验。
- `GET /customer/wallet` 查询账户；读取 `data.balance`、`data.held`、`data.prices`、`data.membership`。
- `POST /customer/recharges` 下单；内部 `pro5/pro20/pro50` 对应 `productCode=pro5x/pro20x/pro50x`。顶层传 `country`（默认 US，可改 PH 等两位国家代码）、`cancelRenewal=true`、`session` 和 `card`。
- `session` 保留已保存 Web Session 的字段（含 `user.email`、`account.id`、`accessToken`），过滤本地附加的 RT、ID Token、Session Cookie 等凭据。Session 必须与本次 AT 和邮箱匹配；只有 AT 时先获取临时 AT，或使用全自动开通的第一步登录。
- `card` 传 `cardNumber`、`expMonth`、`expYear`、`cvv`。密钥仅用 `Authorization: Bearer` 请求头。
- 创建时设置稳定的 `Idempotency-Key`；保存 `data.taskId`（兼容 `orderId/localOrderId`）。`reserved` 表示处理中。
- `GET /customer/orders/{taskId}` 查询订单；手动批量查询最多 50 条、并发最多 4 个请求。按查询 ID 关联没有返回 ID 的状态响应。
- 开通状态与取消续费状态独立；`progress.rechargeStatus=success` 表示开通成功，取消续费失败不会阻止后续授权。

每笔订单加密保存供应商、地址、密钥、国家和完整下单参数。修改配置后，已提交订单仍用原快照查询/重试；网络超时、5xx 或无法确认的响应保留「提交待确认」，只允许重试原订单。旧订单默认属于 Tokenseek，订单导入导出也保留供应商快照。

验证采用模拟 HTTP 供应商与隔离浏览器页面，覆盖两家供应商、50x 参数、密钥隔离与重启持久化、事务回滚、原订单幂等重试、并发查询、连续会话自动流程、取消续费失败和低额度等待；不会向供应商提交真实付费订单。
