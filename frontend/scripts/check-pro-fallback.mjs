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
 vm.runInContext(code+'\nglobalThis.exposed={state,form,password,groups,monitorGroups,dirty,notice,error,refresh,save,retry,resetConnection,resetGroup,loadAccounts,loadGroups,loadMonitorGroups,selectVisible,input,groupOptions,monitorOptions,addRule,removeRule,allIDs};',context)
 return {...context.exposed,requests,timers,mount:()=>mount(),unmount:()=>unmount()}
}
const config=()=>({version:1,enabled:true,url:'https://example.test',email:'test@example.test',password_present:true,group_id:7,account_ids:[1,2],check_seconds:3})
const state=(c=config())=>({config:c,monitor:{group_id:9},plan:{phase:'active',active:true},accounts:[],checked_at:new Date().toISOString()})
async function initial(f,c=config()){const p=f.refresh(true);await tick();f.requests.at(-1).resolve(state(c));await p}
{
 const f=fixture();const mounted=f.mount();await tick();f.unmount();f.requests[0].resolve(state());await mounted;assert.equal(f.timers.size,0);assert.equal(f.state.value,null)
}
{
 const f=fixture();await initial(f)
 assert.equal(f.form.rules[0].monitor_group_id,9,'legacy source monitor lost')
 f.form.rules[0].account_ids.push(3);f.form.check_seconds=8;f.dirty.value=true
 assert.deepEqual([...f.state.value.config.account_ids],[1,2],'draft mutated saved state')
 const poll=f.refresh();await tick();f.requests.at(-1).resolve(state());await poll
 assert.equal(f.form.check_seconds,8);assert.deepEqual([...f.form.rules[0].account_ids],[1,2,3])
 const old=f.refresh();await tick();const oldRequest=f.requests.at(-1);f.password.value='temporary-password'
 const saving=f.save();await tick();const mutation=f.requests.at(-1);assert.equal(mutation.options.method,'PUT');assert.equal(mutation.options.body.password,'temporary-password')
 assert.equal(mutation.options.body.rules[0]._accounts,undefined,'editor state sent to server')
 const saved={...config(),version:2,rules:[{enabled:true,monitor_group_id:9,group_ids:[7],account_ids:[2]}],account_ids:[2],check_seconds:8};mutation.resolve(saved);await tick();f.requests.at(-1).resolve(state(saved));await saving
 oldRequest.resolve(state());await old
 assert.equal(f.state.value.config.version,2);assert.equal(f.form.version,2);assert.equal(f.password.value,'');assert.equal(f.dirty.value,false)
 f.unmount()
}
{
 const f=fixture();await initial(f)
 f.addRule();const a=f.form.rules[0],b=f.form.rules[1]
 b.monitor_group_id=10;b.group_ids=[8,11];b.account_ids=[2,3]
 assert.deepEqual([...f.allIDs.value],[1,2,3],'shared accounts not deduplicated')
 a._accounts=[{id:1,name:'OAuth one',type:'oauth'},{id:2,name:'Key two',type:'apikey'},{id:3,name:'Key three',type:'apikey'}]
 a._search='key';a.account_ids=[1];f.selectVisible(a)
 assert.deepEqual([...a.account_ids],[1,2,3]);assert.deepEqual([...b.account_ids],[2,3])
 const loading=f.loadAccounts(b);await tick();const req=f.requests.at(-1)
 assert.equal(req.options.body.enabled,false);assert.equal(req.options.body.rules,null)
 assert.deepEqual([...req.options.body.group_ids],[8,11],'wrong rule groups queried')
 req.reject(new Error('offline'));await loading
 assert.equal(f.error.value,'offline');assert.deepEqual([...b.account_ids],[2,3])
 f.resetGroup(b);assert.equal(b.account_ids.length,0);assert.equal(a.account_ids.length,3)
 f.password.value='old';f.resetConnection();assert.equal(f.password.value,'')
 assert.equal(a.monitor_group_id,9);assert.equal(b.monitor_group_id,10)
 assert.equal(a.account_ids.length,0);assert.equal(b.group_ids.length,0)
 f.removeRule(a);f.removeRule(b);assert.deepEqual([...f.input().rules],[],'deleted rules resurrected legacy fields')
 f.unmount()
}
{
 const f=fixture();await initial(f,{...config(),rules:[],account_ids:[]})
 assert.equal(f.form.rules.length,0,'empty rules turned into legacy rule')
 const loading=f.loadMonitorGroups();await tick();assert.equal(f.requests.at(-1).url,'/api/pro-restock/fallback/monitor-groups')
 f.requests.at(-1).resolve([{id:9,name:'main'},{id:10,name:'second'}]);await loading
 assert.equal(f.monitorOptions.value.length,2)
 f.unmount()
}
console.log('Passed: independent fallback rule drafts, shared account deduplication, isolated previews, legacy migration, empty rules, stale responses and credential handling')
