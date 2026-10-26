import { applyPaymentPolicies } from '../src/payment-only.js'
import {cardOrigin} from '../src/card-platform.js'
import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import vm from 'node:vm'
import {webcrypto} from 'node:crypto'
import {ref,reactive,computed,watch} from 'vue'
import * as helpers from '../src/nexovcc.js'

const source=readFileSync(new URL('../src/components/NexovccManagementView.vue',import.meta.url),'utf8')
const code=source.split('<script setup>')[1].split('</script>')[0].replace(/^import .*$/gm,'')
const calls=[],timers=[]
let paused=false
const ctx=vm.createContext({applyPaymentPolicies,cardOrigin,...helpers,ref,reactive,computed,watch,crypto:webcrypto,
 defineProps:()=>({defaultPageSize:10}),defineEmits:()=>()=>{},onMounted(){},onBeforeUnmount(){},clearTimeout(){},setTimeout:fn=>timers.push(fn),formatTime:v=>v,
 async api(url,options={}){
  calls.push({url,body:options.body})
  if(url==='/api/nexovcc/control'){paused=options.body.action==='stop';return {paused}}
  if(url==='/api/nexovcc/job')return {id:'control',status:paused?'stopped':'completed',paused}
  if(url==='/api/nexovcc/dashboard')return {control:{paused}}
  if(url==='/api/nexovcc/run')return {id:'open',status:'completed',operation:{status:'succeeded'}}
  throw Error(url)
 }
})
vm.runInContext(code+';globalThis.view={batchOpen,controlInterfaces,perform,apiPaused,busy,batch};',ctx)
const pending=ctx.view.batchOpen({count:2,template_id:'fixture',confirm:true})
for(let i=0;i<10&&!timers.length;i++)await Promise.resolve()
assert.equal(calls.filter(x=>x.url.endsWith('/run')).length,1)
assert.ok(ctx.view.busy.value)
await ctx.view.controlInterfaces()
assert.ok(ctx.view.apiPaused.value)
await ctx.view.perform({action:'balance'})
assert.equal(calls.filter(x=>x.url.endsWith('/run')).length,1,'paused request leaked')
await ctx.view.controlInterfaces()
assert.equal(ctx.view.apiPaused.value,false)
for(const fn of timers)fn()
await pending
assert.equal(calls.filter(x=>x.url.endsWith('/run')).length,1,'old batch resumed after stop')
await ctx.view.perform({action:'balance'})
assert.equal(calls.filter(x=>x.url.endsWith('/run')).length,2,'new request blocked after resume')
assert.match(source,/:disabled="controlBusy\|\|!ready" @click="controlInterfaces"/)
console.log('Passed: stop available while busy, all requests blocked while paused, resume never restarts old batch')
