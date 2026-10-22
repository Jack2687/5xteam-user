import assert from 'node:assert/strict'
import { proPushDuration } from '../src/proPushDuration.js'
const start='2026-10-21T10:00:00+08:00',end='2026-10-21T10:25:00+08:00',later=Date.parse('2026-10-22T12:00:00+08:00')
assert.equal(proPushDuration({pro_push_started_at:start},Date.parse(end)).minutes,25)
for(const account of [
 {pro_push_ended_at:end},
 {pro_manual_stages:{merge:{started_at:end,status:'failed'}}},
 {pro_auto:{stage_times:{'deactivate:running':end}}},
 {pro_deactivation:{started_at:end,confirmed_at:'2026-10-21T11:00:00+08:00'}},
 {space_merged_once:true,space_merged_at:end},
]){
 const p={pro_push_started_at:start,...account}
 assert.equal(proPushDuration(p,later).minutes,25)
 assert.equal(proPushDuration(p,later+86400000).minutes,25)
 assert.equal(proPushDuration(p,later).frozen,true)
}
assert.equal(proPushDuration({pro_push_started_at:start,space_merged_once:true},later).minutes,null,'missing historic end must not keep increasing or invent a duration')
assert.equal(proPushDuration({pro_push_started_at:start,pro_push_ended_at:end,pro_manual_stages:{merge:{started_at:'2026-10-22T00:00:00Z'}}},later).minutes,25,'retry must not move persisted stop time')
assert.equal(proPushDuration({},later).minutes,null)
console.log('Passed: push duration stops at merge/deletion and remains fixed across retries and refreshes')
