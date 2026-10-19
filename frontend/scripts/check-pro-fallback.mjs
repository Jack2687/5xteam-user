import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import vm from 'node:vm'
import {ref,reactive,computed} from 'vue'
import {createLatestRequest} from '../src/latestRequest.js'
const tick=()=>new Promise(resolve=>setImmediate(resolve))
const source=readFileSync(new URL('../src/components/ProFallbackPanel.vue',import.meta.url),'utf8')
const code=source.split('<script setup>')[1].split('</script>')[0].replace(/^import .*$/gm,'')
function fixture(){
 const requests=[],timers=new Set();let mount,unmount
 const context=vm.createContext({ref,reactive,computed,createLatestRequest,defineProps(){},onMounted(fn){mount=fn},onUnmounted(fn){unmount=fn},setInterval(fn){timers.add(fn);return fn},clearInterval(fn){timers.delete(fn)},api(url,options={}){let resolve,reject;const promise=new Promise((ok,fail)=>{resolve=ok;reject=fail});requests.push({url,options,resolve,reject});return promise}})
 vm.runInContext(code+'\nglobalThis.exposed={state,form,password,groups,accounts,loaded,search,dirty,notice,error,refresh,save,retry,resetConnection,resetGroup,loadAccounts,loadGroups,selectVisible,input,groupOptions};',context)
 return {...context.exposed,requests,timers,mount:()=>mount(),unmount:()=>unmount()}
}
const config=()=>({version:1,enabled:true,url:'https://example.test',email:'test@example.test',password_present:true,group_id:7,account_ids:[1,2],check_seconds:3})
const state=(c=config())=>({config:c,plan:{phase:'active',active:true},accounts:[],checked_at:new Date().toISOString()})
{
 const f=fixture();const mounted=f.mount();await tick();f.unmount();f.requests[0].resolve(state());await mounted;assert.equal(f.timers.size,0);assert.equal(f.state.value,null)
}
{
 const f=fixture();const initial=f.refresh(true);await tick();f.requests[0].resolve(state());await initial
 f.form.account_ids.push(3);f.form.check_seconds=8;f.dirty.value=true
 assert.deepEqual([...f.state.value.config.account_ids],[1,2],'form mutated saved state')
 const poll=f.refresh();await tick();f.requests[1].resolve(state());await poll
 assert.equal(f.form.check_seconds,8,'poll overwrote draft');assert.deepEqual([...f.form.account_ids],[1,2,3])
 const old=f.refresh();await tick();const oldRequest=f.requests.at(-1);f.password.value='temporary-password'
 const saving=f.save();await tick();const mutation=f.requests.at(-1);assert.equal(mutation.options.method,'PUT');assert.equal(mutation.options.body.password,'temporary-password')
 const saved={...config(),version:2,account_ids:[2],check_seconds:8};mutation.resolve(saved);await tick();f.requests.at(-1).resolve(state(saved));await saving
 oldRequest.resolve(state());await old
 assert.equal(f.state.value.config.version,2);assert.equal(f.form.version,2);assert.equal(f.password.value,'');assert.equal(f.dirty.value,false)
 f.unmount()
}
{
 const f=fixture();Object.assign(f.form,config());f.accounts.value=[{id:1,name:'OAuth one',type:'oauth'},{id:2,name:'Key two',type:'apikey'},{id:3,name:'Key three',type:'apikey'}]
 f.search.value='key';f.form.account_ids=[1];f.selectVisible();assert.deepEqual([...f.form.account_ids],[1,2,3]);f.selectVisible();assert.equal(f.form.account_ids.length,3)
 const loading=f.loadAccounts();await tick();assert.equal(f.requests[0].options.body.enabled,false,'preview should not require enabling');f.requests[0].reject(new Error('offline'));await loading;assert.equal(f.error.value,'offline');assert.deepEqual([...f.form.account_ids],[1,2,3],'failed refresh cleared selection')
 f.password.value='old-secret';f.resetConnection();assert.equal(f.password.value,'');assert.equal(f.form.group_id,0);assert.equal(f.form.account_ids.length,0);assert.equal(f.accounts.value.length,0)
 f.unmount()
}
{
 const f=fixture();const initial=f.refresh(true);await tick();f.requests[0].resolve(state());await initial
 assert.deepEqual([...f.form.group_ids],[7],'legacy saved group was lost')
 f.form.group_ids.push(8);f.dirty.value=true
 assert.deepEqual([...f.input().group_ids],[7,8])
 assert.equal(f.input().group_id,7)
 const loading=f.loadAccounts();await tick();const req=f.requests.at(-1)
 assert.deepEqual([...req.options.body.group_ids],[7,8],'preview ignored one selected group')
 req.resolve([{id:1,name:'one',type:'oauth'},{id:2,name:'two',type:'apikey'}]);await loading
 const saved={...config(),version:2,group_ids:[7,8]}
 const saving=f.save();await tick();f.requests.at(-1).resolve(saved);await tick();f.requests.at(-1).resolve(state(saved));await saving
 f.form.group_ids.pop();assert.deepEqual([...f.state.value.config.group_ids],[7,8],'editing draft changed saved groups')
 f.form.group_ids=[];assert.equal(f.input().group_id,0);assert.deepEqual([...f.input().group_ids],[])
 f.resetConnection();assert.deepEqual([...f.form.group_ids],[])
 f.unmount()
}
console.log('Passed: fallback multi-group drafts, legacy loading, complete preview payload and clear selection')
console.log('Passed: fallback drafts, request ordering, credential clearing, preview errors and account multi-select')
