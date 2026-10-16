import { summarizeCardAmountsUSD } from './ejh-card-funding.js'

// Configured estimates only. The supplier does not expose its reversal fee.
// Keep the reversal's card currency and round each fee before summing cents.
function amountUnits(value) {
  const text = String(value ?? '')
  if (!/^\d{1,12}(\.\d{1,6})?$/.test(text)) return null
  const [whole, fraction = ''] = text.split('.')
  return BigInt(whole) * 1000000n + BigInt(fraction.padEnd(6, '0'))
}
function rateUnits(value) {
  const text = String(value ?? '')
  if (!/^\d{1,3}(\.\d{1,2})?$/.test(text)) return null
  const [whole, fraction = ''] = text.split('.')
  const rate = BigInt(whole) * 100n + BigInt(fraction.padEnd(2, '0'))
  return rate <= 10000n ? rate : null
}
const formatCents = cents => `${cents / 100n}.${String(cents % 100n).padStart(2, '0')}`

export function summarizeReversalFees(records = [], percent = 6, confirmedOnly = false, cards = [], operations = [], preciseUSD = false) {
  const rate = rateUnits(percent), unique = new Map(), totals = new Map(), conversions = []
  let count = 0, missing = 0, pending = 0
  for (const record of records) {
    const trade = record.trade || {}
    if (String(trade.tradeType) !== '2') continue
    // Distinct partial reversals may share an auth code; deduplicate by trade.
    const card = record.card_id || trade.orderNo || ''
    const key = trade.tradeNo ? `${card}:trade:${trade.tradeNo}` : record.id ? `${card}:id:${record.id}` : record
    const previous = unique.get(key)
    if (!previous || (Date.parse(record.synced_at) || 0) >= (Date.parse(previous.synced_at) || 0)) unique.set(key, record)
  }
  for (const record of unique.values()) {
    const trade = record.trade
    if (!['1', '2'].includes(String(trade.tradeStatus)) || confirmedOnly && !record.confirmed) { pending++; continue }
    const base = amountUnits(trade.cardAmount), currency = trade.cardCurrency
    if (rate === null || base === null || !/^[A-Z]{3}$/.test(currency || '')) { missing++; continue }
    const cents = (base * rate + 50000000n) / 100000000n
    totals.set(currency, (totals.get(currency) || 0n) + cents)
    conversions.push({ card_id: record.card_id, order_no: trade.orderNo, amount: formatCents(cents), currency })
    count++
  }
  const rows = [...totals].sort(([a], [b]) => a.localeCompare(b)).map(([currency, cents]) => ({ currency, amount: formatCents(cents) }))
  return { rows, count, missing, pending, percent: rate === null ? null : String(Number(rate) / 100), usd: summarizeCardAmountsUSD(conversions, cards, operations, preciseUSD) }
}
