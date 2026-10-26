import assert from 'node:assert/strict'
import {financeOverview,financeReport,financeEvents} from '../src/ejh-finance.js'
import {refundFeeSummary} from '../src/ejh-refund-fees.js'

const at='2026-10-19T12:24:46+08:00'
const fee={id:'window-1',amount:'24.84',at,source:'balance_reconciliation',note:'4笔清算后退款，按余额核对登记',revision:1}
const base={cards:[],operations:[],cases:[],trades:[],balance:{data:{abroadList:[{currency:'USD',balance:'2943.25'}]}},recharge_ledger:{amount:'3000',history_month:'2026-10'}}
const before=financeOverview(base),after=financeOverview({...base,refund_fees:[fee,fee]})
assert.equal(before.gap,'26.75')
assert.equal(after.gap,'1.91')
assert.equal(after.refundFees.amount,'24.84')
assert.equal(after.refundFees.count,1)
assert.equal(after.earnings.refundR,'166.4280')
assert.equal(after.assets,before.assets,'fee must not mutate balances or refund principal')
assert.equal(after.loss,before.loss,'expense classification does not change cash loss')
assert.equal(after.earnings.estimated,true,'balance inference must not claim issuer verification')
for(const mode of ['day','week','month','all']) {
 const report=financeReport({...base,refund_fees:[fee,fee]},{},mode,'2026-10-19')
 assert.equal(report.summary.refundUSD,'24.84',mode)
 assert.equal(report.events.filter(e=>e.kind==='refund').length,1)
 assert.equal(report.summary.earnings.refundR,'166.4280')
 assert.equal(report.rows.find(r=>r.refundUSD==='24.84').earnings.refundR,'166.4280')
}
for(const [mode,date] of [['day','2026-10-20'],['week','2026-10-12'],['month','2026-11-01']])assert.equal(financeReport({...base,refund_fees:[fee]},{},mode,date).summary.refundUSD,'0.00')
const midnight={...fee,id:'midnight',at:'2026-10-19T16:00:00Z'}
assert.equal(financeReport({...base,refund_fees:[midnight]},{},'day','2026-10-19').summary.refundUSD,'0.00')
assert.equal(financeReport({...base,refund_fees:[midnight]},{},'day','2026-10-20').summary.refundUSD,'24.84')
const voided={...fee,revision:2,voided:true}
assert.equal(financeOverview({...base,refund_fees:[voided,fee]}).gap,before.gap)
assert.equal(refundFeeSummary([fee,{...fee,revision:2,amount:'25.00'}]).amount,'25.00')
assert.equal(refundFeeSummary([{...fee,amount:'garbage'}]).missing,1)
assert.equal(financeOverview({...base,refund_fees:[{...fee,amount:'garbage'}]}).earnings.costUSD,null)
assert.equal(financeEvents({...base,refund_fees:[voided,fee]}).filter(e=>e.kind==='refund').length,0)
const refunds={...base,trades:[{id:'refund',card_id:'c',trade:{tradeType:'5',tradeStatus:'2',cardAmount:'92.13',cardCurrency:'USD',billAmount:'92.13',billCurrency:'USD'}}]}
assert.equal(financeOverview(refunds).refundFees.amount,'5.53','posted refund uses the configured default rate as an explicit estimate')
assert.equal(financeOverview(refunds).refundFees.estimated,true)
console.log('Passed: refund fees, deduplication, revision/void, day/week/month and Beijing midnight, unchanged assets/principal, explicit provenance')
