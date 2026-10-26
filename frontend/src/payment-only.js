import { accountAmounts } from './ejh-account-amounts.js'
import { summarizeCardAmountsUSD } from './ejh-card-funding.js'
import { summarizeClearingCosts, uniqueTrades } from './ejh-clearing.js'

const cents = v => /^\d{1,12}(\.\d{1,6})?$/.test(String(v ?? '')) ? Math.round(Number(v) * 100) : null
export const paymentMoney = v => v == null ? '—' : (v / 100).toFixed(2)
export function paymentPolicy(data, id) { return (data.payment_policies || []).find(p => p.card_id === id) }
export function isPaymentOnly(data, id) { return paymentPolicy(data, id)?.mode === 'payment_only' }

// Project the accounting label only in card dashboards. Original states remain
// available to operational guards; no issuer/Pro workflow state is rewritten.
export function applyPaymentPolicies(data) {
 return {...data, cards:(data.cards||[]).map(c=>c.card?{...c,card:{...c.card,payment_policy:paymentPolicy(data,c.card.id)}}:{...c,payment_policy:paymentPolicy(data,c.id)}), cases:(data.cases || []).map(c => {
  const state = c.original_state || c.state
  const enabled = isPaymentOnly(data, c.ejh_card_id || c.card_id)
  return {...c, original_state:state, payment_only:enabled, state:enabled && state !== 'unopened' ? 'no_return' : state}
 })}
}

export function paymentOnlySummary(data, provider, serial = '') {
 const rows = []
 for (const policy of data.payment_policies || []) {
  if (policy.mode !== 'payment_only') continue
  const wrapper = (data.cards || []).find(c => (c.card?.id || c.id) === policy.card_id)
  const card = wrapper?.card || wrapper
  if (serial && card?.serial_no !== serial) continue
  const cases = (data.cases || []).filter(c => (c.ejh_card_id || c.card_id) === policy.card_id)
  let paid = 0, returned = 0, clearing = 0, pending = 0, missing = false, returnMissing = false, clearingMissing = false, estimated = false, hasPayment = false
  const entries = []
  if (provider === 'ejh') {
   const trades = uniqueTrades((data.trades || []).filter(t => t.card_id === policy.card_id))
   // Authorizations and their clearing are one payment. Separate refund trade
   // IDs are separate receipts, even when they share an authorization code.
   const payments = new Map()
   for (const t of trades.filter(t => ['1','4','19'].includes(t.trade?.tradeType) && ['1','2'].includes(t.trade?.tradeStatus))) {
    const key = t.trade.authCode || t.trade.tradeNo || t.id, old = payments.get(key)
    if (!old || t.trade.tradeType === '1') payments.set(key, t)
   }
   const entry = record => {
    const t = record.trade || {}
    const bill = cents(t.billAmount), amount = cents(t.cardAmount)
    // A zero bill on an authorization is not a zero payment.
    if (t.billCurrency === 'USD' && bill !== null && (bill > 0 || amount === 0)) return {card_id:policy.card_id, amount:t.billAmount, currency:'USD'}
    return {card_id:policy.card_id, amount:t.cardAmount, currency:t.cardCurrency}
   }
   let original = [...payments.values()].map(record => {
    const t = record.trade
    const posted = trades.find(r => ['4','19'].includes(r.trade?.tradeType) && r.trade.tradeStatus === '2' && t.authCode && r.trade.authCode === t.authCode && r.trade.cardCurrency === t.cardCurrency && cents(r.trade.cardAmount) === cents(t.cardAmount) && r.trade.billCurrency === 'USD' && cents(r.trade.billAmount) > 0)
    return entry(posted || record)
   })
   let refunds = trades.filter(t => ['2','5','16'].includes(t.trade?.tradeType) && (t.trade.tradeStatus === '2' || t.trade.tradeType === '2' && t.trade.tradeStatus === '1')).map(entry)
   const hasRawRefunds = refunds.length > 0
   if (!original.length) {
    const seen = new Set()
    for (const c of cases) {
     if ((c.original_state || c.state) === 'unopened') continue
     const a = accountAmounts(c, data.trades || []), key = c.charge?.trade_no || c.charge?.id || c.id
     if (seen.has(key)) continue
     seen.add(key)
     if (a.paid === null) { missing = true; continue }
     original.push({card_id:policy.card_id, amount:a.paid, currency:a.currency})
     if (!hasRawRefunds) {
      returnMissing ||= a.invalid
      refunds.push(...a.returned.map(r => ({...r, card_id:policy.card_id})))
     }
    }
   }
   const convert = r => summarizeCardAmountsUSD(r, data.cards || [], data.operations || [], true)
   hasPayment = original.some(r => Number(r.amount) > 0)
   const p = convert(original), r = convert(refunds)
   paid = cents(p.amount) || 0; returned = cents(r.amount) || 0
   missing ||= !!p.missing || !original.length
   returnMissing ||= !!r.missing
   estimated = !!p.estimated || !!r.estimated
   const cleared = summarizeClearingCosts(trades, data.cards || [], data.operations || [])
   clearing = Math.max(0, Math.round(Number(cleared.usd.amount || 0) * 100))
   clearingMissing ||= !!cleared.missing
   for (const e of cleared.entries) entries.push(e)
  } else {
   const groups = (data.groups || []).filter(g => g.serial_no === card?.serial_no)
   const valid = groups.filter(g => g.valid)
   missing = groups.some(g => !g.valid) || !groups.length
   returnMissing = groups.some(g => !g.valid || cents(g.returned) === null)
   for (const g of valid) {
    if (cents(g.payment) === null || cents(g.returned) === null) { missing = true; continue }
    hasPayment ||= cents(g.payment) > 0
    paid += cents(g.payment); returned += cents(g.returned)
    const diff = Math.max(0, cents(g.payment) - cents(g.returned))
    if (g.settled || g.fully_returned) clearing += diff
    else pending += diff
   }
  }
  let manual = false
  if ((missing || !hasPayment) && cents(policy.amount_usd) !== null) { paid = cents(policy.amount_usd); missing = false; manual = true }
  // Missing cards/history must never silently become a zero expense.
  if (!card) missing = true
  missing ||= returnMissing || clearingMissing
  const amount = Math.max(0, paid - returned)
  const costExcluded = Math.min(amount, clearing)
  rows.push({card_id:policy.card_id, serial:card?.serial_no, last4:card?.last4 || card?.remote?.cardNo || card?.local?.last4 || '',
   accounts:[...new Set(cases.map(c => c.email))], amount, paid, returned, costExcluded, pending, missing, estimated, manual,
   note:policy.note, at:policy.updated_at, entries})
 }
 return {rows, amount:rows.reduce((s,r) => s + (r.missing ? 0 : r.amount), 0), costExcluded:rows.reduce((s,r) => s + (r.missing ? 0 : r.costExcluded), 0),
  pending:rows.reduce((s,r) => s + (r.missing ? 0 : r.pending), 0), missing:rows.filter(r => r.missing).length, estimated:rows.some(r => r.estimated)}
}
