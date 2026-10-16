import assert from 'node:assert/strict'
import { forecastDetails, supplyLabel, concurrencyDetails, moneyLabel, rateLabel } from '../src/restockForecast.js'
const now = Date.now(), at = new Date(now).toISOString()
const fixture = () => ({supply_minutes:26.641, config:{fresh_seconds:20}, snapshot:{valid:true,load_valid:true,at,accounts:[{id:1,ready:true,capacity:300,used:228}]},money:{state:'ready',coverage:100,remaining_usd:266.41,rate_usd:10,valid_until:new Date(now+120000).toISOString(),rows:[{id:1,total_usd:532.82,used_usd:266.41,remaining_usd:266.41,rate_usd:10}]}})
let d=fixture(), r=forecastDetails(d,now)
assert.equal(r.supply,'26.6 分钟')
assert.equal(r.summary,'剩余 $266.41 · 消耗 $10/分钟')
assert.equal(r.rateText,'$10/分钟')
assert.equal(moneyLabel(0),'$0.00')
assert.equal(rateLabel(0),'$0/分钟')
assert.equal(moneyLabel(null),'—')
assert.equal(rateLabel(null),'采样中')
assert.equal(supplyLabel({supply_minutes:60}),'采样中', 'old percentage records must not masquerade as dollar estimates')
for (const [state,label] of Object.entries({sampling:'采样中',missing:'数据不足',idle:'暂无消耗',stale:'数据过期',empty:'暂无可用账号'})) {
 d=fixture();d.money.state=state
 if(state==='idle') {d.money.rate_usd=0;d.supply_minutes=null}
 assert.equal(forecastDetails(d,now).supply,label)
}
for (const change of [d=>d.snapshot.valid=false,d=>d.snapshot.load_valid=false,d=>d.snapshot.at=new Date(now-21000).toISOString(),d=>d.snapshot.at=new Date(now+6000).toISOString(),d=>d.money.valid_until=at]) {
 d=fixture();change(d);r=forecastDetails(d,now)
 assert.equal(r.supply,'数据过期');assert.equal(r.rate,null);assert.equal(r.rows[0].remaining_usd,null)
}
d=fixture();d.snapshot.accounts.push({id:2,ready:true})
assert.equal(forecastDetails(d,now).supply,'采样中','new member must invalidate old aggregate')
d=fixture();d.snapshot.accounts[0].expires_at=at
assert.equal(forecastDetails(d,now).supply,'采样中')
d=fixture();d.snapshot.accounts[0].plan='different';d.snapshot.accounts[0].force_merge_deadline=at;d.snapshot.lifetime_minutes=1
assert.equal(forecastDetails(d,now).supply,'26.6 分钟','Pro deadline or purchase plan changed existing supply')
d=fixture();d.supply_minutes=900;d.supply_capped=true
assert.equal(forecastDetails(d,now).supply,'900.0 分钟','old horizon cap must not alter dollar forecast')
d=fixture();d.money.rate_usd=Infinity
assert.equal(forecastDetails(d,now).rate,null)
d=fixture();d.snapshot.accounts.push({id:2,ready:false,capacity:100,used:100},{id:3,ready:true,shared:true,capacity:100,used:100},{id:4,ready:true,expires_at:at,capacity:100,used:100})
d.snapshot.accounts[0].waiting=99
assert.deepEqual(concurrencyDetails(d.snapshot,false,now),{used:228,capacity:300,percent:'76%'})
assert.equal(concurrencyDetails(d.snapshot,true,now).percent,'—')
d.snapshot.accounts[0].capacity=0
assert.equal(concurrencyDetails(d.snapshot,false,now).percent,'—')
d.snapshot.accounts[0].capacity=100;d.snapshot.accounts[0].used=120
assert.equal(concurrencyDetails(d.snapshot,false,now).percent,'120%')
d.snapshot.accounts[0].used=null
assert.equal(concurrencyDetails(d.snapshot,false,now).percent,'—')
console.log('Passed: dollar forecast, real zero, missing/stale samples, group changes, lifetime independence, matching concurrency cohorts')

d=fixture();assert.equal(forecastDetails(d,now).windowMinutes,3)
d.config.money_window_minutes=5
r=forecastDetails(d,now)
assert.equal(r.windowMinutes,5);assert.equal(r.supply,'采样中');assert.equal(r.rate,null);assert.equal(r.rows[0].rate_usd,null)
d.money.window_minutes=5
assert.equal(forecastDetails(d,now).supply,'26.6 分钟')
const historical=structuredClone(d);historical.config.money_window_minutes=1;historical.money.window_minutes=1
assert.equal(forecastDetails(historical,now).windowMinutes,1)
console.log('Passed: window defaults, changed-window suppression and historical labels')
