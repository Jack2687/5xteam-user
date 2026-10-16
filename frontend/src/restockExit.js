const finite=n=>typeof n==='number'&&Number.isFinite(n)&&n>=0
export const exitMinutes=n=>finite(n)?n.toFixed(1)+' 分钟':'暂不可估'
export function exitReport(decision={},config=decision.config||{},unavailable=false){
 const f=decision.exit
 const sameVersion=decision.version==null||config.version==null||decision.version===config.version
 const sameGroup=decision.snapshot?.group_id==null||config.group_id==null||decision.snapshot.group_id===config.group_id
 const valid=!unavailable&&sameVersion&&sameGroup&&!!f?.valid&&!!decision.demand_known
 const active=config.exit_enabled
 const status=!active?'规则未开启':!valid?'等待有效监控数据':!config.enabled?'总开关关闭':config.mode!=='auto'?'观察模式':decision.no_restock_active?'每日禁补中':decision.exit_need>0?'存在补货缺口':f.pressure&&f.need===0?'在途已覆盖后续缺口':f.pressure&&!f.confirmed?'等待连续确认':'容量充足／未到提前时间'
 return {valid,status,earliest:valid?exitMinutes(f.earliest_minutes):'暂不可估',rows:valid?(f.rows||[]):[],points:valid?(f.points||[]):[],observed:valid?f.observed_demand:null,demand:valid?f.demand:null,current:valid?f.current_capacity:null,after:valid?f.after_capacity:null,need:valid?decision.exit_need:0,risk:valid?f.at_risk:0,held:valid&&f.demand_held,gap:valid&&f.transient_gap,unknown:valid?f.unknown:0}
}
export function accountExitLabels(decision,row,unavailable=false){
 if(unavailable)return {depletion:'数据过期',exit:'数据过期',reason:''}
 const r=decision.exit?.valid?decision.exit.rows?.find(a=>a.id===row.id):null
 const depletion=r?exitMinutes(r.depletion_minutes):!row.reason&&finite(row.remaining_usd)&&finite(row.rate_usd)&&row.rate_usd>0?exitMinutes(row.remaining_usd/row.rate_usd):row.rate_usd===0?'暂无消耗':'暂不可估'
 return {depletion,exit:r?.warning_only?'额度接近阈值':exitMinutes(r?.exit_minutes),reason:r?.reason||'尚无退出预测'}
}
