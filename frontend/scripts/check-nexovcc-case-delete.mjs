import {cardOrigin} from '../src/card-platform.js'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import { ref, reactive, computed, watch, nextTick } from 'vue'
import { sortReturnCases } from '../src/nexovcc-case-details.js'

const source=readFileSync(new URL('../src/components/NexovccManagementView.vue',import.meta.url),'utf8')
const script=source.split('<script setup>')[1].split('</script>')[0].replace(/^import .*$/gm,'')
const fixture=()=>[
 ...Array.from({length:12},(_,i)=>({id:`failed-${i}`,state:'unopened',payment_status:'failed',card_id:'card-1'})),
 {id:'pending',state:'unopened',payment_status:'processing',card_id:'card-1'},
 {id:'paid',state:'opened',payment_status:'success',card_id:'card-1'},
]
function harness(){
 let accept=true,failDelete=false,failLoad=false,release=null,block=false
 let rows=fixture()
 const requests=[],confirmations=[]
 const context=vm.createContext({cardOrigin,ref,reactive,computed,watch,sortReturnCases,
  defineProps:()=>({defaultPageSize:10}),defineEmits:()=>()=>{},onMounted(){},onBeforeUnmount(){},
  window:{confirm(text){confirmations.push(text);return accept}},
  async api(url,options={}){
   requests.push({url,options})
   if(url==='/api/nexovcc/cases/unopened'){
    if(block)await new Promise(resolve=>release=resolve)
    if(failDelete)throw Error('request failed')
    const ids=rows.filter(c=>c.payment_status==='failed'&&!c.removed_at).map(c=>c.id)
    rows=rows.map(c=>ids.includes(c.id)?{...c,removed_at:'2026-10-19T08:00:00Z'}:c)
    return {deleted:ids.length,deleted_ids:ids,skipped:1}
   }
   if(url==='/api/nexovcc/dashboard'){
    if(failLoad)throw Error('refresh unavailable')
    return {cases:structuredClone(rows)}
   }
   throw Error('Unexpected API '+url)
  },
 })
 vm.runInContext(script+';globalThis.view={data,tab,returnCategory,search,page,filtered,visible,returnCases,unopenedCount,detail,detailCases,deletingUnopened,deleteUnopenedCases,message,job,saving,load};',context)
 const v=context.view
 Object.assign(v.data,{cases:fixture()});v.tab.value='returns';v.returnCategory.value='unopened'
 return {v,requests,confirmations,set(options){({accept=accept,failDelete=failDelete,failLoad=failLoad,block=block}=options)},release(){release()}}
}

// All pages/search results, repeated clicks, protected rows, and the card dialog.
{
 const h=harness(),v=h.v
 await nextTick()
 v.page.value=2
 assert.equal(v.visible.value.length,3)
 v.search.value='failed-11'
 await nextTick()
 h.set({block:true})
 const deleting=v.deleteUnopenedCases()
 assert.equal(v.deletingUnopened.value,true)
 assert.match(v.message.text,/正在清理/)
 await v.deleteUnopenedCases()
 assert.equal(h.requests.length,1)
 assert.match(h.confirmations[0],/13 条/)
 assert.match(h.confirmations[0],/其他分页和搜索范围外/)
 assert.equal(h.requests[0].options.method,'DELETE')
 assert.equal(h.requests[0].options.body.confirm,true)
 h.release();await deleting;await nextTick()
 assert.equal(v.deletingUnopened.value,false)
 assert.equal(v.data.cases.length,14,'financial/internal history must remain')
 assert.equal(v.returnCases.value.length,2)
 assert.equal(v.unopenedCount.value,1)
 assert.equal(v.page.value,1)
 v.detail.value={type:'accounts',cardID:'card-1'}
 assert.equal(v.detailCases.value.length,2)
 assert.match(v.message.text,/已删除 12 条/)
 assert.match(v.message.text,/1 条.*已保留/)
}
// Cancel leaves everything intact; failed requests preserve rows and unlock UI.
{
 const h=harness(),v=h.v
 h.set({accept:false});await v.deleteUnopenedCases()
 assert.equal(h.requests.length,0)
 h.set({accept:true,failDelete:true});await v.deleteUnopenedCases()
 assert.equal(v.returnCases.value.length,14)
 assert.equal(v.deletingUnopened.value,false)
 assert.match(v.message.text,/清理失败/)
}
// A refresh failure must not misreport an already committed cleanup as failed.
{
 const h=harness(),v=h.v
 h.set({failLoad:true});await v.deleteUnopenedCases()
 assert.equal(v.unopenedCount.value,1)
 assert.match(v.message.text,/已删除 12 条.*刷新失败/)
 assert.equal(v.deletingUnopened.value,false)
}
// Running card operations and pending edits block cleanup.
{
 const h=harness(),v=h.v
 v.job.value={status:'running'};await v.deleteUnopenedCases()
 v.job.value={};v.saving.value=true;await v.deleteUnopenedCases()
 assert.equal(h.requests.length,0)
}
assert.match(source,/tab==='returns'&&returnCategory==='unopened'[\s\S]*?@click="deleteUnopenedCases"/)
console.log('Passed: unopened cleanup across pages, guards, retained history, dialog visibility, request and refresh failures')
