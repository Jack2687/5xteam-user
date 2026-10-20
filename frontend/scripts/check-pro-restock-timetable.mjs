import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import vm from 'node:vm'
import {computed,reactive} from 'vue'
import {proPlanName} from '../src/gptpay.js'

const source=readFileSync(new URL('../src/components/ProRestockTimetable.vue',import.meta.url),'utf8')
const code=source.split('<script setup>')[1].split('</script>')[0].replace(/^import .*$/gm,'')
const props=reactive({config:{enabled:true,mode:'auto'},now:Date.parse('2026-10-19T20:49:59+08:00'),timetable:{periods:[
 {start_minute:120,end_minute:540,blocked:true,source:'blocked'},
 {start_minute:540,end_minute:1020,card_provider:'ejh',plan_code:'pro20',source:'purchase'},
 {start_minute:1020,end_minute:1250,blocked:true,source:'blocked'},
 {start_minute:1250,end_minute:120,card_provider:'ejh',plan_code:'pro5',source:'purchase'},
]}})
const context=vm.createContext({computed,proPlanName,defineProps:()=>props})
vm.runInContext(code+'\nglobalThis.exposed={current,currentIndex,next,range,rule,beijingTime,hint,liveDemand,currentTitle,currentBlocked}',context)
const f=context.exposed
assert.equal(f.current.value.blocked,true)
assert.equal(f.rule(f.next.value),'EJH · 5x')
props.now=Date.parse('2026-10-19T20:50:00+08:00')
assert.equal(f.current.value.plan_code,'pro5','boundary must update without another request')
assert.equal(f.range(f.current.value),'20:50～次日02:00')
assert.equal(f.beijingTime.value,'20:50')
props.now=Date.parse('2026-10-20T00:00:00+08:00')
assert.equal(f.current.value.plan_code,'pro5','overnight current rule was lost')
props.now=Date.parse('2026-10-20T09:00:00+08:00')
assert.equal(f.current.value.plan_code,'pro20')
props.config.enabled=false
assert.match(f.hint.value,/新增已暂停/)
props.config.enabled=true;props.config.mode='observe'
assert.match(f.hint.value,/观察模式/)
props.timetable={periods:[{start_minute:0,end_minute:1440,card_provider:'nexovcc',plan_code:'pro20',source:'default'}]}
assert.equal(f.rule(f.current.value),'环球 · 20x','new saved configuration not applied')
assert.equal(f.range(f.current.value),'全天')
assert.equal(f.next.value,null)
console.log('Passed: timetable boundary updates, overnight rules, Beijing clock, saved changes and execution modes')

props.config={enabled:true,mode:'auto',version:2,check_seconds:5}
props.now=Date.parse('2026-10-21T03:00:00+08:00')
props.timetable={periods:[{start_minute:120,end_minute:540,blocked:true,high_demand:true,source:'blocked'}]}
props.demand={active:true,valid:true,allowed:false,phase:'waiting',version:2,total_used:23,monitor_used:8,fallback_used:15,valid_until:new Date(props.now+20000).toISOString(),windows:[{key:'today',start:'02:00',end:'09:00',threshold:20,confirmations:3,consecutive:2}]}
assert.match(f.currentTitle.value,/等待并发达标/)
assert.equal(f.liveDemand.value.total_used,23)
props.demand.allowed=true;props.demand.phase='allowed';props.demand.windows[0].consecutive=3
assert.equal(f.currentBlocked.value,false)
assert.match(f.currentTitle.value,/高并发允许补货/)
props.now+=21000
assert.equal(f.currentBlocked.value,true,'stale permission must not remain green')
assert.match(f.currentTitle.value,/数据待确认/)
props.now=Date.parse('2026-10-21T09:00:00+08:00')
assert.equal(f.liveDemand.value,null,'finished interval retained override')
console.log('Passed: high-demand progress, live permission, stale-state blocking and window end')
