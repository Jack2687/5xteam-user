import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import vm from 'node:vm'
import {ref,reactive,computed,watch,nextTick} from 'vue'
const code=readFileSync(new URL('../src/components/ProTakeoverPanel.vue',import.meta.url),'utf8').split('<script setup>')[1].split('</script>')[0].replace(/^import .*$/gm,'')
const config={version:1,enabled:false,role:'receiver',groups:[],peer_url:'http://peer.test',username:'admin',check_seconds:10}
function fixture(c=config){
 const requests=[],stops=[],props=reactive({data:{config:{...c},state:{}},pushSettings:{group_ids:[42,43],group_names:['old','old2']},monitorGroup:42,compact:false})
 let unmounted
 const context=vm.createContext({ref,reactive,computed,watch:(...args)=>{const stop=watch(...args);stops.push(stop);return stop},defineProps:()=>props,defineEmits:()=>()=>{},onUnmounted:fn=>{unmounted=fn},
 api:(url,options={})=>new Promise((resolve,reject)=>requests.push({url,options:JSON.parse(JSON.stringify(options)),resolve,reject}))})
 vm.runInContext(code+'\nglobalThis.exposed={form,password,dirty,busy,error,notice,groupsError,options,loadGroups,save,test,toggleEnabled};',context)
 return {...context.exposed,props,requests,close(){unmounted();stops.forEach(stop=>stop())}}
}
const plain=x=>JSON.parse(JSON.stringify(x))
const tick=()=>new Promise(resolve=>setImmediate(resolve))
{
 const f=fixture()
 assert.deepEqual(plain(f.form.groups),[42,43])
 f.requests[0].resolve([{id:42,name:'current'},{id:43,name:'extra'},{id:44,name:'new-group'}]);await tick()
 assert.equal(f.options.value[0].name,'current')
 f.form.groups=[42,44];f.dirty.value=true;f.password.value='local-secret'
 const refresh=f.loadGroups();f.requests.at(-1).resolve([{id:42,name:'renamed'},{id:43,name:'extra'},{id:44,name:'new-name'}]);await refresh
 assert.deepEqual(plain(f.form.groups),[42,44],'refresh overwrote selection')
 assert.equal(f.options.value[0].name,'renamed')
 f.props.data.config={...config,version:2,groups:[43]};await nextTick()
 assert.deepEqual(plain(f.form.groups),[42,44],'poll overwrote edits')
 const save=f.save();const put=f.requests.at(-1)
 assert.equal(put.url,'/api/pro-takeover/config')
 assert.deepEqual(put.options.body.groups,[42,44],'save used original push groups')
 put.resolve({...config,version:3,groups:[42,44],password_present:true});await save
 assert.equal(f.form.enabled,false);assert.equal(f.password.value,'');assert.equal(f.dirty.value,false)
 // A slower poll must not revert a successful save.
 f.props.data.config={...config,version:2,groups:[43]};await nextTick()
 assert.deepEqual(plain(f.form.groups),[42,44]);assert.equal(f.form.version,3)
 const enable=f.toggleEnabled();const req=f.requests.at(-1)
 assert.equal(req.url,'/api/pro-takeover/enabled');assert.deepEqual(req.options.body,{enabled:true,version:3})
 req.reject(new Error('两边应分别选择提供接管和接受接管'));await enable
 assert.equal(f.form.enabled,false);assert.deepEqual(plain(f.form.groups),[42,44]);assert.equal(f.form.version,3)
 const retry=f.toggleEnabled();f.requests.at(-1).resolve({...config,version:4,enabled:true,groups:[42,44]});await retry
 assert.equal(f.form.enabled,true)
 const missing=f.loadGroups();f.requests.at(-1).resolve([{id:42,name:'renamed'}]);await missing
 assert.equal(f.options.value.find(g=>g.id===44).missing,true,'deleted choice silently lost')
 assert.deepEqual(plain(f.form.groups),[42,44]);f.close()
}
{
 const f=fixture({...config,version:2,groups:[]})
 assert.deepEqual(plain(f.form.groups),[],'saved empty selection was reseeded')
 f.close();f.requests[0].resolve([{id:42,name:'late'}]);await tick()
 assert.equal(f.options.value.length,0,'unmounted page accepted response')
}
{
 const f=fixture({...config,version:2,role:'provider',groups:[]})
 assert.equal(f.requests.length,0,'provider unnecessarily fetched receiver groups')
 f.dirty.value=true;await f.toggleEnabled()
 assert.equal(f.requests.length,0,'enabled unsaved config');assert.match(f.error.value,/先保存/)
 f.close()
}
console.log('Passed: local save, explicit enable, latest group names, independent multi-selection, stale polling and unmount guards')
