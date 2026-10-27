import { isClosedCard } from './card-status.js'

export const returnStates = { no_return:'只付款 · 无需回款', unopened: '未开通', opened: '已开通', waiting: '已申请待回款', returned: '已回款' }
export const tradeKinds = { '1': '授权', '2': '授权撤销', '3': '授权失败', '4': '清算', '5': '退款', '16': '清算退款', '17': '销卡退回', '19': '销卡后清算' }
export const operationStates = { submitting: '已提交待确认', processing: '销卡处理中', unknown: '结果未知，需核实', import_pending: '已开卡待导入', completed: '已完成', resolved: '人工核实完成', failed: '未执行/失败' }
export const returnTypes = new Set(['2', '5', '16'])
export function newestCards(cards) {
  const created = card => { const value = Date.parse(card.local?.created_at || card.created_at || ''); return Number.isFinite(value) ? value : 0 }
  return [...cards].sort((a, b) => created(b) - created(a) || String(a.id).localeCompare(String(b.id)))
}
export function cancelCardReason(card, operations = [], cases = []) {
  if (card.issuer_linked === false) return '尚未绑定卡台订单'
  if (String(card.remote?.status) === '2') return '已销卡'
  if (String(card.remote?.status) === '8') return '销卡处理中'
  if (card.local?.issuer_cancel_status) return '已有销卡申请，正在等待卡台处理结果'
  if (card.error || !['1','3','4','5','6','7'].includes(String(card.remote?.status))) return '卡片资料或状态待核实'
  if (cases.some(row => cardHasCase(card,row) && ['submitting','processing','submission_unknown','manual_review'].includes(row.payment_status))) return '付款结果待核实，请先核实原订单'
  if (cases.some(row => cardHasCase(card,row) && !(row.pro_dead&&row.payment_status==='success') && !(row.payment_only&&row.requested_at) && !['returned','unopened'].includes(row.original_state||row.state))) return '有关联账号已开通或已申请待回款'
  if (operations.some(op => (op.card_id === card.id || op.order_no && op.order_no === card.remote?.orderNo) && ['submitting','processing','unknown','import_pending'].includes(op.status))) return '有待核实操作'
  return ''
}
// A rejected/ambiguous mutation stops the batch. Never generate a replacement
// request ID for an uncertain item or silently replay a partially finished batch.
export async function runEJHBatch(items, progress, submit, shouldStop = () => false) {
  progress.total = items.length
  progress.processing = 0
  for (let i = 0; i < items.length; i++) {
    if (shouldStop() || progress.stopRequested) { progress.stopped = true; break }
    const item = items[i]
    progress.phase = `正在处理 ${i + 1} / ${items.length}：${item.label}`
    let result
    try { result = await submit(item) }
    catch (error) { result = {status:'unknown',error:error.message} }
    const status = ['completed','resolved'].includes(result.status) ? 'success' : result.status === 'failed' ? 'failed' : item.method === 'cancel' && result.status === 'processing' ? 'processing' : 'unknown'
    progress[status]++
    progress.done++
    progress.results.push({label:item.label,request_id:item.request_id,status,error:result.error || ''})
    if (status !== 'success' && status !== 'processing') { progress.stopped = true; break }
  }
  progress.phase = progress.stopped ? '后续操作已停止' : '全部处理完成'
}

// Follow saved operation results after the submit request has returned. Never
// resubmit a cancellation or interpret an arbitrary error code as acceptance.
export function refreshCancellationProgress(progress, operations = []) {
  if (!progress?.cancellation) return
  const byID = new Map(operations.map(op => [op.id, op]))
  for (const result of progress.results) {
    const op = byID.get(result.request_id)
    if (!op || op.method !== 'cancel') continue
    result.status = ['completed','resolved'].includes(op.status) ? 'success' : op.status === 'failed' ? 'failed' : op.status === 'processing' ? 'processing' : 'unknown'
    result.error = op.error || ''
    result.check_error = op.check_error || ''
  }
  for (const state of ['success','failed','processing','unknown']) progress[state] = progress.results.filter(r => r.status === state).length
  progress.waitingForIssuer = progress.processing > 0
  const pending = progress.processing || progress.unknown || (!progress.finished && !progress.stopped && progress.done < progress.total)
  if (pending) progress.cancellationFinishedAt = null
  else progress.cancellationFinishedAt ??= Date.now()
  progress.elapsed = Math.floor(((progress.cancellationFinishedAt ?? Date.now()) - progress.startedAt) / 1000)
  progress.phase = `销卡成功 ${progress.success} 张 · 处理中 ${progress.processing} 张` + (progress.failed ? ` · 失败 ${progress.failed} 张` : '') + (progress.unknown ? ` · 待确认 ${progress.unknown} 张` : '') + (progress.total > progress.done ? ` · 未提交 ${progress.total-progress.done} 张` : '')
  if (progress.processing) progress.phase += '。卡台处理中，完成后自动更新，无需重复提交。'
  if (progress.unknown) progress.phase += '。部分结果暂未确认，后台继续查询，请勿重复提交。'
  if (progress.results.some(r=>r.check_error)) progress.phase += ' 状态查询暂时失败，后台将重试。'
}
export function returnLabel(row) { return returnStates[row?.state] || '未开通' }
export function caseSummary(row) { return [row.email, row.plan || '—', returnLabel(row), row.deleted ? '账号已删除，历史保留' : ''].filter(Boolean).join(' · ') }
export const returnStatusLabels = { ...returnStates, unverified: '待核实', unmatched: '回款待关联', unknown: '状态未知' }
export function returnStatus(state) { return Object.hasOwn(returnStatusLabels, state) ? state : 'unknown' }
export function cardHasCase(card, row) { return row.card_id === card.id || row.ejh_card_id === card.id }
export const cardStates = { '1': '已激活', '2': '已销卡', '3': '活跃', '4': '未激活', '5': '卡台状态受限', '6': '销卡失败', '7': '非活跃', '8': '销卡处理中', cancel_pending: '销卡结果确认中', review: '资料待核实', unknown: '状态待核实', unlinked: '手动添加 · 未绑定卡台' }
export function cardLifecycle(card) {
  return ['2','8'].includes(String(card.remote?.status ?? card.local?.issuer_status ?? '')) || !!card.local?.issuer_cancel_status ? 'closed' : card.normal_pending ? 'normal_pending' : 'normal'
}
export function cardState(card) {
  if (card.issuer_linked === false) return 'unlinked'
  if (String(card.remote?.status) === '2') return '2'
  if (String(card.remote?.status) === '8' || card.local?.issuer_cancel_status === 'processing') return '8'
  if (['submitting','unknown'].includes(card.local?.issuer_cancel_status)) return 'cancel_pending'
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
  const included = (cards || []).filter(card => !isClosedCard(card))
  const groups = new Map()
  let known = 0, missing = 0, failed = 0
  for (const card of included) {
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
  return { totals, known, missing, failed, total: included.length }
}
