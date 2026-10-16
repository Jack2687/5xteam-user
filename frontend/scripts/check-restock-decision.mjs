import assert from 'node:assert/strict'
import {decisionReport} from '../src/restockDecision.js'

const base={id:'audit-1',at:'2026-10-16T12:00:00+08:00',version:3,demand_known:true,started:2,suggested:2,reason:'本轮已预留',
 config:{enabled:true,mode:'auto',group_id:7,time_enabled:true,trigger_minutes:10,target_minutes:15,early:true,forecast_minutes:60,concurrency_enabled:true,trigger_percent:85,target_percent:75,confirmations:2,reserve:0,empty_group_count:2,force_merge_enabled:false,force_merge_minutes:15,min_batch:1,max_batch:2,allow_small:true,max_inflight:6,hour_limit:20,day_limit:100,failure_limit:3},
 snapshot:{id:'snapshot-1',at:'2026-10-16T12:00:00+08:00',group_id:7,valid:true,load_valid:true,accounts:[{id:1}]},
 usable:1,capacity:20,used:100,waiting:0,inflight:3,confirmations:2,lead_minutes:2,trigger_minutes:10,target_minutes:15,supply_minutes:5,time_need:0,concurrency_need:5,reserve_need:0,empty_group_need:0,force_merge_need:0,
 money:{rate_usd:2,remaining_usd:10},limits:{hour_count:0,day_count:0,hour_cost:0,day_cost:0,failures:0},
 trace:{version:1,time_triggered:true,concurrency_triggered:true,new_account_capacity:20,group_inflight:2,timely_inflight:1,future_capacity:20,net_need:5,after_minimum:5,hard_limit:2,caps:[{key:'batch',remaining:2},{key:'inflight',remaining:3}],cooldown_seconds:60,cooldown_remaining_seconds:0,evaluation_reason:'净缺口取最大值',money_sizing:{new_budget_usd:100,pending:1,gap_usd:-80}}}
const report=decisionReport(base)
assert.match(report.trigger,/持续并发压力/)
assert.doesNotMatch(report.trigger,/预计供给不足/)
assert.match(report.rules[0].status,/条件达到/)
assert.match(report.rules[0].extra,/− 1 个在途 × 每号 100 USD/)
assert.match(report.rules[1].extra,/有效容量 20 及 1 个及时在途/)
assert.match(report.steps[0],/最大值：5 个，不累加/)
assert.equal(report.caps.find(c=>c.name==='每批上限').binding,true)
assert.equal(report.caps.find(c=>c.name==='在途余量').binding,false)
assert.equal(report.evaluationReason,'净缺口取最大值')
// The displayed thresholds come from the saved configuration, even years later.
assert.match(decisionReport(JSON.parse(JSON.stringify(base))).rules[1].evidence,/≥ 85%/)
const legacy={...base,trace:undefined}
assert.equal(decisionReport(legacy).legacy,true)
assert.equal(decisionReport(legacy).caps.length,0)
assert.doesNotMatch(decisionReport(legacy).rules[1].extra,/20 槽位/)
assert.equal(decisionReport({id:'configuration',config:base.config,snapshot:{}}).evaluation,false)
const bad=decisionReport({...base,demand_known:false,started:0,suggested:0,trace:{version:1,net_need:0},snapshot:{...base.snapshot,valid:false,load_valid:false,error:'采集失败'}})
assert.equal(bad.rules[0].status,'未评估')
assert.doesNotMatch(bad.trigger,/产生净缺口的规则/)
const paused=decisionReport({...base,no_restock_active:true,started:0,suggested:0})
assert.equal(paused.outcome,'本轮未新增补货')
assert.match(paused.steps.join(' '),/每日禁补已覆盖建议/)
const empty=decisionReport({...base,concurrency_need:0,empty_group_need:2,snapshot:{...base.snapshot,accounts:[]},trace:{...base.trace,concurrency_triggered:false,net_need:2,after_minimum:2,group_inflight:0}})
assert.match(empty.trigger,/空分组启动/)
assert.match(empty.steps[1],/不抬高到最小批量/)
assert.match(empty.rules[3].evidence,/实际 0 个账号/)
assert.equal(decisionReport({}).evaluation,false)
console.log('Passed: historical trigger reasons, sizing, caps, legacy/failed data, events and daily pause')
