import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import { ref, reactive, computed, watch } from 'vue'
import * as ejh from '../src/ejh.js'
import { createEJHRefreshGate } from '../src/ejhRefresh.js'
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
function fixture({defaultPageSize=10,failSync=false,holdSync=false,confirmDelete=true,deleteResult={deleted:1,deleted_ids:['unopened']},savedCases=[]}={}) {
 const calls=[],mounts=[],unmounts=[],confirmations=[]
 let release,savedPercent=6,failSave=false
 const blocked=new Promise(resolve=>{release=resolve})
 const context=vm.createContext({...ejh,ref,reactive,computed,watch(){},nextTick:tick,onMounted:fn=>mounts.push(fn),onBeforeUnmount:fn=>unmounts.push(fn),defineProps:()=>({defaultPageSize}),defineEmits:()=>()=>{},window:{localStorage:storage,addEventListener(){},removeEventListener(){},confirm:text=>{confirmations.push(text);return confirmDelete}},ejhRefreshGate:gate,setInterval:()=>0,clearInterval(){},CreditCard:{},CircleDashed:{},Clock3:{},CheckCircle2:{},isClosedCard:()=>false,
 async api(url,options={}){
  calls.push({url,options})
  if(url==='/api/ejh/settings'){
   if(options.method==='PUT'){if(failSave)throw Error('save failed');savedPercent=options.body.reversal_fee_percent}
   return {enabled:true,reversal_fee_percent:savedPercent}
  }
  if(url==='/api/ejh/dashboard')return {cards:[],cases:savedCases,trades:[],operations:[],audit:[],sync:{},reversal_fee_percent:savedPercent}
  if(options.method==='DELETE')return deleteResult
  if(url==='/api/ejh/sync'){if(holdSync)await blocked;if(failSync)throw Error('sync failed')}
  return {}
 }})
 context.watch=watch
 vm.runInContext(code+';globalThis.exposed={initialize,refreshPage,refreshAllBalances,busy,message,balanceFailures,editLimit,saveLimit,data,limitID,limitValue,lifecycleFilter,filtered,setLifecycle,clearFilters,cardStatusFilter,targetCardID,changeTab,showCaseState,tab,status,query,accountReturnStates,caseCategory,caseCounts,setCaseCategory,deleteUnopenedCase,page,pageSize,visible,settings,save,load};',context)
 return {...context.exposed,calls,confirmations,release,setFailSave:value=>{failSave=value},unmount:()=>unmounts.forEach(fn=>fn())}
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
f.setLifecycle('closed');assert.deepEqual(Array.from(f.filtered.value,c=>c.id),['2','8'])
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
