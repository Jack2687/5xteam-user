import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import { ref, reactive } from 'vue'
import { accountAmounts, cardAmounts, summarizeInFlightPayments } from '../src/ejh-account-amounts.js'

const charge={type:'4',status:'2',card_amount:'100.30',card_currency:'USD'}
const returned=(id,amount,currency='USD',extra={})=>({id,confirmed:true,confirmed_amount:amount,confirmed_currency:currency,trade:{tradeType:'5',tradeStatus:'2'},...extra})
const a=returned('a','0.10'),b=returned('b','0.20')
assert.equal(accountAmounts({charge,returns:[a,b,a]}).difference,'100.00')
assert.deepEqual(accountAmounts({charge,returns:[a,b]}).returned,[{amount:'0.30',currency:'USD'}])
assert.equal(accountAmounts({charge,returns:[returned('a','101.40')]}).difference,'-1.10')
assert.equal(accountAmounts({credits:{charged:100},returns:[a]}).paid,null)
assert.equal(accountAmounts({charge:{...charge,card_amount:''}}).difference,null)
assert.equal(accountAmounts({charge:{...charge,card_amount:'0'}}).difference,'0.00')
assert.equal(accountAmounts({charge:{...charge,card_amount:'0.000001'}}).difference,'0.000001')
assert.equal(accountAmounts({charge:{...charge,status:'1'}}).difference,null)
assert.equal(accountAmounts({charge:{...charge,type:'1'}}).authorization,true)
assert.equal(accountAmounts({charge:{...charge,type:'3'}}).paid,null)
assert.equal(accountAmounts({charge,returns:[returned('a','1','EUR')]}).difference,null)
assert.equal(accountAmounts({charge,returns:[returned('a','bad')]}).invalid,true)
assert.equal(accountAmounts({charge,returns:[returned('a','bad')]}).difference,null)
for(const amount of ['-1','1e2','NaN','9999999999999','0.0000001'])assert.equal(accountAmounts({charge:{...charge,card_amount:amount}}).paid,null)
const unconfirmed=accountAmounts({charge,returns:[returned('a','10','USD',{confirmed:false}),returned('b','10','USD',{trade:{tradeType:'5',tradeStatus:'1'}}),returned('c','99','USD',{trade:{tradeType:'17',tradeStatus:'2'}})]})
assert.equal(unconfirmed.pending,2)
assert.equal(unconfirmed.returned.length,0)
assert.equal(unconfirmed.difference,'100.30')
const timedReturn=(id,time,extra={})=>returned(id,'10','USD',{trade:{tradeType:'5',tradeStatus:'2',tradeTime:time},...extra})
const firstReturn=timedReturn('first','2026-10-16T01:00:00Z')
const lastReturn=timedReturn('last','2026-10-16T02:00:00Z')
const pendingReturn=timedReturn('pending','2026-10-16T03:00:00Z',{confirmed:false})
for(const returns of [[lastReturn,firstReturn,pendingReturn],[firstReturn,pendingReturn,lastReturn]]){
 assert.equal(accountAmounts({charge,returns}).returnedAt,Date.parse('2026-10-16T02:00:00Z'),'latest confirmed return time, independent of list order')
}
assert.equal(accountAmounts({charge,returns:[pendingReturn]}).returnedAt,0)
assert.equal(accountAmounts({charge,returns:[returned('missing','10','USD',{confirmed_at:'2026-10-16T03:00:00Z',synced_at:'2026-10-16T04:00:00Z'})]}).returnedAt,0,'sync/association time must not be shown as return time')
assert.equal(accountAmounts({charge,returns:[timedReturn('invalid','not-a-date')]}).returnedAt,0)
assert.equal(accountAmounts({charge,returns:[timedReturn('reversal','2026-10-16 10:00:00',{trade:{tradeType:'2',tradeStatus:'1',tradeTime:'2026-10-16 10:00:00'}})]}).returnedAt,Date.parse('2026-10-16T10:00:00+08:00'))

const code=readFileSync(new URL('../src/components/EJHCardTrades.vue',import.meta.url),'utf8').split('<script setup>')[1].split('</script>')[0].replace(/^import .*$/gm,'')
const calls=[],unmounts=[]
let fail=false,release,hold=false
const context=vm.createContext({ref,reactive,AbortController,defineEmits:()=>()=>{},defineProps:()=>({feePercent:6,defaultPageSize:50}),defineExpose(){},onBeforeUnmount:fn=>unmounts.push(fn),async api(url,options){calls.push({url,options});if(hold)await new Promise(resolve=>{release=resolve});if(fail)throw Error('fixture failure');return {items:[{tradeNo:'saved'}],page:options.body.page,has_more:true,queried_at:'2026-10-15T00:00:00Z'}}})
vm.runInContext(code+';globalThis.f={open,query,dates,rows,page,applied,requestedPage,error,busy};',context)
const f=context.f
await f.open({id:'card/one'})
assert.equal(calls[0].url,'/api/ejh/cards/card%2Fone/trades')
assert.equal(calls[0].options.method,'POST')
assert.equal(calls[0].options.body.page_size,50,'trades must use the global page size')
const original={...calls[0].options.body}
f.dates.start='2020-01-01'
await f.query(2);assert.equal(calls.at(-1).options.body.start,original.start)
await f.query(1);assert.equal(calls.at(-1).options.body.start,original.start,'previous page must retain applied dates')
await f.query(1,true);assert.equal(calls.at(-1).options.body.start,'2020-01-01')
fail=true;await f.query(2)
assert.equal(f.page.value,1);assert.equal(f.rows.value[0].tradeNo,'saved');assert.equal(f.error.value,'fixture failure')
fail=false;hold=true
const pending=f.query(2),count=calls.length
assert.equal(f.requestedPage.value,2);assert.equal(f.busy.value,true)
await f.query(3);assert.equal(calls.length,count,'duplicate request admitted')
unmounts.forEach(fn=>fn());assert.equal(calls.at(-1).options.signal.aborted,true)
release();await pending;assert.equal(f.page.value,1,'disposed dialog was updated')
console.log('Passed: precise payment/return/difference, missing/pending/mixed currency, duplicate and closure exclusions; per-card query, pagination date retention, error retention, progress, duplicate requests and disposal')

assert.equal(accountAmounts({charge:{...charge,type:'1',status:'1'}}).difference,'100.30')
const ct=(id,type,amount,currency='PHP',extra={})=>({id,card_id:'c',trade:{tradeNo:id,tradeType:type,tradeStatus:'2',cardAmount:amount,cardCurrency:currency,...extra}})
const auth=ct('auth','1','100.30','PHP',{authCode:'A',tradeStatus:'1'}),clearing=ct('clear','4','100.20','PHP',{authCode:'A'}),refund=ct('refund','5','97.10','PHP',{authCode:'A'})
const cm=cardAmounts({id:'c'},[auth,clearing,refund,refund,ct('closure','17','1000'),ct('failed','3','600'),ct('usd','1','0.30','USD'),ct('eur','4','0.10','EUR'),{...auth,card_id:'other'}])
assert.deepEqual(cm.rows.map(({currency,paid,returned,difference})=>({currency,paid,returned,difference})),[{currency:'EUR',paid:'0.10',returned:'0.00',difference:'0.10'},{currency:'PHP',paid:'100.20',returned:'97.10',difference:'3.10'},{currency:'USD',paid:'0.30',returned:'0.00',difference:'0.30'}])
assert.equal(cm.fee,'0.50 USDT')
const actualPay=ct('actual-pay','1','5794.64','PHP',{tradeStatus:'1',authCode:'259562'})
const reversal=ct('actual-return','2','5780.87','PHP',{tradeStatus:'1',authCode:'259562'})
assert.equal(cardAmounts({id:'c'},[actualPay,reversal]).rows[0].difference,'13.77')
assert.equal(cardAmounts({id:'c'},[actualPay,reversal]).pending,0)
assert.equal(accountAmounts({charge:{type:'1',status:'1',card_amount:'5794.64',card_currency:'PHP'},returns:[returned('actual-return','5780.87','PHP',{trade:reversal.trade})]}).difference,'13.77')
assert.equal(cardAmounts({id:'c'},[auth]).rows[0].authorization,true)
assert.equal(cardAmounts({id:'c'},[auth,ct('bad','5','','PHP')]).rows[0].difference,null)
assert.equal(cardAmounts({id:'c'},[auth,ct('pending','5','3','PHP',{tradeStatus:'1'})]).pending,1)
const early=ct('early','2','97.10','PHP',{authCode:'A',tradeTime:'2026-10-15T12:00:00+08:00'}),late=ct('late','5','96.00','PHP',{authCode:'A',tradeTime:'2026-10-15T13:00:00+08:00'})
for(const rows of [[late,early],[early,late]])assert.equal(cardAmounts({id:'c'},[auth,...rows]).rows[0].returned,'97.10','return dedup must not depend on API ordering')
assert.equal(cardAmounts({id:'c'},[auth,early,{...late,confirmed:true,payment_order_id:'order'}]).rows[0].returned,'96.00','preserve the return actually associated with the account')
const view=readFileSync(new URL('../src/components/EJHManagementView.vue',import.meta.url),'utf8')
assert.equal(view.includes('流水同步'),false)
assert.equal(view.includes('matchForm'),false)
assert.equal((view.match(/<EJHCardAmounts /g)||[]).length,2)
console.log('Passed: card totals, clearing/auth deduplication, per-currency differences, fixed fee and retired manual controls')

// Only opened / waiting cases contribute to in-flight totals, independent of
// card currency and consumption credits. Never merge transaction and bill units.
const inFlightCase=(id,state='opened',extra={})=>({id,state,email:'same@example.com',ejh_card_id:'c',charge:{id:'charge-'+id,trade_no:'trade-'+id,type:'1',status:'1',trade_amount:'100.10',trade_currency:'PHP',bill_amount:'1.20',bill_currency:'USD',card_amount:'99999',card_currency:'JPY',...extra}})
const inflightRows=[inFlightCase('a'),inFlightCase('b','waiting'),inFlightCase('c','returned'),inFlightCase('d','unopened')]
const inFlightTotals=summarizeInFlightPayments(inflightRows)
assert.equal(inFlightTotals.count,2)
assert.deepEqual(inFlightTotals.transaction,[{amount:'200.20',currency:'PHP'}])
assert.deepEqual(inFlightTotals.settlement,[{amount:'2.40',currency:'USD'}])
assert.equal(inFlightTotals.authorizations,2)
assert.equal(inFlightTotals.missingTransaction,0)
assert.deepEqual(summarizeInFlightPayments([...inflightRows,inflightRows[0]]),inFlightTotals,'duplicate case does not duplicate payment')
const duplicatePayment={...inflightRows[0],id:'same-charge'}
assert.deepEqual(summarizeInFlightPayments([...inflightRows,duplicatePayment]).transaction,inFlightTotals.transaction,'duplicate attributed charge counted once')
const mixed=summarizeInFlightPayments([...inflightRows,inFlightCase('eur','waiting',{type:'4',status:'2',trade_amount:'0.000001',trade_currency:'EUR',bill_amount:'0.10',bill_currency:'EUR'})])
assert.deepEqual(mixed.transaction,[{amount:'0.000001',currency:'EUR'},{amount:'200.20',currency:'PHP'}])
assert.deepEqual(mixed.settlement,[{amount:'0.10',currency:'EUR'},{amount:'2.40',currency:'USD'}])
for(const extra of [{trade_amount:''},{trade_amount:'NaN'},{trade_amount:'-1'},{trade_currency:''}]){
 const total=summarizeInFlightPayments([inFlightCase('missing','opened',extra)])
 assert.equal(total.missingTransaction,1);assert.deepEqual(total.transaction,[])
 assert.equal(total.missingSettlement,0);assert.deepEqual(total.settlement,[{amount:'1.20',currency:'USD'}])
}
const noBill=summarizeInFlightPayments([inFlightCase('no-bill','waiting',{bill_amount:''})])
assert.equal(noBill.missingSettlement,1);assert.equal(noBill.missingTransaction,0)
for(const row of [{id:'no-charge',state:'opened'},inFlightCase('failed','opened',{status:'3'}),inFlightCase('refund','waiting',{type:'5'}),inFlightCase('pending-clearing','opened',{type:'4',status:'1'})]){
 const total=summarizeInFlightPayments([row])
 assert.equal(total.count,1);assert.equal(total.missingTransaction,1);assert.equal(total.missingSettlement,1)
 assert.deepEqual(total.transaction,[]);assert.deepEqual(total.settlement,[])
}
const zero=summarizeInFlightPayments([inFlightCase('zero','opened',{card_amount:'0',trade_amount:'0',bill_amount:'0'})])
assert.deepEqual(zero.transaction,[{amount:'0.00',currency:'PHP'}]);assert.equal(zero.missingTransaction,0)
assert.equal(summarizeInFlightPayments().count,0)
assert.equal(summarizeInFlightPayments(inflightRows.map(r=>({...r,state:'returned'}))).count,0,'returned cases immediately leave in-flight totals')
console.log('Passed: in-flight states, separate transaction/settlement currencies, exact decimals, duplicate payments, missing/failed evidence and returned exclusion')

// Real issuer authorization shape: cardAmount is occupied while the other
// two amount fields are zero placeholders until settlement.
const heldRows=['a','b','c'].map(id=>inFlightCase(id,'waiting',{card_amount:'5794.64',card_currency:'PHP',trade_amount:'0',bill_amount:'0'}))
const held=summarizeInFlightPayments(heldRows)
const openingUSD={id:'opening',card_id:'c',method:'open',status:'completed',result:{cardAmount:'13000',cardCurrency:'PHP',billAmount:'208.65',billCurrency:'USD'}}
const heldUSD=summarizeInFlightPayments(heldRows,[],[openingUSD])
assert.deepEqual(heldUSD.paymentUSD,{amount:'279.01',known:3,estimated:3,missing:0,total:3},'unsettled zero billAmount must not turn real card holds into zero USD')
assert.equal(heldUSD.pendingSettlement,3,'estimated USD does not change settlement state')
assert.deepEqual(summarizeInFlightPayments([...heldRows,heldRows[0],{...heldRows[0],id:'duplicate-case'}],[],[openingUSD]).paymentUSD,heldUSD.paymentUSD)
assert.equal(summarizeInFlightPayments(heldRows).paymentUSD.amount,null)
assert.equal(summarizeInFlightPayments(heldRows).paymentUSD.missing,3)
const otherOpening={...openingUSD,id:'other-open',card_id:'other',result:{...openingUSD.result,billAmount:'260'}}
assert.equal(summarizeInFlightPayments([heldRows[0],{...heldRows[0],id:'other-case',ejh_card_id:'other'}],[],[openingUSD,otherOpening]).paymentUSD.amount,'208.90')
assert.equal(summarizeInFlightPayments([{id:'unknown',state:'waiting'},heldRows[0]],[],[openingUSD]).paymentUSD.missing,1)
assert.equal(summarizeInFlightPayments([inFlightCase('bad','waiting',{card_amount:'-1',card_currency:'USD'})]).paymentUSD.amount,null)
assert.equal(summarizeInFlightPayments([inFlightCase('zero-usd','waiting',{card_amount:'0',card_currency:'USD'})]).paymentUSD.amount,'0.00')
assert.equal(summarizeInFlightPayments(heldRows.map(row=>({...row,state:'returned'})),[],[openingUSD]).paymentUSD.total,0)
assert.equal(held.count,3)
assert.deepEqual(held.payment,[{amount:'17383.92',currency:'PHP'}])
assert.deepEqual(held.settlement,[])
assert.equal(held.pendingSettlement,3)
assert.equal(held.missingPayment,0)
assert.equal(held.missingSettlement,0)
assert.deepEqual(summarizeInFlightPayments([...heldRows,heldRows[0],{...heldRows[0],id:'same-payment'}]).payment,held.payment)
assert.deepEqual(summarizeInFlightPayments(heldRows.map(row=>({...row,state:'returned'}))).payment,[])
const partlySettled=summarizeInFlightPayments([...heldRows,inFlightCase('settled','opened',{type:'4',status:'2',card_amount:'100',card_currency:'EUR',bill_amount:'108'})])
assert.deepEqual(partlySettled.payment,[{amount:'100.00',currency:'EUR'},{amount:'17383.92',currency:'PHP'}])
assert.deepEqual(partlySettled.settlement,[{amount:'108.00',currency:'USD'}])
assert.equal(partlySettled.pendingSettlement,3)
const missingCard=summarizeInFlightPayments([inFlightCase('missing-card','opened',{card_amount:''})])
assert.equal(missingCard.missingPayment,1)
assert.deepEqual(missingCard.payment,[],'never substitute transaction or settlement units for missing card payment')
const inconsistentClearing=summarizeInFlightPayments([inFlightCase('inconsistent','waiting',{type:'4',status:'2',bill_amount:'0'})])
assert.equal(inconsistentClearing.missingSettlement,1)
assert.deepEqual(inconsistentClearing.settlement,[])
assert.deepEqual(zero.payment,[{amount:'0.00',currency:'JPY'}])
assert.deepEqual(zero.settlement,[{amount:'0.00',currency:'USD'}],'true zero payment remains zero')
assert.equal(zero.pendingSettlement,0)
console.log('Passed: real authorization hold with zero issuer placeholders, mixed settlements, card currencies, missing amounts and true zeros')
