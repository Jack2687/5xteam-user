import assert from 'node:assert/strict'
import { forecastDetails, supplyLabel } from '../src/restockForecast.js'
const now=Date.now(),at=new Date(now).toISOString()
const window={seconds:18000,reset_at:Math.floor(now/1000)+3600,used:25,rate:3,observed_rate:0,sample_seconds:60,samples:3,at}
const fixture=()=>({supply_minutes:60,supply_capped:true,at,config:{quota_seconds:60,fresh_seconds:20},snapshot:{valid:true,load_valid:true,at,new_plan:'pro',accounts:[{id:1,ready:true,plan:'pro',windows:[{...window}]}]}})
let d=fixture(),r=forecastDetails(d,now)
assert.equal(r.primary.observed,0);assert.equal(r.primary.rate,3);assert.equal(r.primary.remaining,75);assert.match(r.supply,/≥/)
assert.equal(supplyLabel({...d,supply_capped:false}),'60.0 分钟')
assert.equal(supplyLabel({supply_minutes:null}),'暂不可估')
d.snapshot.accounts[0].windows[0].observed_rate=undefined
assert.equal(forecastDetails(d,now).primary.observed,null)
r=forecastDetails(d,now+21000);assert.equal(r.stale,true);assert.equal(r.primary,null)
d=fixture();d.snapshot.accounts.push({id:2,ready:true,plan:'pro',windows:[]})
r=forecastDetails(d,now);assert.equal(r.coverage,50);assert.equal(r.primary,null);assert.equal(r.partial,true)
d=fixture();d.snapshot.accounts.push({id:2,ready:true,plan:'pro',windows:[{...window,seconds:604800}]})
r=forecastDetails(d,now);assert.equal(r.primary,null);assert.equal(r.partial,true)
d=fixture();d.snapshot.accounts[0].windows.push({...window});assert.equal(forecastDetails(d,now).cohorts.length,0)
d=fixture();d.snapshot.accounts[0].windows[0].reset_at=Math.floor(now/1000);assert.equal(forecastDetails(d,now).cohorts.length,0)
d=fixture();d.snapshot.accounts[0].plan='other';assert.equal(forecastDetails(d,now).cohorts.length,0)
console.log('Passed: observed vs forecast rates, missing/zero/stale/reset samples, partial cohorts, capped vs exact horizon')
