export const returnStates = { unopened: '未开通', opened: '已开通', waiting: '已申请待回款', returned: '已回款' }
export const tradeKinds = { '1': '授权', '2': '授权撤销', '3': '授权失败', '4': '清算', '5': '退款', '16': '清算退款', '17': '销卡退回', '19': '销卡后清算' }
export const operationStates = { submitting: '已提交待确认', unknown: '结果未知，需核实', import_pending: '已开卡待导入', completed: '已完成', resolved: '人工核实完成', failed: '未执行/失败' }
export const returnTypes = new Set(['2', '5', '16'])
export function returnLabel(row) { return returnStates[row?.state] || '未开通' }
export function caseSummary(row) { return [row.email, row.plan || '—', returnLabel(row), row.deleted ? '账号已删除，历史保留' : ''].filter(Boolean).join(' · ') }
export const returnStatusLabels = { ...returnStates, unverified: '待核实', unmatched: '回款待关联', unknown: '状态未知' }
export function returnStatus(state) { return Object.hasOwn(returnStatusLabels, state) ? state : 'unknown' }
export function cardHasCase(card, row) { return row.card_id === card.id || row.ejh_card_id === card.id }
export const cardStates = { '1': '已激活', '2': '已销卡', '3': '活跃', '4': '未激活', '5': '已冻结', '6': '销卡失败', '7': '非活跃', '8': '销卡处理中', review: '资料待核实', unknown: '状态待核实', unlinked: '手动添加 · 未绑定卡台' }
export function cardState(card) {
  if (card.issuer_linked === false) return 'unlinked'
  if (card.error) return 'review'
  if (String(card.remote?.isLocked) === '1') return '5'
  const status = String(card.remote?.status || '')
  return Object.hasOwn(cardStates, status) ? status : 'unknown'
}
export function cardMatchesFilters(card, cases, state = '', returnState = '') {
  if (state && cardState(card) !== state) return false
  if (!returnState) return true
  const linked = cases.filter(row => cardHasCase(card, row))
  return returnState === 'none' ? linked.length === 0 : linked.some(row => row.state === returnState)
}
export function cardMatchesSearch(card, cases, search, targetID = '') {
  if (targetID && card.id !== targetID) return false
  const query = String(search || '').trim().toLowerCase()
  if (!query) return true
  return JSON.stringify(card).toLowerCase().includes(query) || cases.some(row => cardHasCase(card, row) && String(row.email || '').toLowerCase().includes(query))
}
const cardViewKey = 'space-ejh-card-view'
export function readCardView(storage) {
  try { return storage?.getItem(cardViewKey) === 'list' ? 'list' : 'cards' } catch { return 'cards' }
}
export function saveCardView(storage, value) {
  try { storage?.setItem(cardViewKey, value === 'list' ? 'list' : 'cards') } catch { /* Private mode/storage disabled: keep in-memory selection. */ }
}
export function ejhCardEntry(account) {
  const cardID = account?.activation_card?.ejh_card_id
  return cardID && account?.email ? { email: account.email, cardID } : null
}

// Balances are supplier snapshots, not live estimates. Never use binary floats
// or mix currencies, and never treat missing balances as zero.
export function summarizeCardBalances(cards) {
  const groups = new Map()
  let known = 0, missing = 0, failed = 0
  for (const card of cards || []) {
    if (card.balance_error) failed++
    const currency = card.remote?.cardCurrency || ''
    const value = String(card.balance ?? '')
    if (!card.balance_at || !/^[A-Z]{3}$/.test(currency) || !/^-?\d{1,12}(\.\d{1,6})?$/.test(value)) { missing++; continue }
    const negative = value.startsWith('-')
    const [whole, fraction = ''] = value.replace(/^-/, '').split('.')
    const units = (BigInt(whole) * 1000000n + BigInt(fraction.padEnd(6, '0'))) * (negative ? -1n : 1n)
    const group = groups.get(currency) || { units: 0n, count: 0 }
    group.units += units; group.count++; groups.set(currency, group); known++
  }
  const totals = [...groups].sort(([a], [b]) => a.localeCompare(b)).map(([currency, group]) => {
    const units = group.units < 0n ? -group.units : group.units
    const fraction = String(units % 1000000n).padStart(6, '0').replace(/0+$/, '').padEnd(2, '0')
    return { currency, count: group.count, amount: (group.units < 0n ? '-' : '') + String(units / 1000000n) + '.' + fraction }
  })
  return { totals, known, missing, failed, total: (cards || []).length }
}
