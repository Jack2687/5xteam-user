import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { financeTotals, caseAmounts, money, rmb, inDateRange } from '../src/nexovcc-finance.js'

const raw=JSON.parse(readFileSync(new URL('../../internal/nexovcc/testdata/finance.json',import.meta.url),'utf8'))
const funds=raw['/api/findLogWebFunds'].list.map(r=>({kind:r.change_type_id,currency:'USD',serial_no:r.serial_no,amount:r.amount}))
const recharges=raw['/api/findLogRecharge'].list.map(r=>({status:String(r.status),currency:'USD',amount:r.amount,received:r.real_amount}))
const group={id:'g1',valid:true,fully_returned:true,serial_no:'fixture-card-1',payment:'92.33',returned:'92.28',difference:'0.05',fees:'0.80',cost:'0.85'}
const c={id:'order1',card_id:'card1',payment_status:'success',match:{group},revenue_r:20}
const data={finance:{at:new Date().toISOString(),funds,recharges},groups:[group],cards:[{card:{id:'card1',serial_no:'fixture-card-1'}}],cases:[c]}
const all=financeTotals(data),card=financeTotals(data,'fixture-card-1')
assert.equal(all.gross,50026)
assert.equal(all.received,49526)
assert.equal(all.rechargeFee,500) // once at aggregate precision, void recharge excluded
assert.equal(card.cost,135) // 0.5 opening + 0.3 transaction + 0.5 reversal + 0.05 card loss
assert.equal(all.cost,635)
assert.equal(card.pending,0)
assert.equal(card.paid,9233)
assert.equal(card.returned,9228)
assert.equal(card.cardLoss,5)
assert.equal(card.unmatched,0)
assert.equal(rmb(card.cost),'9.05') // decimal currency rounding, 1.35 × 6.7
assert.equal(money(card.profit),'10.95')
assert.equal(caseAmounts(c).cost,85)
assert.equal(caseAmounts(c).revenue,2000)
assert.equal(caseAmounts({}).cost,null)
assert.equal(caseAmounts({}).revenue,null)

// Real monthly service debit; yearly charges and repeated months use the ledger amount.
const service=structuredClone(data)
service.finance.funds.push({kind:3004,currency:'USD',serial_no:'fixture-card-1',amount:'-0.20'})
assert.equal(financeTotals(service).unknownFunds,0)
assert.equal(financeTotals(service).service,20)
assert.equal(financeTotals(service).cost,655)
assert.equal(financeTotals(service,'fixture-card-1').cost,155)
assert.equal(financeTotals(service).profit,all.profit-134)
assert.equal(financeTotals(service).otherFees,all.otherFees)
assert.equal(caseAmounts(service.cases[0]).cost,85)
service.finance.funds.push({kind:3004,currency:'USD',serial_no:'fixture-card-1',amount:'-0.35'},{kind:3005,currency:'USD',serial_no:'other-card',amount:'-2.40'})
assert.equal(financeTotals(service).service,295)
assert.equal(financeTotals(service,'fixture-card-1').service,55)
assert.equal(financeTotals(service).cost,all.cost+295)
assert.equal(financeTotals(service).opening,all.opening)
service.finance.funds.push({kind:3999,currency:'USD',amount:'-9.00'})
assert.equal(financeTotals(service).unknownFunds,1)
assert.equal(financeTotals(service).cost,all.cost+295)

const pending=structuredClone(data)
Object.assign(pending.groups[0],{fully_returned:false,returned:'0.00',difference:'92.33',fees:'0.30',cost:'0.30'})
pending.finance.funds=pending.finance.funds.filter(r=>r.kind!==2003)
assert.equal(financeTotals(pending,'fixture-card-1').pending,9233)
assert.equal(financeTotals(pending,'fixture-card-1').cost,80)
const unmatched=structuredClone(data);unmatched.cases=[]
assert.equal(financeTotals(unmatched).unmatched,1)
assert.equal(financeTotals(unmatched).cost,635) // cost doesn't disappear before matching
for (const fixture of [unmatched, {...pending,cases:[]}]) {
 const marked=structuredClone(fixture), before=financeTotals(fixture)
 marked.groups[0].non_pro=true
 const after=financeTotals(marked)
 assert.equal(before.unmatched,1)
 assert.deepEqual(after,{...before,unmatched:0}) // money, pending refunds and costs stay intact
 marked.groups[0].non_pro=false
 assert.deepEqual(financeTotals(marked),before)
}
const unknown=structuredClone(data);unknown.cases[0].revenue_r=null
assert.equal(financeTotals(unknown).unknownRevenue,1)
assert.equal(caseAmounts(unknown.cases[0]).profit,null)
const rebate=structuredClone(data);rebate.finance.funds.push({kind:5004,currency:'USD',serial_no:'fixture-card-1',amount:'0.20'})
assert.equal(financeTotals(rebate).cost,615)
assert.equal(financeTotals(rebate).reversal,30)
assert.equal(inDateRange('2026-10-18T17:00:00Z','2026-10-19','2026-10-19'),true)
assert.equal(inDateRange('2026-10-18T15:59:59Z','2026-10-19','2026-10-19'),false)
console.log('Passed: real ledger cost, 1% recharge, transfers, pending authorizations, returns, rebates, unknown revenue and Beijing date filters')
