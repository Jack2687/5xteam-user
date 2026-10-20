import assert from 'node:assert/strict'
import {financeTotals,ledgerFunds} from '../src/nexovcc-finance.js'
import {reconciliation,cardFunding} from '../src/nexovcc-reconciliation.js'
const now=new Date().toISOString()
const original={finance:{at:now,recharges:[{amount:'100',received:'99',status:'1',currency:'USD'}],funds:[{id:'funding',kind:3001,amount:'-10.00',currency:'USD',serial_no:'c'},{id:'fee',kind:2007,amount:'-0.30',currency:'USD',serial_no:'c'}],trades:[{id:'t',serial_no:'c',last4:'1234'}]},balance:{amount:'89.00',at:now},cards:[{card:{id:'c',serial_no:'c',balance:'7.40',balance_at:now,status:'1'}}],groups:[{id:'g',serial_no:'c',valid:false,payment:'2.30',returned:'0.00',difference:'2.30'}],cases:[]}
const before=financeTotals(original),data=structuredClone(original)
data.groups[0].other_expense={applied:true,kind:'other_fee',amount:'2.30',reason:'核实为其他支出',updated_at:now}
const after=financeTotals(data)
assert.equal(after.cost,before.cost+230)
assert.equal(after.otherFees,before.otherFees+230)
assert.equal(after.transaction,30,'linked fees must not be counted twice')
assert.equal(after.unverified,0)
assert.equal(after.unmatched,0)
assert.equal(after.pending,0,'manual expense must not be treated as pending assets')
assert.equal(reconciliation(data).gap,reconciliation(original).gap-230)
assert.equal(financeTotals(data,'c').otherFees,230)
assert.equal(financeTotals(data,'other').otherFees,0)
const rows=ledgerFunds(data).filter(r=>r.manual)
assert.equal(rows.length,1)
assert.equal(rows[0].amount,'-2.30')
assert.equal(rows[0].reason,'核实为其他支出')
delete data.groups[0].other_expense
assert.deepEqual(financeTotals(data),before,'undo restores exact original accounting')
data.trade_reviews=[{group_id:'g',applied:false,voided:false,problem:'流水已变化'}]
assert.equal(financeTotals(data).staleReviews,1)
assert(reconciliation(data).reasons.some(r=>r.includes('人工核实')))
const closed=structuredClone(original)
closed.cards[0].card.status='4'
closed.finance.funds.push({kind:4002,amount:'7.70',currency:'USD',serial_no:'c'})
closed.groups[0].other_expense={applied:true,amount:'2.30'}
assert.equal(cardFunding(closed,closed.cards[0]).closurePending,0,'recorded card expense explains closure residual without duplicate assets')
console.log('Passed: manual other expenses, per-card totals, no double fees, ledger reason, stale/undo and reconciliation')
