import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import { webcrypto } from 'node:crypto'
import { ref, reactive, computed, watch } from 'vue'
import * as helpers from '../src/nexovcc.js'
import * as finance from '../src/nexovcc-finance.js'
import { cardUsage } from '../src/card-status.js'

const code=readFileSync(new URL('../src/components/NexovccManagementView.vue',import.meta.url),'utf8').split('<script setup>')[1].split('</script>')[0].replace(/^import .*$/gm,'')
for(const action of ['withdraw_all','cancel','withdraw_cancel']){
 const requests=[];let polls=0
 const rows=[]
 const context=vm.createContext({...helpers,...finance,ref,reactive,computed,watch,crypto:webcrypto,
  defineProps:()=>({defaultPageSize:10}),defineEmits:()=>()=>{},onMounted(){},onBeforeUnmount(){},clearTimeout(){},setTimeout:fn=>queueMicrotask(fn),formatTime:v=>v,
  async api(url,options={}){
   if(url==='/api/nexovcc/dashboard')return {cards:rows,cases:[],operations:[],products:[],balance:{},groups:[],finance:{}}
   if(url==='/api/nexovcc/job'){polls++;return {id:'batch-job',status:polls===1?'running':'completed',card_total:3,card_done:polls===1?1:3,message:polls===1?'冻结成功 · 等待 3 秒':'已处理 3/3 张'}}
   if(url!=='/api/nexovcc/run')throw Error(url)
   const body=JSON.parse(JSON.stringify(options.body));requests.push(body)
   assert.equal(context.view.running.value,true)
   return {id:'batch-job',status:'running',card_total:3,card_done:0,message:'正在处理第一张卡'}
  }
 })
 vm.runInContext(code+';globalThis.view={batchCards,message,job,running,cardAction,submit,dialog,resolve};',context)
 await context.view.batchCards({confirm:true,ids:['a','b','c'],action})
 assert.equal(requests.length,1,'batch must execute on server')
 assert.deepEqual(requests[0].card_ids,['a','b','c'])
 assert.equal(requests[0].action,action)
 assert.equal(requests[0].amount,undefined,'amount must use fresh server balance')
 assert.equal(requests[0].confirm,true)
 assert.equal(polls,2)
 assert.equal(context.view.job.value.card_done,3)
 assert.equal(context.view.running.value,false)
 assert.equal(context.view.message.text,'已处理 3/3 张')
 polls=0
 context.view.cardAction({card:{id:'one',last4:'4242',remark:''}},action)
 context.view.dialog.value.confirm=true
 await context.view.submit()
 assert.equal(requests.at(-1).card_id,'one')
 assert.equal(requests.at(-1).action,action)
 polls=0
 await context.view.resolve({id:'pending-close',status:'processing',action:'withdraw_cancel'})
 assert.equal(context.view.dialog.value,null,'accepted operations query progress without manual confirmation')
 assert.equal(requests.at(-1).action,'resolve')
 assert.equal(requests.at(-1).outcome,'query')
 assert.equal(requests.at(-1).request_id,'pending-close')
 const before=requests.length
 context.view.resolve({id:'uncertain-close',status:'unknown',action:'withdraw_cancel'})
 assert.equal(requests.length,before,'uncertain operations must retain explicit resolution')
 assert.equal(context.view.dialog.value.type,'resolve')
}
const frozen={card:{id:'f',status:'1',control:'frozen',imported:true},local:{source:'nexovcc',issuer_status:'1',issuer_frozen:true,issuer_locked:true}}
assert.equal(helpers.cardState(frozen),'已冻结')
assert.equal(helpers.cancelReason(frozen,[]),'','confirmed freeze must allow cancellation')
assert.equal(cardUsage(frozen).label,'已冻结')
assert.equal(cardUsage({...frozen.local,issuer_status:'4'}).label,'已销卡')
assert.ok(helpers.cancelReason(frozen,[{card_id:'f',status:'unknown'}]))
console.log('Passed: single/batch actions, server-side live balance, immediate progress, batch polling, freeze display and uncertainty guard')

{
 const calls=[],mounts=[],timers=[]
 const context=vm.createContext({...helpers,...finance,ref,reactive,computed,watch,crypto:webcrypto,localStorage:{getItem:()=>null},
  defineProps:()=>({defaultPageSize:10}),defineEmits:()=>()=>{},onMounted:fn=>mounts.push(fn),onBeforeUnmount(){},clearTimeout(){},setTimeout:(fn,ms)=>{if(ms===1000)queueMicrotask(fn);else timers.push(fn)},formatTime:v=>v,
  async api(url,options={}){
   calls.push({url,options})
   if(url==='/api/nexovcc/settings')return {enabled:false,page_refresh_seconds:30,templates:[]}
   if(url==='/api/nexovcc/dashboard')return {cards:[],cases:[],operations:[],products:[],balance:{amount:'123.45'},groups:[],finance:{}}
   if(url==='/api/nexovcc/job')return {id:'manual-job',status:'completed',message:'查询完成'}
   if(url==='/api/nexovcc/run')return {id:'manual-job',status:'running'}
   throw Error(url)
  }
 })
 vm.runInContext(code+';globalThis.view={perform,watchUpdates,savedEnabled};',context)
 for(const fn of mounts)await fn()
 assert.equal(context.view.savedEnabled.value,false)
 assert.equal(calls.some(r=>r.options.method),false,'disabled issuer refreshed on entry')
 const before=calls.length
 await context.view.watchUpdates()
 assert.equal(calls.length,before,'disabled issuer continued periodic polling')
 await context.view.perform({action:'balance'})
 assert.equal(calls.filter(r=>r.url==='/api/nexovcc/run').length,1,'manual query blocked')
 assert.equal(calls.find(r=>r.url==='/api/nexovcc/run').options.body.action,'balance')
 assert.equal(calls.filter(r=>r.url==='/api/nexovcc/job').length,1,'manual progress not polled')
 console.log('Passed: disabled Global skips automatic requests; manual query and progress remain available')
}

for (const pageAutoRefresh of [true, false]) {
 const calls=[],mounts=[]
 const context=vm.createContext({...helpers,...finance,ref,reactive,computed,watch,crypto:webcrypto,localStorage:{getItem:()=>null},
  defineProps:()=>({defaultPageSize:10}),defineEmits:()=>()=>{},onMounted:fn=>mounts.push(fn),onBeforeUnmount(){},clearTimeout(){},setTimeout(){},formatTime:v=>v,
  async api(url,options={}){
   calls.push({url,options})
   if(url==='/api/nexovcc/settings')return {enabled:true,page_auto_refresh:pageAutoRefresh,templates:[]}
   if(url==='/api/nexovcc/dashboard')return {cards:[{card:{id:'one',serial_no:'serial-one'}}],cases:[],operations:[],products:[],balance:{},groups:[],finance:{}}
   if(url==='/api/nexovcc/job')return {id:'job',status:'completed'}
   if(url==='/api/nexovcc/run')return {id:'job',status:'completed'}
   throw Error(url)
  }
 })
 vm.runInContext(code+';globalThis.view={perform,syncCardTransactions,watchUpdates};',context)
 for(const fn of mounts)await fn()
 let runs=calls.filter(r=>r.url==='/api/nexovcc/run')
 assert.equal(runs.length,pageAutoRefresh?1:0)
 if(pageAutoRefresh)assert.equal(runs[0].options.body.automatic_refresh,true)
 await context.view.watchUpdates()
 assert.equal(calls.filter(r=>r.url==='/api/nexovcc/run').length,runs.length,'local progress polling triggered upstream refresh')
 await context.view.perform({action:'sync'})
 runs=calls.filter(r=>r.url==='/api/nexovcc/run')
 assert.equal(runs.at(-1).options.body.action,'sync')
 assert.equal(runs.at(-1).options.body.automatic_refresh,undefined)
 await context.view.syncCardTransactions('serial-one')
 runs=calls.filter(r=>r.url==='/api/nexovcc/run')
 assert.equal(runs.at(-1).options.body.action,'card_finance')
 assert.equal(runs.at(-1).options.body.card_id,'one')
 const before=runs.length
 await context.view.syncCardTransactions('missing')
 assert.equal(calls.filter(r=>r.url==='/api/nexovcc/run').length,before)
}
console.log('Passed: page refresh opt-out, manual full refresh, exact-card transaction refresh, invalid card does not broaden scope')

{
 const context=vm.createContext({...helpers,...finance,ref,reactive,computed,watch,
  defineProps:()=>({defaultPageSize:10}),defineEmits:()=>()=>{},onMounted(){},onBeforeUnmount(){},
 })
 vm.runInContext(code+';globalThis.view={data,tab,page,search,filtered,withdrawAttention,withdrawAttentionOnly,showWithdrawAttention};',context)
 const v=context.view
 v.data.operations=[{id:'old-failure',auto_withdraw:true,status:'failed'},{id:'pending',auto_withdraw:true,status:'unknown'},{id:'manual-pending',status:'unknown'}]
 v.data.withdraw={blocked:9}
 assert.equal(v.withdrawAttention.value.length,1,'historical cached count leaked into notice')
 v.search.value='unrelated';v.page.value=3
 v.showWithdrawAttention()
 assert.equal(v.tab.value,'operations')
 assert.equal(v.page.value,1)
 assert.equal(v.search.value,'')
 assert.deepEqual(Array.from(v.filtered.value,r=>r.id),['pending'])
 v.data.operations[1].status='succeeded'
 assert.equal(v.withdrawAttention.value.length,0)
 assert.equal(v.filtered.value.length,0)
 v.withdrawAttentionOnly.value=false
 assert.equal(v.filtered.value.length,3,'resolved/failed records must remain in history')
 console.log('Passed: only unknown automatic withdrawals show a notice; click locates them; resolution clears it without deleting history')
}
