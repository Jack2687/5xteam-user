function timestamp(value){
 if(!value)return 0
 const s=String(value)
 return Date.parse(/^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d$/.test(s)?s.replace(' ','T')+'+08:00':s)||0
}
// Match the visible trade date; invalid/missing timestamps go to the bottom.
export function newestReturnTrades(rows=[]){
 const at=r=>timestamp(r.trade?.tradeTime)||timestamp(r.trade?.createdAt)
 return [...rows].sort((a,b)=>at(b)-at(a)||timestamp(b.trade?.createdAt)-timestamp(a.trade?.createdAt)||String(a.id||'').localeCompare(String(b.id||'')))
}
