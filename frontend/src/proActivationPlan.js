import { proPlanName } from './gptpay.js'

// A later default change, failed retry, or points purchase must not rewrite
// the subscription recorded by the successful activation order.
export function activationPlan(account) {
  const orders = (account?.financial_cases || []).filter(row => row.payment_status === 'success' && row.plan !== 'codex_point')
  orders.sort((a, b) => (Date.parse(b.created_at) || 0) - (Date.parse(a.created_at) || 0) || String(b.id || '').localeCompare(String(a.id || '')))
  const order = orders[0]
  return {
    label: order ? (proPlanName(order.plan) || '套餐未记录') : '未开通',
    tone: order ? 'success' : 'pending',
    title: order ? `开通订单：${order.id}；显示下单时保存的套餐` : '暂无成功的开通订单',
  }
}
