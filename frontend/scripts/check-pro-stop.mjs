import assert from 'node:assert/strict'
import vm from 'node:vm'
import {readFileSync} from 'node:fs'
import {ref,computed} from 'vue'
const source=readFileSync(new URL('../src/components/ProAutoDialog.vue',import.meta.url),'utf8')
const code=source.split('<script setup>')[1].split('</script>')[0].replace(/^import .*$/gm,'')
const calls=[]
const c=vm.createContext({ref,computed,defineProps:()=>({}),defineEmits:()=>()=>{},defineExpose:()=>{},onBeforeUnmount:()=>{},setTimeout:()=>0,clearTimeout:()=>{},window:{confirm:()=>{throw Error('stop must be immediate')}},api:async(path,options)=>{calls.push({path,options});return {status:'stopped',stop_requested:true,pro_workflow_running:false}}})
vm.runInContext(code+'\nglobalThis.test={state,email,active,stop,busy};',c)
c.test.email.value='test@example.test'
c.test.state.value={status:'completed',pro_workflow_running:true}
assert.equal(c.test.active.value,true,'independent deletion must expose Stop')
await c.test.stop()
assert.equal(calls[0].path,'/api/pro-accounts/test%40example.test/auto-pro/stop')
assert.equal(calls[0].options.method,'POST')
assert.equal(c.test.active.value,false)
assert.equal(c.test.busy.value,false)
const management=readFileSync(new URL('../src/components/ProManagementView.vue',import.meta.url),'utf8')
const runOne=management.slice(management.indexOf('async function runOne('),management.indexOf('async function runBatch('))
let confirms=0,requests=0
const guard=vm.createContext({busy:ref(''),window:{confirm:()=>{confirms++;return false}},api:()=>{requests++;throw Error('must not delete after cancel')}})
vm.runInContext(runOne+'\nglobalThis.run=runOne;',guard)
await guard.run({email:'test@example.test'},'deactivate')
assert.equal(confirms,1)
assert.equal(requests,0)
assert.equal(guard.busy.value,'')
console.log('Passed: independent workflow can stop immediately; cancelled deletion confirmation sends no request')
