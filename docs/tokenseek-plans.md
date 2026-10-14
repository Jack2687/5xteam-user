# Tokenseek 国家与套餐配置

按 [Tokenseek API 文档](https://gptpay.tokenseek.app/docs)（2026-10-14）适配自带卡充值接口。入口为 **Pro 管理 → Pro 全自动配置 → GPTPay · Tokenseek**。

- 普通订阅：Go、Plus、Pro 5x、Pro 20x、Pro 50x。
- Plus 升级：Pro 5x、Pro 20x、Pro 50x 升级；仅当前 Plus 可用。
- Codex 点数：已有有效订阅的账号可购买，250～250000，且为 250 的倍数。
- 开通国家：菲律宾 PH、埃及 EG、智利 CL、美国 US、日本 JP。

保存供应商地址和 API Key 后自动读取账户报价，也可点击“刷新国家与套餐”。报价未开放的套餐禁用；国家按当前套餐的 availableCountries 筛选。更换地址或密钥后先保存再刷新，不会把已保存密钥发给尚未保存的地址。读取失败时显示说明和文档选项，便于修复配置；最终可购买项由供应商校验。

国家为显式选择；“使用套餐默认国家”按钮优先采用当前套餐报价中的 defaultCountry。未配置时 Pro50 回退 CL，其他套餐回退 PH。已有国家配置保留。Credits 为供应商服务费，不代表银行卡实际扣款金额。

手动开通和全自动开通窗口显示国家及点数数量，并检查确认后配置是否变化。普通订单不发送点数数量。已明确为 Free 的 Session 在点数购买前拒绝；Plus 升级拒绝已知非 Plus 订阅；未知订阅状态交给供应商校验。

账户查询、订单创建、订单状态查询使用 view=extended。订单保存国家、套餐、点数和加密请求快照，重试始终使用原幂等键和原参数。旧 Tokenseek 订单原先没有发送 country，重试仍省略该字段；没有可靠国家记录的历史订单显示“未记录”。点数订单的 not_applicable 取消续费状态显示为“不适用”。账号导入导出保留这些字段。

仅使用现有自带卡充值 API；平台出资和成品账号购买不属于此次接入。CMSNav 继续沿用自己的目录、认证和下单结构。

验证：Go 供应商/HTTP/存储回归，以及 frontend/scripts/check-gptpay-tokenseek-browser.js 的隔离浏览器用例。所有订单测试使用本地模拟供应商与测试数据，不提交真实付款。
