import { applyPaymentPolicies } from '../src/payment-only.js'
import { applyRefundFeeOverrides } from '../src/ejh-refund-cost.js'
import { sortCardsByReturnState } from '../src/card-workflow-sort.js'
import {cardOrigin} from '../src/card-platform.js'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import { ref, reactive, computed, watch } from 'vue'
import * as ejh from '../src/ejh.js'
import { createEJHRefreshGate } from '../src/ejhRefresh.js'
import { createLatestRequest } from '../src/latestRequest.js'
import { activationPlan } from '../src/proActivationPlan.js'

const paid = (id, plan, created_at, payment_status = 'success') => ({ id, plan, created_at, payment_status })
const account = { current_plan_type:'free', financial_cases:[paid('old','pro5','2026-10-01'),paid('paid','pro20','2026-10-02'),paid('retry','pro50','2026-10-03','failed'),paid('points','codex_point','2026-10-04')] }
assert.equal(activationPlan(account).label,'Pro 20x')
assert.equal(activationPlan({...account,plan_code:'pro50',current_plan_type:'plus'}).label,'Pro 20x')
assert.equal(activationPlan({current_plan_type:'pro',financial_cases:[]}).label,'未开通')
assert.equal(activationPlan({financial_cases:[paid('missing','','2026-10-01')]}).label,'套餐未记录')
assert.equal(activationPlan({financial_cases:[paid('cmsnav','pro5x','2026-10-01')]}).label,'Pro 5x')

const code = readFileSync(new URL('../src/components/EJHManagementView.vue',import.meta.url),'utf8').split('<script setup>')[1].split('</script>')[0].replace(/^import .*$/gm,'')
const tick = () => new Promise(resolve=>setImmediate(resolve))
let time=100000
const saved = new Map(), storage={getItem:key=>saved.get(key)??null,setItem:(key,value)=>saved.set(key,value)}
const gate=createEJHRefreshGate({now:()=>time,storage:()=>storage})
function fixture({enabled=true,defaultPageSize=10,failSync=false,holdSync=false,confirmDelete=true,deleteResult={deleted:1,deleted_ids:['unopened']},savedCases=[],savedCards=[],failURLs=new Set(),holdDashboard=false,holdSettings=false,failDashboard=false}={}) {
 const calls=[],mounts=[],unmounts=[],confirmations=[]
 let release,savedPercent=6,failSave=false
 const blocked=new Promise(resolve=>{release=resolve})
 const context=vm.createContext({createLatestRequest,applyPaymentPolicies,applyRefundFeeOverrides,sortCardsByReturnState,cardOrigin,...ejh,ref,reactive,computed,watch(){},nextTick:tick,onMounted:fn=>mounts.push(fn),onBeforeUnmount:fn=>unmounts.push(fn),defineProps:()=>({defaultPageSize}),defineEmits:()=>()=>{},window:{localStorage:storage,addEventListener(){},removeEventListener(){},confirm:text=>{confirmations.push(text);return confirmDelete}},ejhRefreshGate:gate,setInterval:()=>0,clearInterval(){},CreditCard:{},CircleDashed:{},Clock3:{},CheckCircle2:{},isClosedCard:()=>false,
 async api(url,options={}){
  calls.push({url,options});if(failURLs.has(url))throw Error("query failed")
  if(url==='/api/ejh/settings'){
   if(holdSettings)await blocked
   if(options.method==='PUT'){if(failSave)throw Error('save failed');savedPercent=options.body.reversal_fee_percent}
   return {enabled,reversal_fee_percent:savedPercent}
  }
  if(url==='/api/ejh/dashboard'){if(holdDashboard)await blocked;if(failDashboard)throw Error('read timed out');return {cards:savedCards,cases:savedCases,trades:[],operations:[],audit:[],sync:{},reversal_fee_percent:savedPercent}}
  if(options.method==='DELETE')return deleteResult
  if(url==='/api/ejh/sync'){if(holdSync)await blocked;if(failSync)throw Error('sync failed')}
  return {}
 }})
 context.watch=watch
 vm.runInContext(code+';globalThis.exposed={retryFailedRefresh,progress,initialize,refreshPage,refreshAllBalances,busy,message,balanceFailures,editLimit,saveLimit,data,limitID,limitValue,lifecycleFilter,filtered,setLifecycle,clearFilters,cardStatusFilter,targetCardID,changeTab,showCaseState,tab,status,query,accountReturnStates,caseCategory,caseCounts,setCaseCategory,deleteUnopenedCase,page,pageSize,visible,settings,save,load,ready,loading,initializing,initialError,pollSavedData};',context)
 return {...context.exposed,calls,confirmations,release,setFailSync:value=>{failSync=value},setFailSave:value=>{failSave=value},unmount:()=>unmounts.forEach(fn=>fn())}
}
let f=fixture();await f.initialize()
assert.equal(fixture({defaultPageSize:50}).pageSize.value,50,'EJH lists must use the global page size')
assert.equal(f.calls.filter(r=>r.url==='/api/ejh/sync').length,1)
f.unmount()
let second=fixture();await second.initialize()
assert.equal(second.calls.filter(r=>r.options.method).length,0,'remount repeated supplier refresh')
await second.refreshPage()
assert.equal(second.calls.filter(r=>r.url==='/api/ejh/sync').length,1,'manual retry was throttled')
second.unmount()
time+=30000
f=fixture({holdSync:true});const original=f.initialize();await tick();f.unmount()
second=fixture();const remount=second.initialize();await tick()
assert.equal(second.calls.filter(r=>r.options.method).length,0,'in-flight refresh duplicated')
f.release();await original;await remount
assert.equal(f.calls.filter(r=>r.url==='/api/ejh/balance').length,0,'unmount started another supplier request')
second.unmount()
time+=30000
f=fixture({failSync:true});await f.initialize();assert.equal(f.balanceFailures.value.length,1);f.unmount()
second=fixture();await second.initialize();assert.equal(second.calls.filter(r=>r.options.method).length,0)
await second.refreshPage();assert.equal(second.calls.filter(r=>r.url==='/api/ejh/sync').length,1)
second.unmount()
assert.equal(createEJHRefreshGate({now:()=>time,storage:()=>storage}).begin(true).reason,'cooldown','reload lost cooldown')
time+=30000
const renewed=createEJHRefreshGate({now:()=>time,storage:()=>storage}).begin(true)
assert.equal(renewed.reason,undefined);renewed.finish()
const noStorage=createEJHRefreshGate({now:()=>time,storage:()=>{throw Error('blocked storage')}})
const lease=noStorage.begin(true);lease.finish();assert.equal(noStorage.begin(true).reason,'cooldown')

f=fixture();f.data.value.cards=[{id:'card1',local:{max_accounts:2},remote:{}}]
f.editLimit(f.data.value.cards[0]);assert.equal(f.limitValue.value,2)
f.limitValue.value='';await f.saveLimit();assert.equal(f.calls.length,0)
f.limitValue.value=4;await f.saveLimit()
assert.deepEqual(JSON.parse(JSON.stringify(f.calls[0])),{url:'/api/gptpay/cards/card1',options:{method:'PUT',body:{max_accounts:4}}})
assert.equal(f.limitID.value,'')
f.unmount()
console.log('Passed: saved activation plans, remount cooldown, manual retry, failed/interrupted/in-flight refresh, per-card limit form')
f=fixture()
f.data.value.cards=['1','2','5','8'].map(state=>({id:state,remote:{status:state},local:{}}))
assert.equal(f.lifecycleFilter.value,'normal')
assert.deepEqual(Array.from(f.filtered.value,c=>c.id),['1','5'])
f.setLifecycle('closed');assert.deepEqual(Array.from(f.filtered.value,c=>c.id),['8','2'])
f.cardStatusFilter.value='5';assert.equal(f.lifecycleFilter.value,'normal');assert.equal(f.filtered.value.length,1)
f.cardStatusFilter.value='2';assert.equal(f.lifecycleFilter.value,'closed');assert.equal(f.filtered.value[0].id,'2')
f.clearFilters();assert.equal(f.lifecycleFilter.value,'normal')
f.targetCardID.value='8';assert.equal(f.lifecycleFilter.value,'closed');assert.equal(f.filtered.value[0].id,'8')
f.setLifecycle('normal');assert.equal(f.targetCardID.value,'');assert.equal(f.cardStatusFilter.value,'')
f.unmount()
console.log('Passed: normal default, closed/closing category, detailed filter changes, reset and exact-card navigation')
f=fixture()
f.data.value.cases=['unopened','opened','waiting','returned'].map(state=>({id:state,email:state+'@example.com',state}))
f.changeTab('cases')
assert.deepEqual(Array.from(f.filtered.value,c=>c.id),['opened','waiting','returned'])
assert.equal(Object.hasOwn(f.accountReturnStates,'unopened'),false)
f.query.value='unopened@example.com';assert.equal(f.filtered.value.length,0)
f.clearFilters();assert.equal(f.filtered.value.length,3)
f.showCaseState('unopened');assert.equal(f.tab.value,'cases');assert.equal(f.caseCategory.value,'unopened');assert.equal(f.filtered.value[0].id,'unopened')
assert.equal(f.caseCounts.value.normal,3);assert.equal(f.caseCounts.value.unopened,1)
f.status.value='waiting';f.page.value=5;f.setCaseCategory('normal')
assert.equal(f.status.value,'');assert.equal(f.page.value,1);assert.equal(f.filtered.value.length,3)
f.showCaseState('waiting');assert.equal(f.tab.value,'cases');assert.equal(f.filtered.value[0].id,'waiting')
f.changeTab('cards');f.changeTab('cases');assert.equal(f.caseCategory.value,'normal');assert.equal(f.filtered.value.length,3)
f.unmount()
console.log('Passed: normal returns default, separate unopened category, counts, search, summary navigation and category reset')

const unopenedRows=Array.from({length:12},(_,i)=>({id:'unopened-'+i,email:'account-'+i+'@example.com',state:'unopened'}))
const normalRow={id:'normal',email:'normal@example.com',state:'opened'}
f=fixture({deleteResult:{deleted:12,deleted_ids:unopenedRows.map(c=>c.id)},savedCases:[normalRow]})
f.data.value.cases=[...unopenedRows,normalRow];f.changeTab('cases');f.setCaseCategory('unopened')
f.query.value='account-11@example.com'
assert.equal(f.filtered.value.length,1)
await f.deleteUnopenedCase()
assert.match(f.confirmations[0],/全部 12 条.*其他分页和搜索范围外/)
assert.deepEqual(JSON.parse(JSON.stringify(f.calls[0])),{url:'/api/ejh/cases/unopened',options:{method:'DELETE',body:{confirm:true}}})
assert.equal(f.caseCounts.value.unopened,0);assert.equal(f.caseCounts.value.normal,1)
assert.match(f.message.text,/已删除 12 条/)
f.unmount()
f=fixture({deleteResult:{deleted:1,deleted_ids:['unopened-10']},savedCases:unopenedRows.slice(0,10)})
f.data.value.cases=unopenedRows.slice(0,11);f.changeTab('cases');f.setCaseCategory('unopened');f.page.value=2
await f.deleteUnopenedCase(f.visible.value[0])
assert.equal(f.calls[0].url,'/api/ejh/cases/unopened-10');assert.equal(f.page.value,1)
assert.match(f.confirmations[0],/account-10@example.com.*unopened-10/)
f.unmount()
f=fixture({confirmDelete:false});f.data.value.cases=unopenedRows;await f.deleteUnopenedCase()
assert.equal(f.calls.length,0);assert.equal(f.data.value.cases.length,12);f.unmount()
f=fixture();f.data.value.cases=unopenedRows;f.busy.value=true;await f.deleteUnopenedCase();f.busy.value=false;await f.deleteUnopenedCase(normalRow)
assert.equal(f.calls.length,0);assert.equal(f.confirmations.length,0);f.unmount()
console.log('Passed: delete all across search/pages, single deletion, last-page clamping, confirmation cancellation and busy/state guards')

{
 const f=fixture();await f.initialize()
 assert.equal(f.data.value.reversal_fee_percent,6)
 f.settings.reversal_fee_percent=8
 assert.equal(f.data.value.reversal_fee_percent,6,'unsaved draft must not recalculate fee displays')
 await f.load()
 assert.equal(f.settings.reversal_fee_percent,8,'polling must retain the settings draft')
 assert.equal(f.data.value.reversal_fee_percent,6)
 f.setFailSave(true);await f.save()
 assert.equal(f.data.value.reversal_fee_percent,6,'failed save must not change displayed fees')
 assert.equal(f.settings.reversal_fee_percent,8)
 f.setFailSave(false);await f.save()
 assert.equal(f.data.value.reversal_fee_percent,8)
 f.settings.reversal_fee_percent=0;await f.save();await f.load()
 assert.equal(f.data.value.reversal_fee_percent,0,'explicit zero survives saving and refresh')
 f.unmount()
}
console.log('Passed: saved reversal fee rate, draft isolation, refresh retention, save failure and zero rate')

let configuredNow=0
const configuredGate=createEJHRefreshGate({now:()=>configuredNow,storage:()=>null})
configuredGate.begin(true,120).finish()
configuredNow=31000
assert.equal(configuredGate.begin(true,120).reason,'cooldown','configured interval was ignored')
const manual=configuredGate.begin(false,120);assert.equal(manual.reason,undefined);manual.finish()
configuredNow+=120000
const expired=configuredGate.begin(true,120);assert.equal(expired.reason,undefined);expired.finish()
configuredNow+=6000
const shorter=configuredGate.begin(true,5);assert.equal(shorter.reason,undefined);shorter.finish()
console.log('Passed: configurable refresh interval and immediate manual refresh')

// A disabled issuer must not refresh on entry or claim a refresh cooldown.
time+=30000
f=fixture({enabled:false});await f.initialize()
assert.equal(f.calls.filter(r=>r.options.method).length,0,'disabled issuer auto queried')
await f.refreshPage(true)
assert.equal(f.calls.filter(r=>r.options.method).length,0,'automatic refresh bypassed switch')
await f.refreshPage()
assert.equal(f.calls.filter(r=>r.url==='/api/ejh/sync').length,1,'disabled manual sync blocked')
assert.equal(f.calls.filter(r=>r.url==='/api/ejh/balance').length,1,'disabled manual balance blocked')
f.unmount()
console.log('Passed: disabled EJH skips automatic refresh and permits manual refresh')

// Reading saved data is independent of settings and slow supplier refreshes.
time+=30000
f=fixture({holdSettings:true,savedCases:[normalRow]})
let pending=f.initialize();await tick()
assert.equal(f.data.value.cases.length,1,'slow settings hid available dashboard')
assert.equal(f.busy.value,false,'initial reads locked card operations')
assert.equal(f.calls.find(c=>c.url==='/api/ejh/dashboard').options.timeoutMs,15000)
f.release();await pending;f.unmount()
time+=30000
f=fixture({holdSync:true,savedCases:[normalRow]})
pending=f.initialize();await tick()
assert.equal(f.ready.value,true);assert.equal(f.data.value.cases.length,1)
const before=f.calls.filter(c=>c.url==='/api/ejh/dashboard').length
f.pollSavedData();await tick()
assert.equal(f.calls.filter(c=>c.url==='/api/ejh/dashboard').length,before+1,'supplier refresh stopped saved-data polling')
f.release();await pending;f.unmount()
f=fixture({enabled:false,holdDashboard:true})
pending=f.initialize();await tick()
const reads=Array.from({length:20},()=>f.load({background:true}));await tick()
assert.equal(f.calls.filter(c=>c.url==='/api/ejh/dashboard').length,1,'slow dashboard produced overlapping polls')
const signal=f.calls.find(c=>c.url==='/api/ejh/dashboard').options.signal
f.unmount();assert.equal(signal.aborted,true)
f.release();await pending;await Promise.all(reads)
assert.equal(f.data.value.cases.length,0)
f=fixture({enabled:false,failDashboard:true});await f.initialize()
assert.match(f.initialError.value,/read timed out/)
assert.equal(f.initializing.value,false);assert.equal(f.busy.value,false)
assert.equal(f.ready.value,false);f.unmount()
console.log('Passed: independent first load, visible data during sync, no overlapping polling, bounded reads and unmount cancellation')

// Failed queries retry independently, with the same reentry and stop guards.
time+=30000
f=fixture({failSync:true});await f.initialize()
let start=f.calls.length
await f.retryFailedRefresh()
assert.equal(f.balanceFailures.value.length,1,'failed retry must remain visible')
assert.deepEqual(f.calls.slice(start).filter(c=>c.options.method).map(c=>c.url),['/api/ejh/sync'])
f.setFailSync(false);start=f.calls.length;await f.retryFailedRefresh()
assert.equal(f.balanceFailures.value.length,0)
assert.deepEqual(f.calls.slice(start).filter(c=>c.options.method).map(c=>c.url),['/api/ejh/sync'])
f.unmount()
time+=30000
const failURLs=new Set(['/api/ejh/cards/b/action'])
f=fixture({savedCards:['a','b','c'].map(id=>({id,remote:{orderNo:id,status:'1'},local:{}})),failURLs})
await f.initialize();assert.equal(f.balanceFailures.value.length,1)
failURLs.clear();start=f.calls.length;await f.retryFailedRefresh()
assert.deepEqual(f.calls.slice(start).filter(c=>c.options.method).map(c=>c.url),['/api/ejh/cards/b/action'])
assert.equal(f.balanceFailures.value.length,0);f.unmount()
time+=30000
f=fixture({failSync:true,failURLs:new Set(['/api/ejh/balance'])});await f.initialize()
f.setFailSync(false);f.calls.length=0
const retry=f.retryFailedRefresh();f.progress.value.stopRequested=true;await f.retryFailedRefresh();await retry
assert.equal(f.calls.filter(c=>c.options.method).length,1,'overlap or stop dispatched more queries')
assert.equal(f.balanceFailures.value.length,1,'unattempted item must remain')
assert.equal(f.balanceFailures.value[0].kind,'account_balance')
f.unmount();start=f.calls.length;await f.retryFailedRefresh();assert.equal(f.calls.length,start)
f=fixture();f.data.value.cards=[{id:'pending',normal_pending:true,remote:{status:'1'},local:{}}]
f.cardStatusFilter.value='normal_pending';assert.equal(f.lifecycleFilter.value,'normal_pending');assert.equal(f.filtered.value[0].id,'pending');f.unmount()
const overview=readFileSync(new URL('../src/components/EJHBalanceOverview.vue',import.meta.url),'utf8')
assert.ok(overview.indexOf('@click="emit(\'retry-failed\')"')<overview.indexOf('@click="emit(\'refresh\')"'))
assert.ok(code.includes('async function retryFailedRefresh'))
console.log('Passed: targeted failure retry, retained failures, stop/unmount/reentry guards and pending filter')
