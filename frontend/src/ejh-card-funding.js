const scale = 1000000n
function units(value) {
  const text = String(value ?? '')
  if (!/^\d{1,12}(\.\d{1,6})?$/.test(text)) return null
  const [whole, fraction = ''] = text.split('.')
  return BigInt(whole) * scale + BigInt(fraction.padEnd(6, '0'))
}
function format(value) {
  return `${value / scale}.${String(value % scale).padStart(6, '0').replace(/0+$/, '').padEnd(2, '0')}`
}
const currencyOK = value => /^[A-Z]{3}$/.test(value || '')
function money(value, currency) {
  const amount = units(value)
  return amount !== null && currencyOK(currency) ? `${format(amount)} ${currency}` : '未记录'
}
const matches = (card, op) => op.card_id === card.id || !!card.remote?.orderNo &&
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

export function cardFundedAmounts(card, operations = []) {
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
  return { amounts: [...totals].sort(([a], [b]) => a.localeCompare(b)).map(([currency, n]) => `${format(n)} ${currency}`),
    increases, missing, settlement: money(initial.settlement, initial.settlementCurrency), missingSettlements: increases }
}

export function cardClosureAmounts(card, trades = []) {
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
  const lines = totals => [...totals].sort(([a], [b]) => a.localeCompare(b)).map(([currency, n]) => `${format(n)} ${currency}`)
  return { transaction: lines(transaction), settlement: lines(settlement), posted, pending, unknown, missingTransaction, missingSettlement }
}
