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
function fixture({failSync=false,holdSync=false}={}) {
 const calls=[],mounts=[],unmounts=[]
 let release
 const blocked=new Promise(resolve=>{release=resolve})
 const context=vm.createContext({...ejh,ref,reactive,computed,watch(){},nextTick:tick,onMounted:fn=>mounts.push(fn),onBeforeUnmount:fn=>unmounts.push(fn),defineProps:()=>({}),defineEmits:()=>()=>{},window:{localStorage:storage,addEventListener(){},removeEventListener(){}},ejhRefreshGate:gate,setInterval:()=>0,clearInterval(){},CreditCard:{},CircleDashed:{},Clock3:{},CheckCircle2:{},isClosedCard:()=>false,
 async api(url,options={}){
  calls.push({url,options})
  if(url==='/api/ejh/settings')return {enabled:true}
  if(url==='/api/ejh/dashboard')return {cards:[],cases:[],trades:[],operations:[],audit:[],sync:{}}
  if(url==='/api/ejh/sync'){if(holdSync)await blocked;if(failSync)throw Error('sync failed')}
  return {}
 }})
 context.watch=watch
 vm.runInContext(code+';globalThis.exposed={initialize,refreshPage,refreshAllBalances,busy,message,balanceFailures,editLimit,saveLimit,data,limitID,limitValue,lifecycleFilter,filtered,setLifecycle,clearFilters,cardStatusFilter,targetCardID,changeTab,showCaseState,tab,status,query,accountReturnStates};',context)
 return {...context.exposed,calls,release,unmount:()=>unmounts.forEach(fn=>fn())}
}
let f=fixture();await f.initialize()
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
f.showCaseState('unopened');assert.equal(f.tab.value,'cards')
f.showCaseState('waiting');assert.equal(f.tab.value,'cases');assert.equal(f.filtered.value[0].id,'waiting')
f.unmount()
console.log('Passed: unopened accounts excluded from returns list, counts and search; unopened summary links to card management')
