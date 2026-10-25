import {cardOrigin} from '../src/card-platform.js'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import { ref, reactive, computed, watch } from 'vue'
import * as nexovcc from '../src/nexovcc.js'

// Exercise the actual view's save and submit handlers using the numeric model
// values produced by <input type="number">, then inspect JSON on the wire.
const code=readFileSync(new URL('../src/components/NexovccManagementView.vue',import.meta.url),'utf8')
 .split('<script setup>')[1].split('</script>')[0].replace(/^import .*$/gm,'')
const requests=[]
const context=vm.createContext({cardOrigin,...nexovcc,ref,reactive,computed,watch,
 defineProps:()=>({defaultPageSize:10}),defineEmits:()=>()=>{},onMounted(){},onBeforeUnmount(){},
 clearTimeout(){},setTimeout(){},formatTime:value=>value,
 async api(url,options={}){
  const body=options.body===undefined?undefined:JSON.parse(JSON.stringify(options.body))
  requests.push({url,body})
  if(url==='/api/nexovcc/settings')return body
  if(url==='/api/nexovcc/run')return {id:'test-job',status:'completed',operation:{status:'succeeded'}}
  if(url==='/api/nexovcc/dashboard')return {cards:[],cases:[],operations:[],products:[],balance:{}}
  throw Error('Unexpected API '+url)
 }
})
vm.runInContext(code+';globalThis.view={cfg,save,submit,dialog,message,data,filter,syncCategoryTransactions,refreshPendingCards};',context)
const {cfg,save,submit,dialog,message}=context.view
cfg.templates=[{id:'t1',name:'Pro备用卡',product_id:'267',holder_id:'',amount:110,fee_buffer:10}]
await save()
let payload=requests.find(r=>r.url==='/api/nexovcc/settings').body
assert.equal(payload.templates[0].amount,'110')
assert.equal(payload.templates[0].fee_buffer,'10')
assert.equal(typeof payload.default_max_accounts,'number')
assert.equal(message.text,'环球配置已保存')

cfg.templates[0].amount='110.25'
cfg.templates[0].fee_buffer=0
await save()
payload=requests.filter(r=>r.url==='/api/nexovcc/settings').at(-1).body
assert.equal(payload.templates[0].amount,'110.25')
assert.equal(payload.templates[0].fee_buffer,'0')

for(const action of ['deposit','withdraw']){
 dialog.value={type:'action',action,card:{card:{id:'fixture-card'}},amount:9.25,remark:'',request_id:'fixture-request',confirm:true}
 await submit()
 const request=requests.filter(r=>r.url==='/api/nexovcc/run').at(-1)
 assert.equal(request.body.action,action)
 assert.equal(request.body.amount,'9.25')
 assert.equal(request.body.confirm,true)
}
console.log('Passed: template save with numeric/decimal amounts, zero fee buffer, and deposit/withdraw request strings')

cfg.auto_withdraw_enabled=true
cfg.batch_interval_seconds=7
cfg.request_interval_seconds=4
cfg.templates[0].product_ids=['268','267']
await save()
payload=requests.filter(r=>r.url==='/api/nexovcc/settings').at(-1).body
assert.deepEqual(payload.templates[0].product_ids,['268','267'])
assert.equal(payload.templates[0].product_id,'268')
assert.equal(payload.auto_withdraw_enabled,true)
assert.equal(payload.batch_interval_seconds,7)
assert.equal(payload.request_interval_seconds,4)
assert.deepEqual(nexovcc.templateProductIDs({product_id:267}),['267'])
assert.deepEqual(nexovcc.templateProductIDs({product_id:'267',product_ids:[]}),[])

const picker=readFileSync(new URL('../src/components/NexovccProductPicker.vue',import.meta.url),'utf8')
 .split('<script setup>')[1].split('</script>')[0].replace(/^import .*$/gm,'')
const pickerProps=reactive({modelValue:['267'],products:[],disabled:false})
const pickerContext=vm.createContext({cardOrigin,ref,computed,moveProduct:nexovcc.moveProduct,
 defineProps:()=>pickerProps,defineEmits:()=>(event,value)=>{assert.equal(event,'update:modelValue');pickerProps.modelValue=value},
})
vm.runInContext(picker+';globalThis.picker={toggle,start,drop,move,label};',pickerContext)
pickerContext.picker.toggle('268',true)
assert.deepEqual(Array.from(pickerProps.modelValue),['267','268'])
pickerContext.picker.start({dataTransfer:{setData(){}}},1)
pickerContext.picker.drop(0)
assert.deepEqual(Array.from(pickerProps.modelValue),['268','267'])
assert.match(pickerContext.picker.label('268'),/当前不可用/)
pickerContext.picker.move(0,1)
assert.deepEqual(Array.from(pickerProps.modelValue),['267','268'])
pickerContext.picker.toggle('267',false)
assert.deepEqual(Array.from(pickerProps.modelValue),['268'])
pickerProps.disabled=true
pickerContext.picker.toggle('267',true)
assert.deepEqual(Array.from(pickerProps.modelValue),['268'])
console.log('Passed: scheduled withdrawal settings, legacy template, multi-select, drag and keyboard priority, unavailable products')

assert.equal(cfg.http_522_retry_seconds,5)
assert.equal(cfg.http_522_retries,2)
cfg.http_522_retry_seconds=11
cfg.http_522_retries=0
await save()
payload=requests.filter(r=>r.url==='/api/nexovcc/settings').at(-1).body
assert.equal(payload.http_522_retry_seconds,11)
assert.equal(payload.http_522_retries,0)
console.log('Passed: 522 retry settings defaults and explicit disabled values')

context.view.filter.value='normal_pending'
context.view.data.cards=[{card:{id:'pending',status:'1',normal_pending:true}}]
await context.view.refreshPendingCards()
assert.equal(requests.filter(r=>r.url==='/api/nexovcc/run').at(-1).body.action,'pending_sync')
await context.view.syncCategoryTransactions()
assert.deepEqual(requests.filter(r=>r.url==='/api/nexovcc/run').at(-1).body,{action:'card_finance',card_category:'normal_pending',confirm:true})
context.view.filter.value='normal'
await context.view.syncCategoryTransactions()
assert.equal(requests.filter(r=>r.url==='/api/nexovcc/run').at(-1).body.card_category,'normal')
console.log('Passed: pending refresh and transaction buttons send isolated category actions')

for(const [key,value] of Object.entries({balance_sync_seconds:60,card_sync_seconds:180,transaction_sync_seconds:300,account_sync_seconds:1800,product_sync_seconds:7200}))cfg[key]=value
await save()
payload=requests.filter(r=>r.url==='/api/nexovcc/settings').at(-1).body
for(const key of ['balance_sync_seconds','card_sync_seconds','transaction_sync_seconds','account_sync_seconds','product_sync_seconds'])assert.equal(payload[key],cfg[key])
console.log('Passed: five independent sync intervals round-trip through settings save')
