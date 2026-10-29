function timestamp(value){
 if(!value)return 0
 const s=String(value)
 return Date.parse(/^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d$/.test(s)?s.replace(' ','T')+'+08:00':s)||0
}
// Issuer posting is when a return lands; transaction initiation can be earlier.
// Never substitute local sync/confirmation time for missing issuer timestamps.
export function returnPostedAt(row){
 return timestamp(row?.trade?.createdAt)||timestamp(row?.trade?.tradeTime)
}
// Match the visible date while preserving payment transaction ordering.
export function newestReturnTrades(rows=[]){
 const at=r=>['2','5','16'].includes(r.trade?.tradeType)?returnPostedAt(r):timestamp(r.trade?.tradeTime)||timestamp(r.trade?.createdAt)
 return [...rows].sort((a,b)=>at(b)-at(a)||timestamp(b.trade?.createdAt)-timestamp(a.trade?.createdAt)||String(a.id||'').localeCompare(String(b.id||'')))
}
