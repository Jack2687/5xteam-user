import assert from 'node:assert/strict'
import { balanceCostAdjustments, financeTotals, ledgerFunds, rmb } from '../src/nexovcc-finance.js'
import { reconciliation, cardFunding, frozenBalanceDisplay } from '../src/nexovcc-reconciliation.js'

const at='2026-10-20T18:52:00+08:00',before='2026-10-20T18:30:00+08:00'
function fixture(balance='169.73',waiting=false){
 const group={id:'payment',serial_no:'card',valid:true,fully_returned:!waiting,settled:false,payment:'141.82',returned:waiting?'0.00':'141.82',difference:waiting?'141.82':'0.00',fees:'6.42',cost:'6.42',at:before,returned_at:waiting?'0001-01-01T00:00:00Z':before}
 return {finance:{at,card_at:{card:at},recharges:[{status:'1',currency:'USD',amount:'500.00',received:'495.00'}],funds:
  [[3001,'-170.00'],[3003,'-0.50'],[3004,'-0.20'],[2007,'-3.42'],[2003,'-3.00']].map(([kind,amount],i)=>({id:'f'+i,kind,amount,serial_no:'card',currency:'USD',at:before}))},
  cards:[{card:{id:'c1',serial_no:'card',last4:'9731',status:'1',balance,balance_at:at,synced_at:at}}],
  groups:[group],cases:[{id:'o1',card_id:'c1',state:waiting?'waiting':'returned',requested_at:before,payment_status:'success',match:{group},revenue_r:167.04}],
  operations:[],balance:{amount:'317.88',at}}
}
for(const [balance,expected] of [['169.73',27],['169.70',30],['169.39',61]]){
 const d=fixture(balance),original=structuredClone(d),t=financeTotals(d,'card')
 assert.equal(t.balanceDifference,expected)
 assert.equal(t.cost,712+expected)
 assert.equal(t.profit,16704-Math.round((712+expected)*6.7))
 assert.equal(financeTotals(d).cost,1212+expected)
 assert.equal(ledgerFunds(d).filter(r=>r.estimated).length,1)
 assert.equal(reconciliation(d,Date.parse(at)).gap,0)
 assert.deepEqual(d,original,'estimated costs must not rewrite original data')
}
assert.equal(rmb(financeTotals(fixture(),'card').cost),'49.51')
const pending=fixture('169.39',true)
const beforeTotals=financeTotals(pending,'card')
assert.equal(beforeTotals.balanceDifference,61)
assert.equal(beforeTotals.balanceRecoveredPending,14182)
assert.equal(beforeTotals.pending,14182,'keep original refund pending')
assert.equal(reconciliation(pending,Date.parse(at)).pendingAssets,0,'do not count recovered balance twice')
assert.equal(pending.cases[0].state,'waiting')
assert.equal(pending.groups[0].returned,'0.00')
// Production headline: 170 + 28.18 + 169.39 = 367.57 in normal cards;
// only 2842's 141.82 remains outside balances. 4276 is awaiting its ledger.
const overview=structuredClone(pending)
overview.cards[0].card.last4='4276'
overview.balance.frozen='425.49'
overview.cards.push({card:{id:'new',serial_no:'new',last4:'1527',status:'1',balance:'170.00',balance_at:at}},
 {card:{id:'open',serial_no:'open',last4:'2842',status:'1',balance:'28.18',balance_at:at}},
 {card:{id:'deleted',serial_no:'deleted',last4:'0450',status:'4',balance:'169.76',balance_at:at}})
overview.groups.push({id:'open',serial_no:'open',valid:true,payment:'141.82',returned:'0.00',difference:'141.82',at:before},
 {id:'deleted',serial_no:'deleted',valid:true,payment:'141.83',returned:'0.00',difference:'141.83',at:before})
overview.cases.push({id:'opened',card_id:'open',state:'opened',payment_status:'success',match:{group:overview.groups[1]}})
const overviewBefore=structuredClone(overview),overviewReport=reconciliation(overview,Date.parse(at))
const freeze=frozenBalanceDisplay(overview)
assert.equal(freeze.current,14182)
assert.equal(freeze.recovered,14182)
assert.equal(freeze.historical,14183)
assert.equal(freeze.platform,42549,'retain supplier aggregate without assigning its small residual to active holds')
assert.deepEqual(freeze.pendingRows.map(r=>r.last4),['2842'])
assert.deepEqual(freeze.recoveredRows.map(r=>r.last4),['4276'])
assert.equal(overviewReport.cardBalance,36757)
assert.deepEqual(overview,overviewBefore)
assert.deepEqual(reconciliation(overview,Date.parse(at)),overviewReport)
overview.cases[1].state='waiting';overview.cases[1].requested_at=before
assert.equal(frozenBalanceDisplay(overview).current,14182,'an application alone does not mean the money returned')
assert.equal(frozenBalanceDisplay(laterRefundFixture()).current,0)
function laterRefundFixture(){const d=structuredClone(pending);Object.assign(d.groups[0],{fully_returned:true,returned:'141.21',difference:'0.61',returned_at:before});return d}
const late=structuredClone(pending)
Object.assign(late.groups[0],{fully_returned:true,returned:'141.21',difference:'0.61',returned_at:before})
assert.equal(financeTotals(late,'card').balanceDifference,0)
assert.equal(financeTotals(late,'card').cost,beforeTotals.cost,'posted loss replaces, not adds to estimate')
assert.equal(financeTotals(late,'card').pending,0)
const partlyKnown=fixture('169.39')
Object.assign(partlyKnown.groups[0],{returned:'141.62',difference:'0.20'})
assert.equal(financeTotals(partlyKnown,'card').balanceDifference,41)
assert.equal(financeTotals(partlyKnown,'card').cost,beforeTotals.cost)

for(const type of ['ordinary pending','unrequested','unmatched','moving','stale ledger','stale balance','missing opening','foreign','unknown fee','invalid group','manual expense','sync error']){
 const d=structuredClone(pending)
 if(type==='ordinary pending')d.cards[0].card.balance='28.18'
 if(type==='unrequested')d.cases[0].requested_at=''
 if(type==='unmatched')d.cases=[]
 if(type==='moving')d.operations=[{card_id:'c1',action:'withdraw_all',status:'processing'}]
 if(type==='stale ledger')d.finance.card_at.card='2026-10-20T18:00:00+08:00'
 if(type==='stale balance')d.cards[0].card.balance_at='2026-10-20T17:00:00+08:00'
 if(type==='missing opening')d.finance.funds.shift()
 if(type==='foreign')d.finance.funds[0].currency='PHP'
 if(type==='unknown fee')d.finance.funds[1].kind=9999
 if(type==='invalid group')d.groups[0].valid=false
 if(type==='manual expense')d.groups[0].other_expense={applied:true,amount:'141.82'}
 if(type==='sync error')d.finance_error='page failed'
 assert.equal(financeTotals(d,'card').balanceDifference,0,type)
 assert.equal(financeTotals(d,'card').balanceRecoveredPending,0,type)
}
const withdrawal=structuredClone(pending)
withdrawal.cards[0].card.balance='1.00'
withdrawal.cards[0].card.balance_at='2026-10-20T18:53:10+08:00'
withdrawal.finance.funds_at={card:'2026-10-20T18:53:09+08:00'}
withdrawal.finance.funds.push({id:'withdraw',kind:4002,serial_no:'card',currency:'USD',amount:'168.39',at:'2026-10-20T18:53:05+08:00'})
withdrawal.operations=[{action:'withdraw_all',card_id:'c1',status:'succeeded',created_at:'2026-10-20T18:53:00+08:00',updated_at:'2026-10-20T18:53:09+08:00',evidence:'fund:withdraw',before_balance:'169.39',amount:'168.39'}]
assert.equal(financeTotals(withdrawal,'card').balanceDifference,61,'withdrawal leaves the same cost')
const staleTransfer=structuredClone(withdrawal);staleTransfer.cards[0].card.balance='169.39'
assert.equal(financeTotals(staleTransfer,'card').balanceDifference,0,'mixed transfer snapshots are not expenses')
const closing=structuredClone(withdrawal)
Object.assign(closing.groups[0],{fully_returned:true,returned:'141.82',difference:'0.00'})
closing.cards[0].card.status='4';closing.balance.at='2026-10-20T18:55:00+08:00'
assert.equal(financeTotals(closing,'card').balanceDifference,0,'deleted without receipt is not a loss')
closing.finance.funds.push({id:'closed',kind:3002,serial_no:'card',currency:'USD',amount:'1.00',at:'2026-10-20T18:54:00+08:00'})
closing.operations.push({action:'withdraw_cancel',card_id:'c1',status:'succeeded',evidence:'fund:closed'})
assert.equal(financeTotals(closing,'card').balanceDifference,61,'verified final refund retains the card loss')
assert.equal(cardFunding(closing,closing.cards[0]).closurePending,0)
const afterOtherRefund=structuredClone(closing);afterOtherRefund.groups[0].fully_returned=false
assert.equal(financeTotals(afterOtherRefund,'card').balanceDifference,0,'deleted-before-refund must stay unresolved')
console.log('Passed: 0.27/0.30/0.61 USD residual costs, USD/R profit, late-refund deduplication, guarded estimates and withdrawal/closure continuity')
