import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import {computed,ref} from 'vue'
import {proCardEntry} from '../src/pro-card-entry.js'

assert.deepEqual(proCardEntry({email:'pro@example.com',activation_card:{nexovcc_card_id:'canonical',card_last4:'4242'}}),{provider:'nexovcc',email:'pro@example.com',cardID:'canonical'})
assert.deepEqual(proCardEntry({email:'pro@example.com',activation_card:{ejh_card_id:'ejh-card'}}),{provider:'ejh',email:'pro@example.com',cardID:'ejh-card'})
assert.equal(proCardEntry({email:'pro@example.com',activation_card:{card_last4:'4242',card_name:'环球'}}),null)
assert.equal(proCardEntry({activation_card:{nexovcc_card_id:'canonical'}}),null)

function functions(file,names){
 const source=readFileSync(new URL('../src/components/'+file,import.meta.url),'utf8').split('<script setup>')[1].split('</script>')[0]
 const parsed=ts.createSourceFile(file,source,ts.ScriptTarget.Latest,true,ts.ScriptKind.JS)
 return parsed.statements.filter(s=>ts.isFunctionDeclaration(s)&&names.includes(s.name?.text)).map(s=>s.getText(parsed)).join('\n')
}
const blocked=ref([]),calls=[]
const ctx=vm.createContext({blockedCards:blocked,api:async url=>{
 calls.push(url)
 return url.includes('page=1&')?{items:[{id:'first',card_type:'ejh',occupants:[{deleted:true,order_id:'o1'}]}],total:501}:{items:[{id:'last',card_type:'nexovcc',occupants:[{deleted:true,order_id:'o2'}]},{id:'ordinary',card_type:'other',occupants:[{deleted:true,order_id:'o3'}]}],total:501}
}})
vm.runInContext(functions('ProAutoDialog.vue',['loadBlockedCards'])+';this.load=loadBlockedCards',ctx)
await ctx.load();assert.deepEqual(Array.from(blocked.value,c=>c.id),['first','last']);assert.equal(calls.length,2)

for(const provider of ['ejh','nexovcc']){
 const requests=[],order=ref(null),opened=ref(false),busy=ref(false),card=ref(null),outcome=ref(''),evidence=ref(''),confirmed=ref(false)
 const context=vm.createContext({order,opened,busy,card,outcome,evidence,confirmed,error:ref(''),canSave:ref(true),base:computed(()=>'/api/'+provider+'/occupancy/'),emit(){},api:async(url,options)=>{requests.push({url,options});return {id:'original',email:'deleted@example.com',status:'manual_review'}}})
 vm.runInContext(functions('EJHOccupancyDialog.vue',['open','save'])+';this.run={open,save}',context)
 await context.run.open({order_id:'original'},{id:'physical-card'})
 outcome.value='not_activated';evidence.value='Verified original supplier result';confirmed.value=true
 await context.run.save()
 assert.equal(requests[0].url,'/api/'+provider+'/occupancy/original')
 assert.equal(requests[1].url,'/api/'+provider+'/occupancy/original/resolve')
 assert.equal(requests[1].options.body.card_id,'physical-card');assert.equal(requests[1].options.body.expected_status,'manual_review')
}
console.log('Passed: canonical issuer navigation, occupancy across all pages, issuer-specific resolution with original-order guards')
