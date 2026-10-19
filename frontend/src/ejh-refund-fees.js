// These amounts are explicitly recorded. Never manufacture a fee from a refund
// principal, a percentage, or the unexplained reconciliation balance.
export function refundFeeEntries(records = []) {
 const unique = new Map()
 for (const row of records) {
  if (!row?.id) continue
  const old = unique.get(row.id)
  if (!old || (Number(row.revision) || 0) > (Number(old.revision) || 0)) unique.set(row.id, row)
 }
 return [...unique.values()].filter(row => !row.voided)
}

export function refundFeeSummary(records = []) {
 const entries = refundFeeEntries(records)
 let total = 0n, missing = 0
 for (const row of entries) {
  if (!/^\d{1,12}(\.\d{1,2})?$/.test(String(row.amount ?? ''))) { missing++; continue }
  const [whole, part = ''] = String(row.amount).split('.')
  const value = BigInt(whole) * 100n + BigInt(part.padEnd(2, '0'))
  if (value <= 0n) { missing++; continue }
  total += value
 }
 return { entries, count: entries.length, missing, estimated: entries.some(row => row.source === 'balance_reconciliation'),
  amount: `${total / 100n}.${String(total % 100n).padStart(2, '0')}` }
}
