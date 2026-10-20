import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { cardBalanceDisplay, cardFunding, cardReturnCounts, reconciliation } from '../src/nexovcc-reconciliation.js'
const now=Date.parse('2026-10-19T12:00:00+08:00'),at=new Date(now).toISOString()
const raw=JSON.parse(readFileSync(new URL('../../internal/nexovcc/testdata/finance.json',import.meta.url),'utf8'))
const data={finance:{at,
 funds:raw['/api/findLogWebFunds'].list.map(r=>({kind:r.change_type_id,currency:'USD',serial_no:r.serial_no,amount:r.amount})),
 recharges:raw['/api/findLogRecharge'].list.map(r=>({status:String(r.status),currency:'USD',amount:r.amount,received:r.real_amount}))},
 groups:[{id:'g1',valid:true,fully_returned:true,serial_no:'fixture-card-1',payment:'92.33',returned:'92.28',difference:'0.05',fees:'0.80',cost:'0.85'}],
 cards:[{card:{id:'c1',serial_no:'fixture-card-1',status:'1',balance:'119.95',balance_at:at,synced_at:at}}],
 balance:{amount:'373.96',frozen:'0.05',at},operations:[],cases:[]}
const check=(d=data)=>reconciliation(d,now)
assert.equal(check().gap,0)
assert.equal(check().state,'balanced') // frozen balance and unmatched accounts must not duplicate financial amounts
assert.equal(check().totals.cost,635)
const service=structuredClone(data)
service.finance.funds.push({kind:3004,currency:'USD',serial_no:'fixture-card-1',amount:'-0.20'})
service.balance.amount='373.76'
assert.equal(check(service).gap,0)
assert.equal(check(service).state,'balanced')
assert.equal(check(service).totals.cost,655)
assert.equal(cardFunding(service,service.cards[0]).funded,12000)
assert.equal(cardFunding(data,data.cards[0]).funded,12000)
assert.equal(cardFunding(data,data.cards[0]).difference,null) // an open card's principal is not a loss
const added=structuredClone(data)
added.finance.funds.push({kind:4001,serial_no:'fixture-card-1',currency:'USD',amount:'-10.00'})
added.balance.amount='363.96';added.cards[0].card.balance='129.95'
assert.equal(check(added).gap,0)
assert.equal(cardFunding(added,added.cards[0]).funded,13000)
assert.equal(cardFunding(added,added.cards[0]).additions,1)

const closure=structuredClone(data)
closure.finance.funds.push({kind:4002,serial_no:'fixture-card-1',currency:'USD',amount:'118.95'})
closure.balance.amount='492.91';closure.cards[0].card.balance='1.00'
closure.operations=[{card_id:'c1',action:'cancel',status:'processing'}]
assert.equal(check(closure).gap,0)
assert.equal(check(closure).closurePending,0) // closing but still active: already included in card assets
assert.equal(check(closure).state,'stale')
closure.cards[0].card.status='4'
assert.equal(check(closure).gap,100)
assert.equal(check(closure).closurePending,100)
assert.equal(check(closure).unexplained,0)
assert.equal(cardFunding(closure,closure.cards[0]).difference,null)
assert.deepEqual(cardBalanceDisplay(closure,closure.cards[0]),{amount:'—',note:'余款待退回'})
closure.operations[0].status='succeeded'
assert.equal(check(closure).state,'pending') // local success alone doesn't prove receipt
closure.finance.funds.push({kind:3002,serial_no:'fixture-card-1',currency:'USD',amount:'1.00'})
closure.balance.amount='493.91'
assert.equal(check(closure).gap,0)
assert.equal(check(closure).closurePending,0)
assert.equal(check(closure).state,'balanced')
assert.equal(cardFunding(closure,closure.cards[0]).difference,5)
assert.equal(cardFunding(closure,closure.cards[0]).withdrawn,11895)
assert.deepEqual(cardBalanceDisplay(closure,closure.cards[0]),{amount:'0.00',note:'余款已退回'})
assert.equal(closure.cards[0].card.balance,'1.00') // preserve the historical final balance
assert.deepEqual(cardBalanceDisplay(data,data.cards[0]),{amount:'119.95',note:''})
const unverifiedClose=structuredClone(closure);unverifiedClose.groups[0].valid=false
assert.deepEqual(cardBalanceDisplay(unverifiedClose,unverifiedClose.cards[0]),{amount:'—',note:'余款待核实'})
for(const action of ['withdraw_all','withdraw_cancel']){
 const moving=structuredClone(data);moving.operations=[{card_id:'c1',action,status:'processing'}]
 assert.equal(check(moving).state,'stale')
 assert.equal(cardFunding(moving,moving.cards[0]).waiting,true)
}

const external=structuredClone(data)
external.finance.funds.push({kind:1001,currency:'USD',amount:'-100.00'})
external.balance.amount='273.96'
assert.equal(check(external).gap,0)
assert.equal(check(external).totals.cost,635)
const gap=structuredClone(data);gap.balance.amount='273.96'
assert.equal(check(gap).gap,10000)
assert.equal(check(gap).state,'difference')
assert.equal(check(gap).totals.cost,635) // unexplained difference never creates a new cost
const invalid=structuredClone(data);invalid.balance.amount=''
assert.equal(check(invalid).gap,null)
assert.equal(check(invalid).account,null)
assert.equal(check(invalid).state,'unknown')
const stale=structuredClone(data);stale.cards[0].card.balance_at='2026-10-18T12:00:00+08:00'
assert.equal(check(stale).state,'stale')
const partial=structuredClone(data);partial.finance_error='page 2 failed'
assert.equal(check(partial).state,'stale')
const unsupported=structuredClone(data);unsupported.finance.funds.push({kind:9999,currency:'USD',amount:'-0.30'})
assert.equal(check(unsupported).state,'stale')
const noFunding=structuredClone(data);noFunding.finance.funds=noFunding.finance.funds.filter(f=>f.kind!==3001)
assert.equal(cardFunding(noFunding,noFunding.cards[0]).funded,null)
assert.equal(check(noFunding).state,'stale')
const pending=structuredClone(data)
Object.assign(pending.groups[0],{fully_returned:false,returned:'0.00',difference:'92.33'})
pending.finance.funds=pending.finance.funds.filter(f=>f.kind!==2003)
pending.balance.amount='374.46';pending.cards[0].card.balance='27.67'
assert.equal(check(pending).gap,0)
assert.equal(check(pending).totals.pending,9233)
// Three-card production regression: one closed/refunded, one reversed with
// a 0.23 FX remainder, one new 92.33 authorization still awaiting a refund.
const latest=structuredClone(closure)
latest.balance.amount='251.81';latest.balance.frozen='92.56'
latest.cards.push(...['two','three'].map((id,i)=>({card:{id,serial_no:id,status:'1',balance:i?'27.67':'119.77',balance_at:at,synced_at:at}})))
latest.finance.funds.push(...['two','three'].flatMap(serial=>[{kind:3001,serial_no:serial,currency:'USD',amount:'-120.00'},{kind:3003,serial_no:serial,currency:'USD',amount:'-0.50'},{kind:2007,serial_no:serial,currency:'USD',amount:'-0.30'}]),{kind:2003,serial_no:'two',currency:'USD',amount:'-0.50'})
latest.groups.push({id:'g2',serial_no:'two',valid:true,fully_returned:true,payment:'92.33',returned:'92.10',difference:'0.23',cost:'1.03',fees:'0.80'}, {id:'g3',serial_no:'three',valid:true,fully_returned:false,payment:'92.33',returned:'0.00',difference:'92.33',cost:'0.30',fees:'0.30'})
assert.equal(check(latest).totals.cost,868)
assert.equal(check(latest).assets,49158)
assert.equal(check(latest).totals.pending,9233)
assert.equal(check(latest).gap,0)
assert.equal(check(latest).state,'balanced')

// Production amounts: a card was deleted before its 141.83 authorization was
// reversed, but 169.76 of its 170.00 funding already went back to the account.
// Aggregate unrelated settled cards; do not fabricate a refund or a write-off.
const deletedBeforeRefund = {
 balance:{amount:'1368.82',frozen:'141.85',at},operations:[],cases:[],
 cards:['pending','small-a','small-b','settled-history'].map(serial=>({card:{id:serial,serial_no:serial,status:'4',balance:'1.00',balance_at:at,synced_at:at}})),
 finance:{at,recharges:[{status:'1',currency:'USD',amount:'1501.67',received:'1486.65'}],funds:[
  ...['pending','small-a','small-b'].map(serial_no=>({kind:3001,serial_no,currency:'USD',amount:'-170.00',at})),
  {kind:3002,serial_no:'pending',currency:'USD',amount:'169.76',at},
  {kind:4002,serial_no:'small-a',currency:'USD',amount:'168.96',at},
  {kind:3002,serial_no:'small-a',currency:'USD',amount:'1.00',at},
  {kind:4002,serial_no:'small-b',currency:'USD',amount:'168.66',at},
  {kind:3002,serial_no:'small-b',currency:'USD',amount:'1.00',at},
  {kind:3001,serial_no:'settled-history',currency:'USD',amount:'-100.00',at},
  {kind:3002,serial_no:'settled-history',currency:'USD',amount:'48.97',at},
  {kind:2007,currency:'USD',amount:'-66.18',at},
 ]},
 groups:[
  {id:'pending-payment',serial_no:'pending',valid:true,fully_returned:false,settled:false,payment:'141.83',returned:'0.00',difference:'141.83'},
  {id:'returned-history',serial_no:'settled-history',valid:true,fully_returned:true,settled:true,payment:'100.00',returned:'95.73',difference:'4.27'},
  {id:'settled-history',serial_no:'settled-history',valid:true,fully_returned:false,settled:true,payment:'46.76',returned:'0.00',difference:'46.76'},
 ],
}
const unchanged=structuredClone(deletedBeforeRefund), corrected=check(deletedBeforeRefund)
assert.equal(corrected.pendingAssets,0)
assert.equal(corrected.closedAuthorization,14183)
assert.equal(corrected.totals.pending,14183,'the original payment is still awaiting refund/settlement')
assert.equal(corrected.assets,136882)
assert.equal(corrected.totals.cost,13223,'no invented expense or refund')
assert.equal(corrected.gap,62,'the old -141.21 difference must be corrected, not hidden')
assert.equal(corrected.closurePending,62,'all unexplained closure residuals remain visible')
assert.equal(corrected.unexplained,0)
assert.equal(cardFunding(deletedBeforeRefund,deletedBeforeRefund.cards[0]).closurePending,24)
assert.deepEqual(deletedBeforeRefund,unchanged,'reconciliation must not mutate raw financial evidence or account state')

for (const variation of ['active','no-return','zero-return','old-balance','unknown-return-time','in-progress','invalid-trade','stale-review']) {
 const d=structuredClone(deletedBeforeRefund),card=d.cards[0].card,refund=d.finance.funds.find(f=>f.kind===3002&&f.serial_no==='pending')
 if(variation==='active')card.status='1'
 if(variation==='no-return')d.finance.funds=d.finance.funds.filter(f=>f!==refund)
 if(variation==='zero-return')refund.amount='0.00'
 if(variation==='old-balance')d.balance.at=new Date(now-1000).toISOString()
 if(variation==='unknown-return-time')delete refund.at
 if(variation==='in-progress')d.operations=[{card_id:card.id,action:'cancel',status:'processing'}]
 if(variation==='invalid-trade')d.groups.push({id:'unknown',serial_no:'pending',valid:false,payment:'0.00',returned:'0.00',difference:'0.00'})
 if(variation==='stale-review'){d.trade_reviews=[{group_id:'pending-payment',applied:false,voided:false}];d.review_groups=d.groups}
 assert.equal(check(d).pendingAssets,14183,variation+' cannot prove a completed transfer to the account')
 assert.equal(check(d).closedAuthorization,0,variation)
}
const partialReturn=structuredClone(deletedBeforeRefund)
partialReturn.finance.funds.find(f=>f.kind===3002&&f.serial_no==='pending').amount='27.93'
partialReturn.balance.amount='1226.99'
assert.equal(check(partialReturn).pendingAssets,0)
assert.equal(check(partialReturn).gap,14245)
assert.equal(check(partialReturn).closurePending,14245,'a partial closure return must leave the missing principal visible')
const laterSettlement=structuredClone(deletedBeforeRefund)
laterSettlement.groups[0].settled=true
laterSettlement.balance.amount='1226.99'
assert.equal(check(laterSettlement).closedAuthorization,0)
assert.equal(check(laterSettlement).totals.cost,13223+14183)
assert.equal(check(laterSettlement).gap,62,'a later actual settlement is counted once')
const laterReversal=structuredClone(deletedBeforeRefund)
Object.assign(laterReversal.groups[0],{fully_returned:true,returned:'141.83',difference:'0.00'})
assert.equal(check(laterReversal).closedAuthorization,0)
assert.equal(check(laterReversal).totals.pending,0)
assert.equal(check(laterReversal).totals.cost,13223)
assert.equal(check(laterReversal).gap,62,'a later release after the balance was returned does not create another asset')
assert.deepEqual(cardReturnCounts([{state:'unopened'},{state:'opened'},{state:'waiting'},{state:'waiting'},{state:'returned'},{state:'invalid'}]),{unopened:1,opened:1,waiting:2,returned:1})
console.log('Passed: funding, additional deposits, withdrawals, pending/final closure returns, deletion before refund, later settlements/reversals, pending payments, external withdrawals, missing/stale/partial data, unexplained differences and account states')
