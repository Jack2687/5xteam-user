import assert from 'node:assert/strict'
import {restockDisplay} from '../src/restockDisplay.js'
import {restockMonitorStatus} from '../src/restockMonitor.js'
const at=Date.parse('2026-10-27T05:18:26Z'),now=at+39000
const config={version:139,group_id:4,fresh_seconds:20,capacity_percent:100,money_window_minutes:1}
const snapshot={id:'s',group_id:4,binding:'pool',at:new Date(at).toISOString(),valid:true,load_valid:true,accounts:[{id:1,ready:true,capacity:60,used:48}]}
const decision={version:139,snapshot,config,supply_minutes:10,money:{state:'ready',window_minutes:1,valid_until:new Date(at+120000).toISOString(),remaining_usd:100,rate_usd:10,rows:[{id:1,remaining_usd:100,rate_usd:10}]}}
const state={config,snapshot,decision,monitor:{running:true,phase:'evaluating'}}
const original=JSON.stringify(state)
let view=restockDisplay(state,now)
assert.equal(view.historical,true)
assert.equal(view.concurrency.used,48)
assert.equal(view.concurrency.capacity,60)
assert.equal(view.usable,1)
assert.equal(view.forecast.remaining,100)
assert.equal(view.forecast.supply,'10.0 分钟')
assert.equal(view.decisionUnavailable,false)
assert.equal(restockMonitorStatus(state,now).unavailable,true,'display must not change admission freshness')
assert.equal(JSON.stringify(state),original,'display must not renew or modify data')
view=restockDisplay({...state,config:{...config,group_id:5}},now)
assert.equal(view.concurrency.used,null)
assert.equal(view.decisionUnavailable,true)
assert.equal(view.forecast.remaining,null)
view=restockDisplay({...state,snapshot:{...snapshot,valid:false}},now)
assert.equal(view.concurrency.used,null)
view=restockDisplay(state,at+3000)
assert.equal(view.historical,false)
assert.equal(view.concurrency.used,48)
console.log('Passed: historical display remains visible, timestamp preserved, wrong scope hidden, admission still rejects expired data')
