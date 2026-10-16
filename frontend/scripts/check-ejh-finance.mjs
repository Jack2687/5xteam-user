import assert from 'node:assert/strict'
import { financeOverview, financeReport, periodRange, financeTime, beijingDay, cents, money, consumptionSummary } from '../src/ejh-finance.js'

const card={id:'card',issuer_linked:true,balance:'22595.26',balance_at:'2026-10-16T17:17:20+08:00',remote:{orderNo:'opened',status:'3',cardCurrency:'PHP'}}
const closed={id:'closed',issuer_linked:true,balance:'999999',balance_at:card.balance_at,remote:{orderNo:'closed-order',status:'2',cardCurrency:'PHP'}}
const operation={id:'open',method:'open',status:'completed',card_id:'card',order_no:'opened',created_at:'2026-10-15T12:00:00+08:00',request:{cardAmount:'10000',cardCurrency:'PHP'},result:{cardAmount:'10000',cardCurrency:'PHP',billAmount:'160.27',billCurrency:'USD'}}
const trade={id:'reversal',card_id:'card',trade:{tradeNo:'trade',tradeType:'2',tradeStatus:'1',cardAmount:'5780.48',cardCurrency:'PHP',orderNo:'opened',tradeTime:'2026-10-16 17:17:20'}}
const data={recharge_ledger:{amount:'3000.00'},balance:{data:{abroadList:[{currency:'USD',balance:'2273.15'}]}},cards:[card,closed],operations:[operation,{...operation,id:'duplicate-open'}],trades:[trade,{...trade,id:'duplicate-trade'}],cases:[{id:'pay',email:'user@example.com',ejh_card_id:'card',state:'waiting',consumption_total:120,charge:{type:'1',status:'1',trade_no:'hold',card_amount:'17383.92',card_currency:'PHP'}},{id:'returned',email:'user@example.com',state:'returned',consumption_total:120}],reversal_fee_percent:6}
let total=financeOverview(data)
assert.equal(total.net,'2970.00');assert.equal(total.depositFee,'30.00')
assert.equal(total.openingFee,'1.00','closed card fees must remain, duplicate operations must not add fees')
assert.equal(total.reversal.rows[0].amount,'346.83','fee rounds each distinct reversal once')
assert.equal(total.reversal.count,1)
assert.equal(total.consumption.total,120)
assert.equal(total.loss,money(297000n-227315n-cents(total.cardUSD.amount)-cents(total.inFlight.paymentUSD.amount)))
assert.equal(total.cardUSD.total,1,'closed card balance excluded')
assert.equal(financeOverview({...data,cards:[{...card,balance_at:null},closed]}).loss,null,'unknown balances cannot turn into zero')
assert.equal(financeOverview({...data,operations:[]}).loss,null,'unknown FX prevents loss calculation')
assert.equal(financeOverview({...data,cases:[{id:'unmatched',state:'waiting'}]}).loss,null)
assert.equal(financeOverview({...data,balance:{data:{abroadList:[{currency:'EUR',balance:'12'}]}}}).loss,null)
assert.equal(financeOverview({...data,cards:[{...card,remote:{...card.remote,status:'8'}}]}).cardUSD.total,1)
assert.equal(consumptionSummary([{email:'deleted@example.com',consumption_total:null}],[{id:'old',email:'deleted@example.com',total:40}]).total,40)

const history={consumption:[{id:'recorded',email:'user@example.com',total:120,baseline:100}],usage_days:[{id:'day1',email:'user@example.com',day:'2026-10-15',amount:8},{id:'day2',email:'user@example.com',day:'2026-10-16',amount:12}],recharges:[{id:'deposit',at:'2026-10-16T02:00:00+08:00',before:'2000.00',after:'3000.00',delta:'1000.00',baseline:false}]}
let report=financeReport(data,history,'day','2026-10-16')
assert.equal(report.summary.r,12);assert.equal(report.summary.rechargeUSD,'10.00');assert.equal(report.summary.usdt,'0.00');assert.equal(report.baseline,100)
assert.equal(report.events.filter(e=>e.kind==='reversal').length,1)
report=financeReport(data,history,'all')
assert.equal(report.summary.r,120);assert.equal(report.summary.rechargeUSD,'30.00');assert.equal(report.summary.usdt,'1.00')
const multiple={...data,trades:Array.from({length:12},(_,i)=>({...trade,id:'many-'+i,trade:{...trade.trade,tradeNo:'many-'+i}}))}
assert.equal(financeReport(multiple,history,'all').summary.reversalUSD,financeOverview(multiple).reversal.usd.amount,'USD must round after summing, matching the card dashboard')
assert.equal(report.rows.reduce((n,r)=>n+r.r,0),120,'period rows plus undated baseline reconcile to all-time usage')
assert.equal(report.rows.reduce((n,r)=>n+(cents(r.rechargeUSD)||0n),0n),3000n)
assert.equal(financeReport(data,history,'day','2026-10-14').events.length,0)
assert.equal(beijingDay(financeTime('2026-10-15T16:00:00Z')),'2026-10-16')
const week=periodRange('week','2026-10-18');assert.equal(beijingDay(week.start),'2026-10-12');assert.equal(beijingDay(week.end-1),'2026-10-18')
const month=periodRange('month','2026-12-31');assert.equal(beijingDay(month.end),'2027-01-01')
assert.equal(financeTime('2026-10-16 00:00:00'),Date.parse('2026-10-15T16:00:00Z'))
const snapshot={...data,at:'2026-10-16T00:05:00+08:00'}
assert.equal(financeReport(data,{...history,snapshots:[snapshot]},'day','2026-10-16').lossChange,null,'one observation is not a period loss')
assert.equal(financeReport(data,{...history,snapshots:[snapshot,{...snapshot,at:'2026-10-16T23:59:00+08:00',balance:{data:{abroadList:[{currency:'USD',balance:'2270.15'}]}}}]},'day','2026-10-16').lossChange,'3.00')
console.log('Passed: exact fee arithmetic, duplicate transactions/cards/accounts, closed and pending cards, missing FX, Beijing day/week/month, undated baselines, deleted accounts and snapshot loss changes')
