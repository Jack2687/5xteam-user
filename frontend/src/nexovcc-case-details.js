import { caseAmounts, cents, feeKinds } from './nexovcc-finance.js'

const present = v => v !== null && v !== undefined && String(v).trim() !== '' && Number.isFinite(Number(v))
const validTime = v => v && !String(v).startsWith('0001') && Number.isFinite(Date.parse(v))
export function caseTime(row) {
 return [row.match?.group?.returned_at, row.requested_at, row.opened_at, row.created_at].find(validTime) || ''
}
export function sortReturnCases(rows) {
 return [...rows].sort((a,b) => (Date.parse(caseTime(b))||0) - (Date.parse(caseTime(a))||0) || String(b.id).localeCompare(String(a.id)))
}

export function caseDetails(row, finance = {}) {
 const amounts = caseAmounts(row), group = row.match?.group
 const ids = new Set(group?.trade_ids || [])
 const trades = (finance.trades || []).filter(t => t.serial_no === group?.serial_no && ids.has(t.id))
 const funds = (finance.funds || []).filter(f => f.serial_no === group?.serial_no && ids.has(f.link) && feeKinds.includes(f.kind) && ![3003,5001].includes(f.kind))
 const feeTotal = funds.reduce((s,f) => s-cents(f.amount),0)
 const feesReady = !!group?.valid && validTime(finance.at) && feeTotal === amounts.fees && funds.every(f => f.currency === 'USD' && present(f.amount))
 const fee = kinds => feesReady ? funds.filter(f=>kinds.includes(f.kind)).reduce((s,f)=>s-cents(f.amount),0) : null
 const originalPayments = trades.some(t=>t.status==='COMPLETE') ? trades.filter(t=>t.status==='COMPLETE') : trades.filter(t=>t.status==='PENDING')
 const originalReturns = trades.filter(t=>['REVERSED','REFUNDED'].includes(t.status))
 const original = rows => {
  const byCurrency = new Map()
  for(const t of rows){
   if(!t.merchant_currency || !present(t.merchant_amount))continue
   byCurrency.set(t.merchant_currency,(byCurrency.get(t.merchant_currency)||0)+cents(t.merchant_amount))
  }
  return [...byCurrency].map(([currency,amount])=>({currency,amount}))
 }
 const merchantPaid=original(originalPayments),merchantReturned=original(originalReturns)
 // Older groups can retain the original payment without raw records loaded.
 // Do not invent the merchant's returned amount by converting from USD.
 if(!merchantPaid.length && group?.valid && group.merchant_currency && present(group.merchant_amount)) merchantPaid.push({currency:group.merchant_currency,amount:cents(group.merchant_amount)})
 return {...amounts,merchantPaid,merchantReturned,trades,funds,feesReady,
  reversal:fee([2003,5004]),transaction:fee([2007,5008]),refundFee:fee([8888,5003]),otherFees:fee(feeKinds.filter(k=>![3003,5001,2003,5004,2007,5008,8888,5003].includes(k))),
  cardLoss:group?.valid ? group.fully_returned ? amounts.difference : 0 : null,
  settlement:group?.valid ? group.settled && !group.fully_returned ? amounts.difference : 0 : null,
  pending:group?.valid ? amounts.closed ? 0 : Math.max(0,amounts.difference) : null,
  returnedAt:validTime(group?.returned_at) ? group.returned_at : '',
  provisional:!amounts.closed,
  manualUnconfirmed:row.state==='returned' && (!group?.valid || !group.fully_returned),
 }
}
