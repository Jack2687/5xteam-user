import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import { computed, ref } from 'vue'
import { summarizeInFlightPayments } from '../src/ejh-account-amounts.js'

const code=readFileSync(new URL('../src/components/EJHBalanceOverview.vue',import.meta.url),'utf8').split('<script setup>')[1].split('</script>')[0].replace(/^import .*$/gm,'')
const calls=[],emits=[]
const props={busy:false,data:{cards:[],cases:[],operations:[],recharge_ledger:{amount:'12.00'}}}
let fail=false
const context=vm.createContext({computed,ref,nextTick:async()=>{},defineProps:()=>props,defineEmits:()=>((event,value)=>{emits.push({event,value});props.data.recharge_ledger=value}),summarizeInFlightPayments,summarizeCardBalances:()=>({}),summarizeCardBalancesUSD:()=>({}),async api(url,options){calls.push({url,options});if(fail)throw Error('save failed');return {amount:options.body.amount,updated_at:'2026-10-16'}}})
vm.runInContext(code+';globalThis.form={editLedger,saveLedger,editing,draft,error,saving,valid};',context)
const f=context.form
await f.editLedger();assert.equal(f.draft.value,'12.00');assert.equal(f.editing.value,true)
f.draft.value='1234.56';props.data.recharge_ledger={amount:'999.00'}
assert.equal(f.draft.value,'1234.56','background refresh replaced unsaved input')
await f.saveLedger()
assert.deepEqual(JSON.parse(JSON.stringify(calls[0])),{url:'/api/ejh/recharge-ledger',options:{method:'PUT',body:{amount:'1234.56'}}})
assert.equal(f.editing.value,false);assert.equal(emits[0].event,'ledger-updated')
await f.editLedger();f.draft.value='-1';await f.saveLedger();assert.equal(calls.length,1)
f.draft.value='1.001';assert.equal(f.valid.value,false)
f.draft.value='10.00';fail=true;await f.saveLedger()
assert.equal(f.editing.value,true);assert.equal(f.draft.value,'10.00');assert.match(f.error.value,/save failed/);assert.equal(f.saving.value,false)
fail=false;f.draft.value='0';await f.saveLedger();assert.equal(calls.at(-1).options.body.amount,'0')
await f.editLedger();f.draft.value='';await f.saveLedger();assert.equal(calls.at(-1).options.body.amount,'')
const before=calls.length;props.busy=true;await f.saveLedger();assert.equal(calls.length,before)
console.log('Passed: manual ledger save/clear/zero, server errors, validation, busy guard and unsaved input retained during refresh')
