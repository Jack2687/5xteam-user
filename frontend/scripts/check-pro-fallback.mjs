import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import vm from 'node:vm'
import {ref,reactive,computed,watch} from 'vue'
import {createLatestRequest} from '../src/latestRequest.js'
const tick=()=>new Promise(resolve=>setImmediate(resolve))
const source=readFileSync(new URL('../src/components/ProFallbackPanel.vue',import.meta.url),'utf8')
const code=source.split('<script setup>')[1].split('</script>')[0].replace(/^import .*$/gm,'')
function fixture(settings=false){
 const requests=[],timers=new Set(),watches=[],props=reactive({settings,compact:!settings});let mount,unmount
 const context=vm.createContext({ref,reactive,computed,AbortController,setTimeout,clearTimeout,watch(...args){const stop=watch(...args);watches.push(stop);return stop},createLatestRequest,defineProps(){return props},onMounted(fn){mount=fn},onUnmounted(fn){unmount=fn},setInterval(fn){timers.add(fn);return fn},clearInterval(fn){timers.delete(fn)},api(url,options={}){let resolve,reject;const promise=new Promise((ok,fail)=>{resolve=ok;reject=fail});requests.push({url,options,resolve,reject});return promise}})
 vm.runInContext(code+'\nglobalThis.exposed={state,form,password,groups,monitorGroups,dirty,notice,error,refresh,save,retry,reload,resetConnection,resetGroup,loadAccounts,loadGroups,loadMonitorGroups,selectVisible,input,groupOptions,monitorOptions,monitorLoading,monitorError,groupsLoading,groupsError,addRule,removeRule,allIDs,busy,remove,cancelAccounts,accountName,removeUnavailable};',context)
 return {...context.exposed,props,requests,timers,mount:()=>mount(),unmount:()=>{unmount();watches.forEach(stop=>stop())}}
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
 assert.equal(b._error,'offline');assert.deepEqual([...b.account_ids],[2,3])
 f.resetGroup(b);assert.equal(b.account_ids.length,2,'removing a deleted group must preserve other selections');assert.equal(a.account_ids.length,3)
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
{
 const f=fixture(true),mounted=f.mount();await tick()
 f.requests[0].resolve(state({...config(),rules:[{enabled:true,monitor_group_id:4,group_ids:[7],account_ids:[1,2]}]}));await mounted;await tick()
 assert.equal(f.requests.length,3,'opening settings must load both complete group lists automatically')
 const monitor=f.requests.find(r=>r.url.endsWith('/monitor-groups')),groups=f.requests.find(r=>r.url.endsWith('/groups'))
 assert.equal(f.monitorLoading.value,true);assert.equal(f.groupsLoading.value,true)
 await f.loadMonitorGroups(false);assert.equal(f.requests.length,3,'focusing a loading selector duplicated the request')
 f.addRule();f.form.rules[1].monitor_group_id=5;f.dirty.value=true
 monitor.resolve([{id:4,name:'0.2'},{id:5,name:'0.15'},{id:6,name:'6a'}]);groups.resolve([{id:7,name:'fallback'},{id:8,name:'second fallback'}]);await tick()
 assert.deepEqual([...f.monitorOptions.value.map(g=>g.name)],['0.2','0.15','6a'])
 assert.equal(f.groupOptions.value.length,2);assert.equal(f.monitorLoading.value,false)
 assert.equal(f.form.rules.length,2);assert.equal(f.form.rules[1].monitor_group_id,5);assert.equal(f.dirty.value,true,'loading choices reset unsaved edits')
 await f.loadMonitorGroups(false);assert.equal(f.requests.length,3,'opening another selector must reuse loaded choices')
 f.unmount()
}
{
 const f=fixture();await initial(f);assert.equal(f.requests.length,1,'dashboard must not fetch settings group catalogs')
 f.props.settings=true;await tick()
 assert.equal(f.requests.length,3,'switching to settings on the same mounted panel did not load groups')
 f.requests.find(r=>r.url.endsWith('/monitor-groups')).reject(new Error('temporarily offline'))
 f.requests.find(r=>r.url.endsWith('/groups')).resolve([{id:7,name:'fallback'}]);await tick()
 assert.equal(f.monitorError.value,'temporarily offline');assert.equal(f.form.rules[0].monitor_group_id,9)
 const retry=f.loadMonitorGroups(false);await tick();f.requests.at(-1).resolve([{id:9,name:'renamed'},{id:10,name:'new group'}]);await retry
 assert.equal(f.monitorError.value,'');assert.equal(f.monitorOptions.value.length,2)
 const reload=f.reload();await tick();f.requests.at(-1).resolve(state());await tick()
 assert.equal(f.requests.at(-2).url,'/api/pro-restock/fallback/monitor-groups')
 f.requests.at(-2).resolve([{id:9,name:'new name'},{id:10,name:'new group'}]);f.requests.at(-1).resolve([{id:7,name:'fallback'}]);await reload
 assert.equal(f.monitorOptions.value[0].name,'new name','reload discarded choices instead of refreshing them')
 const loading=f.loadGroups();await tick();const old=f.requests.at(-1);f.resetConnection();old.resolve([{id:99,name:'old connection'}]);await loading
 assert.equal(f.groups.value.length,0,'stale connection choices were applied')
 f.unmount()
}
{
 const f=fixture();await initial(f,{...config(),account_names:{1:'Deleted account',2:'Existing account'}})
 const row=f.form.rules[0]
 assert.equal(f.accountName(row,1),'Deleted account')
 const loading=f.loadAccounts(row);await tick();const first=f.requests.at(-1)
 assert.equal(f.busy.value,false,'account read disabled all selection controls')
 f.remove(row,'1');assert.deepEqual([...row.account_ids],[2],'deselect failed during account read')
 first.resolve({accounts:[{id:2,name:'Renamed account',type:'apikey'}],complete:true,warnings:['group #97 removed']});await loading
 assert.deepEqual([...row.account_ids],[2],'late read restored deleted selection');assert.equal(f.accountName(row,2),'Renamed account')
 assert.equal(row._warnings.length,1)
 row.account_ids=[1,2];f.removeUnavailable(row);assert.deepEqual([...row.account_ids],[2])
 row.account_ids=[1,2];const partial=f.loadAccounts(row);await tick();f.requests.at(-1).resolve({accounts:[],complete:false,warnings:['group failed']});await partial
 f.removeUnavailable(row);assert.deepEqual([...row.account_ids],[1,2],'partial failure classified accounts as deleted')
 const stale=f.loadAccounts(row);await tick();const old=f.requests.at(-1);f.resetGroup(row);old.resolve({accounts:[{id:3,name:'stale'}],complete:true});await stale
 assert.equal(row._accounts.length,0,'old group read applied after group change');assert.equal(old.options.signal.aborted,true)
 const cancelled=f.loadAccounts(row);await tick();const cancelledRequest=f.requests.at(-1);f.cancelAccounts(row);cancelledRequest.reject(new Error('aborted'));await cancelled;assert.equal(row._error,'')
 f.form.url='https://other.test';f.resetConnection();assert.equal(f.accountName(row,1),'未保存账号名称','old connection account name leaked')
 f.unmount()
}
console.log('Passed: fallback reads, deleted group recovery, editable selections during reads, cancellation, saved names, partial failure, drafts and connection isolation')
