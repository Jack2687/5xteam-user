import { summarizeCardAmountsUSD } from './ejh-card-funding.js'

export function tradeUnits(value){
 const s=String(value??'');if(!/^\d{1,12}(\.\d{1,6})?$/.test(s))return null
 const [a,b='']=s.split('.');return BigInt(a)*1000000n+BigInt(b.padEnd(6,'0'))
}
export function tradeMoney(value,decimals=6){
 const n=value<0n?-value:value,divisor=10n**BigInt(6-decimals),rounded=(n+divisor/2n)/divisor,scale=10n**BigInt(decimals)
 return (value<0n?'-':'')+String(rounded/scale)+'.'+String(rounded%scale).padStart(decimals,'0')
}
export function uniqueTrades(trades=[]){
 const unique=new Map()
 for(const r of trades){
  const key=(r.card_id||r.trade?.orderNo)+':'+(r.trade?.tradeNo||r.id)
  const old=unique.get(key)
  if(!old||(Date.parse(r.synced_at)||0)>=(Date.parse(old.synced_at)||0))unique.set(key,r)
 }
 return [...unique.values()]
}
export function authorizationGroups(trades=[]){
 const groups=new Map()
 for(const r of uniqueTrades(trades)){
  const t=r.trade||{},n=tradeUnits(t.cardAmount)
  if(!r.card_id||!t.authCode||!['1','2','4','5','16','19'].includes(t.tradeType)||!['1','2'].includes(t.tradeStatus)||!(/^[A-Z]{3}$/).test(t.cardCurrency))continue
  const key=r.card_id+':'+t.authCode+':'+t.cardCurrency
  const g=groups.get(key)||{card_id:r.card_id,currency:t.cardCurrency,authorization:0n,cleared:0n,returned:0n,refunded:0n,hasClearing:false,hasReversal:false,reference:t.authCode,records:[],invalid:false}
  g.records.push(r)
  if(n===null){g.invalid=true;groups.set(key,g);continue}
  if(t.tradeType==='1')g.authorization=g.authorization>n?g.authorization:n
  if(['4','19'].includes(t.tradeType)&&t.tradeStatus==='2'){g.hasClearing=true;g.cleared+=n}
  if(t.tradeType==='2'){g.hasReversal=true;g.returned+=n}
  if(['5','16'].includes(t.tradeType)&&t.tradeStatus==='2')g.refunded+=n
  groups.set(key,g)
 }
 return [...groups.values()]
}
const matchesCase=(row,group)=>group.card_id===(row?.ejh_card_id||row?.card_id)&&group.records.some(r=>row?.charge?.id&&r.id===row.charge.id||row?.charge?.trade_no&&r.trade?.tradeNo===row.charge.trade_no)
// A full capture with a recorded refund request is recoverable principal.
// Small partial settlements remain expenses; no refund is invented from balance.
export function clearingAwaitsRefund(row,group){
 return !row?.payment_only&&row?.state==='waiting'&&!!row.requested_at&&!String(row.requested_at).startsWith('0001')&&matchesCase(row,group)&&!group.invalid&&group.authorization>0n&&group.cleared===group.authorization&&group.refunded<group.cleared
}
export function remainingAuthorization(row,groups){
 const c=row?.charge,card=row?.ejh_card_id||row?.card_id
 const group=groups.find(g=>matchesCase(row,g))
 if(!group)return c?.type==='4'?{missing:true}:null
 if(group.invalid||group.hasClearing&&!group.authorization)return {missing:true}
 if(!group.hasClearing&&!group.hasReversal)return null
 const refundPending=clearingAwaitsRefund(row,group)
 const remaining=refundPending?group.cleared-group.refunded:group.authorization-group.cleared-group.returned
 let settled=0n,settlementMissing=false
 if(refundPending)for(const r of group.records){
  const t=r.trade;if(t.tradeStatus!=='2'||!['4','19','5','16'].includes(t.tradeType))continue
  const bill=tradeUnits(t.billAmount)
  if(t.billCurrency!=='USD'||bill===null||bill===0n&&tradeUnits(t.cardAmount)>0n)settlementMissing=true
  else settled+=['5','16'].includes(t.tradeType)?-bill:bill
 }
 return {amount:tradeMoney(remaining>0n?remaining:0n),currency:group.currency,key:card+':auth:'+group.reference+':'+group.currency,clearing:group.hasClearing,refundPending,settlementUSD:refundPending&&!settlementMissing?tradeMoney(settled>0n?settled:0n):null}
}
export function casePaymentTrades(row,trades=[]){
 const c=row?.charge,card=row?.ejh_card_id||row?.card_id
 if(!c)return []
 const all=uniqueTrades(trades),original=all.find(r=>r.card_id===card&&(c.id&&r.id===c.id||c.trade_no&&r.trade?.tradeNo===c.trade_no))
 if(original?.trade?.authCode)return all.filter(r=>r.card_id===card&&r.trade?.authCode===original.trade.authCode)
 if(original)return [original]
 return [{id:c.id,card_id:card,trade:{tradeNo:c.trade_no,tradeType:c.type,tradeStatus:c.status,cardAmount:c.card_amount,cardCurrency:c.card_currency,billAmount:c.bill_amount,billCurrency:c.bill_currency,tradeTime:c.trade_time}}]
}
export function summarizeClearingCosts(trades=[],cards=[],operations=[],cases=[]){
 const all=uniqueTrades(trades),totals=new Map(),entries=[]
 const deferred=new Set(authorizationGroups(all).filter(g=>cases.some(c=>clearingAwaitsRefund(c,g))).flatMap(g=>g.records.map(r=>r.id||r.trade?.tradeNo)))
 const cleared=new Set(all.filter(r=>['4','19'].includes(r.trade?.tradeType)&&r.trade?.tradeStatus==='2'&&r.trade?.authCode).map(r=>r.card_id+':'+r.trade.authCode))
 let count=0,refundCount=0,pending=0,missing=0,usd=0n,estimated=false
 for(const record of all){
  const t=record.trade||{},refund=['5','16'].includes(t.tradeType)&&cleared.has(record.card_id+':'+t.authCode)
  if(!['4','19'].includes(t.tradeType)&&!refund)continue
  if(deferred.has(record.id||t.tradeNo)&&t.tradeStatus==='2'){
   entries.push({record,amount:'0.00',currency:t.cardCurrency,usd:'0.00',incomplete:false,refund,refundPending:true});continue
  }
  if(t.tradeStatus!=='2'){pending++;entries.push({record,amount:null,currency:t.cardCurrency,usd:null,incomplete:true,pending:true,refund});continue}
  const n=tradeUnits(t.cardAmount),valid=n!==null&&/^[A-Z]{3}$/.test(t.cardCurrency||'')
  if(refund)refundCount++;else count++
  const sign=refund?-1n:1n
  if(valid)totals.set(t.cardCurrency,(totals.get(t.cardCurrency)||0n)+sign*n)
  const bill=tradeUnits(t.billAmount)
  let converted=t.billCurrency==='USD'&&bill!==null&&(bill>0n||n===0n)?bill:null,approximate=false
  if(converted===null&&valid){
   const v=summarizeCardAmountsUSD([{card_id:record.card_id,amount:t.cardAmount,currency:t.cardCurrency}],cards,operations,true)
   if(!v.missing&&v.amount!==null){converted=tradeUnits(v.amount);approximate=true}
  }
  const incomplete=!valid||converted===null
  if(incomplete)missing++;else usd+=sign*converted
  estimated ||= approximate
  entries.push({record,amount:valid?tradeMoney(sign*n):null,currency:t.cardCurrency,usd:converted===null?null:tradeMoney(sign*converted),incomplete,estimated:approximate,refund})
 }
 return {count,refundCount,pending,missing,estimated,rows:[...totals].map(([currency,n])=>({currency,amount:tradeMoney(n,2)})),usd:{amount:missing&&usd===0n?null:tradeMoney(usd,2),missing,estimated},entries}
}
