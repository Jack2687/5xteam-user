import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import { ref, computed } from 'vue'
const source=readFileSync(new URL('../src/components/EJHOccupancyDialog.vue',import.meta.url),'utf8')
const code=source.split('<script setup>')[1].split('</script>')[0].replace(/^import .*$/gm,'')
const calls=[],events=[]
let status='success',fail=false,hold,release
const ctx=vm.createContext({ref,computed,defineExpose(){},defineEmits:()=>event=>events.push(event),async api(path,options){
 calls.push({path,options})
 if(path.endsWith('/resolve')){if(hold)await hold;if(fail)throw Error('订单状态已变化');return {released:true}}
 return {id:'old',email:'deleted@example.com',status,remote:{id:''}}
}})
vm.runInContext(code+';globalThis.test={open,save,query,order,opened,busy,error,outcome,evidence,confirmed,canSave}',ctx)
const f=ctx.test
await f.open({order_id:'old'},{id:'card',last4:'9974'})
assert.equal(f.canSave.value,false)
f.outcome.value='success_removed';f.evidence.value='verified successful removal'
await f.save();assert.equal(calls.length,1,'submitted without confirmation')
f.confirmed.value=true;hold=new Promise(r=>release=r)
const pending=f.save();assert.equal(f.busy.value,true);await f.save();assert.equal(calls.length,2,'duplicate submission')
release();await pending;hold=null
assert.equal(f.opened.value,false);assert.deepEqual(events,['updated'])
assert.deepEqual(JSON.parse(JSON.stringify(calls[1].options.body)),{card_id:'card',email:'deleted@example.com',expected_status:'success',outcome:'success_removed',evidence:'verified successful removal',confirm:true})
status='processing';await f.open({order_id:'old'},{id:'card'});f.outcome.value='not_activated';f.evidence.value='supplier confirmed';f.confirmed.value=true
assert.equal(f.canSave.value,false);const count=calls.length;await f.save();assert.equal(calls.length,count)
status='manual_review';await f.open({order_id:'old'},{id:'card'});await f.query();assert.equal(calls.length,count+1,'remote-less order must never be resubmitted')
f.outcome.value='not_activated';f.evidence.value='supplier confirmed';f.confirmed.value=true;fail=true
await f.save();assert.equal(f.opened.value,true);assert.equal(f.busy.value,false);assert.ok(f.error.value.includes('状态已变化'));assert.equal(events.length,1)
assert.ok(!calls.some(c=>c.path.endsWith('/retry')||c.path.includes('/open')))
console.log('Passed: explicit confirmation, pending feedback, duplicate-submit guard, unresolved processing rejection, no payment resubmission, errors retain dialog')
