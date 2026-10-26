import assert from 'node:assert/strict'
import {restockMonitorStatus} from '../src/restockMonitor.js'

const now=Date.parse('2026-10-26T07:30:12Z')
const state={config:{group_id:4,fresh_seconds:20,enabled:true},snapshot:{at:'2026-10-26T07:29:50Z',valid:true,load_valid:true,error:'',accounts:[{id:1}]}}
let status=restockMonitorStatus(state,now)
assert.equal(status.kind,'stale')
assert.equal(status.unavailable,true)
assert.match(status.message,/22 秒.*20 秒/)
assert.doesNotMatch(status.title+status.message,/检查连接|选择监控/)
state.monitor={running:true,phase:'collecting',started_at:'2026-10-26T07:30:08Z'}
status=restockMonitorStatus(state,now)
assert.equal(status.unavailable,true,'collection must not renew an expired sample')
assert.match(status.message,/采集并发中.*4 秒/)
state.snapshot.at='2026-10-26T07:30:10Z'
state.monitor.phase='reconciling'
status=restockMonitorStatus(state,now)
assert.equal(status.kind,'collecting')
assert.equal(status.unavailable,false,'fresh collection can be displayed before evaluation')
state.monitor={running:false,error:'context deadline exceeded',next_check_at:'2026-10-26T07:30:17Z'}
status=restockMonitorStatus(state,now)
assert.equal(status.kind,'error')
assert.equal(status.unavailable,true)
assert.equal(status.retry,5)
assert.match(status.message,/deadline/)
delete state.monitor
state.snapshot.at='0001-01-01T00:00:00Z'
assert.equal(restockMonitorStatus(state,now).kind,'empty')
state.monitor={running:true,phase:'preparing',started_at:new Date(now).toISOString()}
assert.equal(restockMonitorStatus(state,now).title,'正在首次采集')
state.snapshot={at:new Date(now).toISOString(),valid:true,load_valid:false}
assert.equal(restockMonitorStatus(state,now).kind,'incomplete')
state.snapshot.load_valid=true
delete state.monitor
assert.equal(restockMonitorStatus(state,now).kind,'ready')
state.snapshot.at=new Date(now+10000).toISOString()
assert.match(restockMonitorStatus(state,now).message,/服务器时钟/)
console.log('Restock monitor: expiry, collection, errors, recovery and clock checks passed')
