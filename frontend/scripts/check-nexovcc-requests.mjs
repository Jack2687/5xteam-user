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
const context=vm.createContext({...nexovcc,ref,reactive,computed,watch,
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
vm.runInContext(code+';globalThis.view={cfg,save,submit,dialog,message};',context)
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
