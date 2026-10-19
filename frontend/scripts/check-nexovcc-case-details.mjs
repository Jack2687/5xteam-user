import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import { ref, reactive, computed, watch } from 'vue'
import * as nexovcc from '../src/nexovcc.js'
import * as financeHelpers from '../src/nexovcc-finance.js'
import { caseDetails, sortReturnCases } from '../src/nexovcc-case-details.js'

const raw=JSON.parse(readFileSync(new URL('../../internal/nexovcc/testdata/finance.json',import.meta.url),'utf8'))
const finance={at:new Date().toISOString(),
 funds:raw['/api/findLogWebFunds'].list.map((r,i)=>({id:'f'+i,kind:r.change_type_id,currency:'USD',serial_no:r.serial_no,amount:r.amount,link:r.link_log_no})),
 trades:raw['/api/findLogCardTrades'].list.map(r=>({id:r.log_no,serial_no:r.serial_no,status:r.status,amount:r.trade_amount,currency:'USD',merchant_amount:r.merchant_amount,merchant_currency:r.merchant_currency,at:r.created_at}))}
const row={id:'returned',email:'return@example.com',state:'returned',payment_status:'success',card_id:'card1',revenue_r:20,match:{method:'auto',group:{serial_no:'fixture-card-1',valid:true,fully_returned:true,payment:'92.33',returned:'92.28',difference:'0.05',fees:'0.80',cost:'0.85',returned_at:'2026-10-18T23:16:43+08:00',trade_ids:['fixture-log-1','fixture-log-2']}}}
let d=caseDetails(row,finance)
assert.equal(d.reversal,50)
assert.equal(d.transaction,30)
assert.equal(d.refundFee,0)
assert.equal(d.otherFees,0)
assert.equal(d.cardLoss,5)
assert.equal(d.settlement,0)
assert.equal(d.pending,0)
assert.equal(d.cost,85) // opening fee belongs to the card, not this order
assert.equal(d.profit,1430)
assert.deepEqual(d.merchantPaid,[{currency:'PHP',amount:579464}])
assert.deepEqual(d.merchantReturned,[{currency:'PHP',amount:579464}])
assert.equal(d.manualUnconfirmed,false)
const unrelated=structuredClone(finance)
unrelated.funds.push({kind:2003,currency:'USD',serial_no:'other-card',link:'fixture-log-1',amount:'-99.00'})
assert.equal(caseDetails(row,unrelated).reversal,50) // link alone isn't unique across cards
const rebate=structuredClone(finance)
rebate.funds.push({kind:5004,currency:'USD',serial_no:'fixture-card-1',link:'fixture-log-1',amount:'0.20'})
const rebated=structuredClone(row);Object.assign(rebated.match.group,{fees:'0.60',cost:'0.65'})
assert.equal(caseDetails(rebated,rebate).reversal,30)
assert.equal(caseDetails(row,{}).reversal,null)
const missing=structuredClone(finance);missing.funds=[]
assert.equal(caseDetails(row,missing).feesReady,false)
const waiting=structuredClone(row);waiting.state='waiting';Object.assign(waiting.match.group,{fully_returned:false,returned:'0',difference:'92.33',fees:'0.30',cost:'0.30',returned_at:'0001-01-01T00:00:00Z'})
const pendingFinance=structuredClone(finance);pendingFinance.funds=pendingFinance.funds.filter(f=>f.kind!==2003);pendingFinance.trades=pendingFinance.trades.filter(t=>t.status!=='REVERSED')
d=caseDetails(waiting,pendingFinance)
assert.equal(d.pending,9233)
assert.equal(d.cardLoss,0)
assert.equal(d.provisional,true)
assert.equal(d.returnedAt,'')
assert.equal(d.cost,30)
const manual={id:'manual',state:'returned',manual_state:'returned',updated_at:'2026-10-19T12:00:00Z'}
assert.equal(caseDetails(manual,finance).manualUnconfirmed,true)
assert.equal(caseDetails(manual,finance).returnedAt,'') // manual time is never a bank credit time
assert.equal(caseDetails(manual,finance).returned,null)
const settled=structuredClone(waiting);Object.assign(settled.match.group,{settled:true,payment:'92.33',returned:'10.00',difference:'82.33',cost:'82.63'})
assert.equal(caseDetails(settled,pendingFinance).pending,0)
assert.equal(caseDetails(settled,pendingFinance).settlement,8233)

const code=readFileSync(new URL('../src/components/NexovccManagementView.vue',import.meta.url),'utf8').split('<script setup>')[1].split('</script>')[0].replace(/^import .*$/gm,'')
const context=vm.createContext({...nexovcc,...financeHelpers,sortReturnCases,ref,reactive,computed,watch,defineProps:()=>({defaultPageSize:10}),defineEmits:()=>()=>{},onMounted(){},onBeforeUnmount(){},formatTime:v=>v})
vm.runInContext(code+';globalThis.view={data,tab,returnCategory,returnFilter,filtered,inspect,detailCases,caseTrades,ledger};',context)
const view=context.view
view.data.cards=[{card:{id:'card1',serial_no:'fixture-card-1'}}]
view.data.cases=[row,{id:'unopened',state:'unopened',email:'failed@example.com',payment_status:'failed',card_id:'card1',created_at:'2026-10-19T12:00:00Z'}, {...waiting,id:'waiting',requested_at:'2026-10-19T13:00:00Z'}]
view.tab.value='returns'
assert.deepEqual([...view.filtered.value].map(c=>c.id),['waiting','returned'])
view.returnFilter.value='returned';assert.equal(view.filtered.value.length,1)
view.returnCategory.value='unopened';assert.equal(view.filtered.value[0].id,'unopened')
view.inspect({type:'accounts',state:'waiting'},'fixture-card-1')
assert.equal(view.detailCases.value.length,1)
view.caseTrades({row,options:{type:'funds',kind:'2003'}})
assert.equal(view.ledger.value.serial,'fixture-card-1')
assert.equal(view.ledger.value.group.trade_ids.length,2)
assert.equal(view.ledger.value.kind,'2003')
console.log('Passed: per-order original currency, fee breakdown/refunds, missing data, pending vs settled costs, manual status dates, default/filter/sort and linked ledger navigation')
