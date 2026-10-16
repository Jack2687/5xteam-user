const finite = value => typeof value === 'number' && Number.isFinite(value) && value >= 0
export const moneyLabel = value => finite(value) ? '$' + value.toLocaleString('en-US', {minimumFractionDigits: 2, maximumFractionDigits: 2}) : '—'
export const rateLabel = value => finite(value) ? '$' + value.toLocaleString('en-US', {maximumFractionDigits: 4}) + '/分钟' : '采样中'
export const windowLabel = seconds => seconds === 604800 ? '7 天窗口' : seconds === 18000 ? '5 小时窗口' : '额度窗口'
const states = {sampling:'采样中', missing:'数据不足', idle:'暂无消耗', empty:'暂无可用账号', stale:'数据过期'}
export function supplyLabel(decision, unavailable = false) {
  if (unavailable) return '数据过期'
  if (decision?.money?.state !== 'ready') return states[decision?.money?.state] || '采样中'
  if (!finite(decision?.supply_minutes)) return '数据不足'
  return decision.supply_minutes.toLocaleString('zh-CN', {minimumFractionDigits:1,maximumFractionDigits:1}) + ' 分钟'
}
export function forecastDetails(decision = {}, now = Date.now()) {
  const snapshot = decision.snapshot || {}, config = decision.config || {}, money = decision.money || {}
  const windowMinutes = config.money_window_minutes ?? 3
  const sameWindow = (money.window_minutes || 3) === windowMinutes
  const at = Date.parse(snapshot.at || '')
  const until = Date.parse(money.valid_until || '')
  const complete = ['ready','idle'].includes(money.state)
  const stale = !snapshot.valid || !snapshot.load_valid || !Number.isFinite(at) || now - at > (config.fresh_seconds || 20) * 1000 || at > now + 5000 || (complete && (!Number.isFinite(until) || now >= until))
  const rows = money.rows || []
  const current = (snapshot.accounts || []).filter(a=>a.ready && !a.shared && (!a.expires_at || Date.parse(a.expires_at)>now)).map(a=>a.id).sort((a,b)=>a-b)
  const sampled = rows.map(a=>a.id).sort((a,b)=>a-b)
  const sameAccounts = JSON.stringify(current) === JSON.stringify(sampled)
  const usable = !stale && sameWindow && complete && sameAccounts && finite(money.remaining_usd) && finite(money.rate_usd)
  const label = stale ? '数据过期' : !sameWindow || !sameAccounts ? '采样中' : states[money.state] || '数据不足'
  return {
    stale, windowMinutes, rows:stale ? rows.map(r=>({...r,total_usd:null,used_usd:null,remaining_usd:null,rate_usd:null,reason:'数据过期'})) : !sameWindow ? rows.map(r=>({...r,rate_usd:null,reason:'采样中'})) : rows, coverage:money.coverage || 0,
    remaining:usable ? money.remaining_usd : null,
    rate:usable ? money.rate_usd : null,
    supply:usable ? supplyLabel(decision) : label,
    rateText:usable ? rateLabel(money.rate_usd) : label,
    summary:usable ? `剩余 ${moneyLabel(money.remaining_usd)} · 消耗 ${rateLabel(money.rate_usd)}` : label,
  }
}
export function concurrencyDetails(snapshot = {}, unavailable = false, now = Date.now(), config = {}) {
  const capacityPercent = Number.isInteger(config.capacity_percent) && config.capacity_percent >= 1 && config.capacity_percent <= 100 ? config.capacity_percent : 60
  const accounts = (snapshot.accounts || []).filter(a=>a.ready && !a.shared && (!a.expires_at || Date.parse(a.expires_at)>now))
  const valid = !unavailable && snapshot.valid && snapshot.load_valid && accounts.every(a=>finite(a.used) && finite(a.capacity))
  const used = accounts.reduce((sum,a)=>sum+a.used,0), reportedCapacity = accounts.reduce((sum,a)=>sum+a.capacity,0)
  const capacity = reportedCapacity * capacityPercent / 100
  const triggerCount = valid && Number.isInteger(config.trigger_percent) && config.trigger_percent >= 1 && config.trigger_percent <= 100 ? Math.floor(reportedCapacity * capacityPercent * config.trigger_percent / 10000) : null
  return {used:valid?used:null,capacity:valid?capacity:null,triggerCount,reportedCapacity:valid?reportedCapacity:null,capacityPercent,percent:valid && capacity>0 ? (used*100/capacity).toLocaleString('zh-CN',{maximumFractionDigits:1})+'%' : '—'}
}
