import { isClosedCard } from './card-status.js'

const scale = 1000000n
function units(value) {
  const text = String(value ?? '')
  if (!/^\d{1,12}(\.\d{1,6})?$/.test(text)) return null
  const [whole, fraction = ''] = text.split('.')
  return BigInt(whole) * scale + BigInt(fraction.padEnd(6, '0'))
}
function format(value) {
  const sign = value < 0n ? '-' : ''
  const absolute = value < 0n ? -value : value
  return `${sign}${absolute / scale}.${String(absolute % scale).padStart(6, '0').replace(/0+$/, '').padEnd(2, '0')}`
}
const lines = totals => [...totals].sort(([a], [b]) => a.localeCompare(b)).map(([currency, n]) => `${format(n)} ${currency}`)
const currencyOK = value => /^[A-Z]{3}$/.test(value || '')
function money(value, currency) {
  const amount = units(value)
  return amount !== null && currencyOK(currency) ? `${format(amount)} ${currency}` : '未记录'
}
const matches = (card, op) => !!card.id && op.card_id === card.id || !!card.remote?.orderNo &&
  (op.order_no === card.remote.orderNo || op.result?.orderNo === card.remote.orderNo)

function openingOperation(card, operations) {
  return operations.filter(op => op.method === 'open' &&
    ['completed', 'resolved', 'import_pending'].includes(op.status) && matches(card, op))
    .sort((a, b) => (Date.parse(a.created_at) || Infinity) - (Date.parse(b.created_at) || Infinity) || String(a.id).localeCompare(String(b.id)))[0]
}
function openingValues(card, operations) {
  const opening = openingOperation(card, operations)
  // Only the original opening response/request is historical evidence. A current
  // card snapshot can contain later top-ups, so never use it as the initial limit.
  const result = opening?.result || {}, request = opening?.request || {}
  const issued = units(result.cardAmount) !== null && currencyOK(result.cardCurrency || request.cardCurrency)
  return { amount: issued ? result.cardAmount : request.cardAmount, currency: issued ? result.cardCurrency || request.cardCurrency : request.cardCurrency,
    settlement: result.billAmount, settlementCurrency: result.billCurrency || request.billCurrency }
}
export function cardOpeningAmounts(card, operations = []) {
  const value = openingValues(card, operations)
  return {
    amount: money(value.amount, value.currency), settlement: money(value.settlement, value.settlementCurrency),
  }
}

function fundingValues(card, operations) {
  const initial = openingValues(card, operations)
  const totals = new Map(), seen = new Set()
  let increases = 0, missing = 0
  const add = (amount, currency) => {
    const n = units(amount)
    if (n === null || !currencyOK(currency)) { missing++; return }
    totals.set(currency, (totals.get(currency) || 0n) + n)
  }
  add(initial.amount, initial.currency)
  for (const op of operations) {
    if (op.method !== 'increase' || !['completed', 'resolved'].includes(op.status) || !matches(card, op) || seen.has(op.id)) continue
    seen.add(op.id); increases++; add(op.amount, op.currency)
  }
  // card.change.info billAmount is the post-increase BALANCE, not the USD
  // settlement of this increase. Never add it to historical funding costs.
  return { initial, totals, increases, missing }
}
function fundedAmounts({ initial, totals, increases, missing }) {
  return { amounts: lines(totals), increases, missing,
    settlement: money(initial.settlement, initial.settlementCurrency), missingSettlements: increases }
}
export function cardFundedAmounts(card, operations = []) {
  return fundedAmounts(fundingValues(card, operations))
}

function closureValues(card, trades) {
  const unique = new Map()
  for (const record of trades) {
    if (record.card_id !== card.id || record.trade?.tradeType !== '17') continue
    const key = record.trade.tradeNo || record.id
    const previous = unique.get(key)
    if (!previous || (Date.parse(record.synced_at) || 0) > (Date.parse(previous.synced_at) || 0) ||
      record.synced_at === previous.synced_at && record.trade.tradeStatus === '2') unique.set(key, record)
  }
  const transaction = new Map(), settlement = new Map()
  let pending = 0, unknown = 0, posted = 0, missingTransaction = 0, missingSettlement = 0
  const add = (totals, value, currency) => {
    const n = units(value)
    if (n === null || !currencyOK(currency)) return false
    totals.set(currency, (totals.get(currency) || 0n) + n)
    return true
  }
  for (const { trade } of unique.values()) {
    if (trade.tradeStatus === '1') { pending++; continue }
    if (trade.tradeStatus !== '2') { unknown++; continue }
    posted++
    if (!add(transaction, trade.tradeAmount, trade.tradeCurrency)) missingTransaction++
    if (!add(settlement, trade.billAmount, trade.billCurrency)) missingSettlement++
  }
  return { transaction, settlement, posted, pending, unknown, missingTransaction, missingSettlement }
}
function closureAmounts(value) {
  return { ...value, transaction: lines(value.transaction), settlement: lines(value.settlement) }
}
export function cardClosureAmounts(card, trades = []) {
  return closureAmounts(closureValues(card, trades))
}

function differenceAmounts(funding, closure, kind) {
  const unavailable = reason => ({ amounts: [], reason })
  // No closure record is unknown, not a zero refund. Wait for all known closure
  // records to post before presenting a final funding-minus-refund difference.
  if (closure.pending) return unavailable('销卡退回尚有待入账记录')
  if (closure.unknown) return unavailable('销卡退回状态待核实')
  if (!closure.posted) return unavailable('暂无销卡退回记录，差额暂不计算')
  let funded = funding.totals
  if (kind === 'transaction') {
    if (funding.missing) return unavailable('初始或追加的交易金额缺失')
    if (closure.missingTransaction) return unavailable('销卡退回的交易金额缺失')
  } else {
    if (funding.increases) return unavailable('成功追加的结算金额未记录')
    const initial = funding.initial, amount = units(initial.settlement)
    if (amount === null || !currencyOK(initial.settlementCurrency)) return unavailable('初始开卡的结算金额缺失')
    if (closure.missingSettlement) return unavailable('销卡退回的结算金额缺失')
    funded = new Map([[initial.settlementCurrency, amount]])
  }
  const returned = closure[kind]
  if (funded.size !== returned.size || [...funded.keys()].some(currency => !returned.has(currency))) {
    return unavailable('累计额度与销卡退回的币种不一致')
  }
  return { amounts: lines(new Map([...funded].map(([currency, amount]) => [currency, amount - returned.get(currency)]))), reason: '' }
}

export function cardFundingSummary(card, operations = [], trades = []) {
  const funding = fundingValues(card, operations), closure = closureValues(card, trades)
  return {
    opening: { amount: money(funding.initial.amount, funding.initial.currency), settlement: money(funding.initial.settlement, funding.initial.settlementCurrency) },
    funded: fundedAmounts(funding), closure: closureAmounts(closure),
    difference: { transaction: differenceAmounts(funding, closure, 'transaction'), settlement: differenceAmounts(funding, closure, 'settlement') },
  }
}

// Convert each amount using its own card's original opening settlement ratio.
// Historical estimates are independent of current balance, availability or closure.
export function summarizeCardAmountsUSD(entries = [], cards = [], operations = [], precise = false) {
  const byID = new Map(cards.filter(card => card.id).map(card => [card.id, card]))
  const byOrder = new Map(cards.filter(card => card.remote?.orderNo).map(card => [card.remote.orderNo, card]))
  const rates = new Map()
  let total = 0n, known = 0, estimated = 0
  for (const entry of entries) {
    const text = String(entry.amount ?? ''), negative = text.startsWith('-')
    const amount = units(negative ? text.slice(1) : text), currency = entry.currency
    if (amount === null || !currencyOK(currency)) continue
    let usd = amount
    if (currency !== 'USD' && amount !== 0n) {
      if (!entry.card_id && !entry.order_no) continue
      const card = byID.get(entry.card_id) || byOrder.get(entry.order_no) || { id: entry.card_id || '', remote: { orderNo: entry.order_no || '' } }
      const key = card.id ? `id:${card.id}` : `order:${card.remote.orderNo}`
      if (!rates.has(key)) rates.set(key, openingValues(card, operations))
      const initial = rates.get(key)
      const base = units(initial.amount), quote = units(initial.settlement)
      if (initial.currency !== currency || initial.settlementCurrency !== 'USD' || base === null || base <= 0n || quote === null || quote <= 0n) continue
      usd = amount * quote / base
      estimated++
    }
    total += negative ? -usd : usd
    known++
  }
  // Keep six decimal places while summing, then round the displayed total once.
  const absolute = total < 0n ? -total : total, cents = (absolute + 5000n) / 10000n
  const amount = `${total < 0n && cents !== 0n ? '-' : ''}${cents / 100n}.${String(cents % 100n).padStart(2, '0')}`
  return { amount: known ? (precise ? format(total) : amount) : null, known, estimated, missing: entries.length - known, total: entries.length }
}

// This is not a live FX quote or the supplier's refundable USD amount.
export function summarizeCardBalancesUSD(cards = [], operations = []) {
  const included = cards.filter(card => !isClosedCard(card))
  return summarizeCardAmountsUSD(included.map(card => ({
    card_id: card.id, order_no: card.remote?.orderNo,
    amount: card.balance_at ? card.balance : null, currency: card.remote?.cardCurrency,
  })), included, operations)
}
