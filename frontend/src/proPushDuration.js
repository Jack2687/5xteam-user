const timestamp = value => value && !String(value).startsWith('0001') && Number.isFinite(Date.parse(value)) ? Date.parse(value) : null
export function proPushDuration(account, now = Date.now()) {
 const start=timestamp(account.pro_push_started_at)
 if(start===null)return {minutes:null,frozen:false,end:null}
 const candidates=[]
 for(const stage of ['merge','deactivate']){
  candidates.push(account.pro_manual_stages?.[stage]?.started_at)
  for(const state of ['running','completed','failed'])candidates.push(account.pro_auto?.stage_times?.[stage+':'+state])
 }
 candidates.push(account.pro_deactivation?.started_at,account.pro_deactivation?.accepted_at,account.pro_deactivation?.confirmed_at,account.space_merged_at)
 const historical=candidates.map(timestamp).filter(v=>v!==null&&v>=start)
 const end=timestamp(account.pro_push_ended_at)??(historical.length?Math.min(...historical):null)
 const terminal=!!account.space_merged_once||!!account.pro_deactivation?.accepted_at||!!account.pro_deactivation?.confirmed_at
 return {minutes:terminal&&end===null?null:Math.max(0,Math.floor(((end??now)-start)/60000)),frozen:end!==null||terminal,end}
}
