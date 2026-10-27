import assert from 'node:assert/strict'
import {ejhDeadLoss} from '../src/dead-account-loss.js'
import {financeOverview} from '../src/ejh-finance.js'
import {financeTotals,caseAmounts} from '../src/nexovcc-finance.js'
import {paymentOnlySummary} from '../src/payment-only.js'
import {concurrencyDetails} from '../src/restockForecast.js'
const at='2026-10-27T08:00:00+08:00'
const trade=(id,type,amount,status='2')=>({id,card_id:'c',confirmed:true,confirmed_amount:amount,confirmed_currency:'USD',trade:{tradeNo:id,authCode:'a',tradeType:type,tradeStatus:status,cardAmount:amount,cardCurrency:'USD',billAmount:amount,billCurrency:'USD',tradeTime:at}})
const auth=trade('auth','1','100','1'),clear=trade('clear','4','100')
const row={id:'o',card_id:'c',ejh_card_id:'c',payment_status:'success',state:'waiting',pro_dead:{at},charge:{trade_no:'auth',type:'1',status:'1',card_amount:'100',card_currency:'USD'},returns:[],consumption_total:10}
const data={cards:[{id:'c',balance:'0',remote:{status:'1',cardCurrency:'USD'}}],cases:[row],trades:[auth],operations:[],recharge_ledger:{amount:'200'},balance:{data:{abroadList:[{currency:'USD',balance:'100'}]}}}
let d=ejhDeadLoss(data);assert.equal(d.amount,10000);assert.equal(d.alreadyCost,0)
data.trades.push(clear);d=ejhDeadLoss(data);assert.equal(d.amount,10000);assert.equal(d.alreadyCost,10000)
let overview=financeOverview(data);assert.equal(overview.platformClearingUSD,'0.00');assert.equal(overview.dead.amount,10000);assert.equal(overview.inFlight.count,0)
for(const [status,amount] of [['1',10000],['2',6000]]){const refund=trade('return','5','40',status);const partial={...data,trades:[auth,clear,refund],cases:[{...row,returns:[refund,refund]}]};assert.equal(ejhDeadLoss(partial).amount,amount,'only posted unique refunds reduce loss')}
const full=trade('full','5','100');assert.equal(ejhDeadLoss({...data,trades:[auth,clear,full],cases:[{...row,returns:[full]}]}).amount,0)
assert.equal(ejhDeadLoss({...data,cases:[row,{...row,id:'duplicate'}]}).amount,10000,'authorization counted once')
assert.equal(paymentOnlySummary({...data,payment_policies:[{card_id:'c',mode:'payment_only'}]},'ejh').amount,0,'dead loss has precedence over external spend')
const group={id:'g',serial_no:'s',valid:true,fully_returned:false,settled:false,payment:'100',returned:'0',difference:'100',fees:'1',cost:'1'}
const nc={id:'o',card_id:'c',pro_dead:{at},payment_status:'success',match:{group},revenue_r:10}
const nd={cards:[{card:{id:'c',serial_no:'s'},local:{dead_account:true}}],cases:[nc],groups:[group],finance:{at,funds:[{kind:2007,serial_no:'s',currency:'USD',amount:'-1'}],recharges:[]}}
assert.equal(financeTotals(nd).deadLoss,10000);assert.equal(financeTotals(nd).pending,0);assert.equal(financeTotals(nd).cost,10100);assert.equal(caseAmounts(nc).cost,10100)
group.settled=true;group.cost='101';assert.equal(financeTotals(nd).cost,10100);assert.equal(financeTotals(nd).settlement,0);assert.equal(caseAmounts(nc).cost,10100)
group.returned='40';group.difference='60';group.cost='61';assert.equal(financeTotals(nd).deadLoss,6000);assert.equal(financeTotals(nd).cost,6100)
group.returned='100';group.difference='0';group.cost='1';group.fully_returned=true;assert.equal(financeTotals(nd).deadLoss,0);assert.equal(financeTotals(nd).cost,100)
const tiers={capacity_percent:100,capacity_tiers_enabled:true,capacity_tiers:[{min_capacity:0,trigger_percent:80,target_percent:75},{min_capacity:120,trigger_percent:85,target_percent:80}]}
for(const [capacity,trigger,count] of [[60,80,48],[119,80,95],[120,85,102]]){const x=concurrencyDetails({valid:true,load_valid:true,accounts:[{ready:true,capacity,used:1}]},false,Date.now(),tiers);assert.equal(x.triggerPercent,trigger);assert.equal(x.triggerCount,count)}
console.log('Passed: dead principal, settled reclassification, partial/full refunds, duplicate evidence, external-spend precedence, and capacity tier boundaries')
