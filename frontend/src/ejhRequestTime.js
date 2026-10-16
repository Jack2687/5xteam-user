export function caseRequestTime(row={}) {
 const real=Date.parse(row.requested_at)
 if(Number.isFinite(real))return {at:row.requested_at,label:'申请'}
 const manual=row.state_override?.state==='waiting'?row.state_override.updated_at:null
 return {at:Number.isFinite(Date.parse(manual))?manual:undefined,label:manual?'人工标记':'申请'}
}
export function waitingRequestSummary(rows=[]) {
 const waiting=rows.filter(row=>row.state==='waiting')
 const dates=waiting.map(row=>({...caseRequestTime(row),email:row.email})).sort((a,b)=>(Date.parse(a.at)||Infinity)-(Date.parse(b.at)||Infinity))
 const first=dates[0]||{}
 return {count:waiting.length,at:first.at,label:waiting.length>1?'最早'+(first.label||'申请'):first.label||'申请',title:dates.map(row=>`${row.email||'账号'}：${row.label} ${row.at?new Date(row.at).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai',hour12:false}):'时间未记录'}`).join('\n')}
}
