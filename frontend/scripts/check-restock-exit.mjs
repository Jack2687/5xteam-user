import assert from 'node:assert/strict'
import {exitReport,exitMinutes,accountExitLabels} from '../src/restockExit.js'
import {decisionReport} from '../src/restockDecision.js'

const config={version:2,group_id:1,enabled:true,mode:'auto',exit_enabled:true,exit_minutes:5,trigger_percent:85,target_percent:75,confirmations:2}
const decision={version:2,demand_known:true,config,snapshot:{id:'exit',group_id:1},exit_need:1,exit:{valid:true,confirmed:true,pressure:true,need:1,current_capacity:100,after_capacity:50,observed_demand:60,demand:60,at_risk:1,earliest_minutes:4,confirmations:2,rows:[{id:1,depletion_minutes:7,exit_minutes:4,reason:'达到额度合并阈值'}],points:[]}}
const report=exitReport(decision)
assert.equal(report.status,'存在补货缺口')
assert.equal(report.after,50)
assert.equal(report.earliest,'4.0 分钟')
assert.equal(exitMinutes(0),'0.0 分钟')
assert.equal(exitMinutes(null),'暂不可估')
assert.equal(exitMinutes(NaN),'暂不可估')
assert.equal(exitReport(decision,config,true).after,null)
assert.equal(exitReport(decision,{...config,version:3}).valid,false)
assert.equal(exitReport(decision,{...config,group_id:9}).valid,false)
assert.equal(exitReport(decision,{...config,exit_enabled:false}).status,'规则未开启')
assert.equal(exitReport({...decision,no_restock_active:true}).status,'每日禁补中')
assert.equal(exitReport({...decision,exit_need:0,exit:{...decision.exit,confirmed:false}}).status,'等待连续确认')
assert.equal(exitReport({...decision,exit_need:0,exit:{...decision.exit,need:0}}).status,'在途已覆盖后续缺口')
assert.equal(exitReport({...decision,exit:{...decision.exit,demand_held:true,observed_demand:50}}).held,true)
assert.deepEqual(accountExitLabels(decision,{id:1}),{depletion:'7.0 分钟',exit:'4.0 分钟',reason:'达到额度合并阈值'})
assert.equal(accountExitLabels(decision,{id:1},true).exit,'数据过期')
assert.equal(accountExitLabels({}, {id:1,remaining_usd:8,rate_usd:2}).depletion,'4.0 分钟')
assert.equal(accountExitLabels({...decision,exit:{...decision.exit,rows:[{id:1,warning_only:true}]}},{id:1}).exit,'额度接近阈值')
const audit=decisionReport(JSON.parse(JSON.stringify(decision)))
assert.match(audit.trigger,/账号退出前补货/)
assert.match(audit.rules.find(r=>r.key==='exit').evidence,/退出后容量 50，采用需求 60/)
assert.match(audit.steps[0],/最大值：1 个，不累加/)
console.log('Passed: exit forecasts, confirmations, pending coverage, stale config/group protection and historical evidence')
