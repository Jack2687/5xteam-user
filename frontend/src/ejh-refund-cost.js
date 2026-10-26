import { uniqueTrades, tradeUnits, tradeMoney } from './ejh-clearing.js'
import { summarizeCardAmountsUSD } from './ejh-card-funding.js'
import { refundFeeEntries } from './ejh-refund-fees.js'

const refund=r=>['5','16'].includes(String(r.trade?.tradeType))
const validRate=value=>/^\d{1,3}(\.\d{1,2})?$/.test(String(value??''))&&Number(value)<=100
const centsText=n=>tradeMoney(n*10000n,2)

// Explicitly linked manual totals replace proportional estimates. Allocation
// by refund USD preserves the recorded total down to the cent.
export function applyRefundFeeOverrides(data){
 const trades=uniqueTrades(data.trades||[]),overrides=new Map()
 for(const fee of refundFeeEntries(data.refund_fees||[])){
  const amount=tradeUnits(fee.amount),ids=[...new Set(fee.trade_ids||[])]
  if(amount===null||!ids.length)continue
  const rows=ids.map(id=>trades.find(r=>r.id===id))
  if(rows.some(r=>!r||!refund(r)||r.trade.tradeStatus!=='2'||r.trade.billCurrency!=='USD'||!(tradeUnits(r.trade.billAmount)>0n)))continue
  const total=rows.reduce((s,r)=>s+tradeUnits(r.trade.billAmount),0n),cents=(amount+5000n)/10000n
  let allocated=0n
  rows.forEach((r,i)=>{
   const share=i===rows.length-1?cents-allocated:cents*tradeUnits(r.trade.billAmount)/total
   allocated+=share;overrides.set(r.id,{amount:centsText(share),fee_id:fee.id,allocated:rows.length>1})
  })
 }
 const attach=r=>{const {refund_fee_override,...clean}=r;return overrides.has(r.id)?{...clean,refund_fee_override:overrides.get(r.id)}:clean}
 return {...data,trades:trades.map(attach),cases:(data.cases||[]).map(c=>({...c,returns:(c.returns||[]).map(attach)}))}
}

export function summarizeRefundFees(records=[],percent=6,confirmedOnly=false,cards=[],operations=[]){
 let sum=0n,count=0,missing=0,pending=0,estimated=false
 const rates=new Set(),entries=[]
 for(const record of uniqueTrades(records).filter(refund)){
  const t=record.trade
  if(t.tradeStatus!=='2'||confirmedOnly&&!record.confirmed){pending++;continue}
  const rate=record.reversal_fee?record.reversal_fee.percent:percent
  const manual=record.refund_fee_override
  let amount=manual?tradeUnits(manual.amount):null,approximate=false
  if(!manual){
   if(!validRate(rate)){missing++;continue}
   let principal=t.billCurrency==='USD'?tradeUnits(t.billAmount):null
   if(principal===null||principal===0n&&tradeUnits(t.cardAmount)>0n){
    const converted=summarizeCardAmountsUSD([{card_id:record.card_id,order_no:t.orderNo,amount:t.cardAmount,currency:t.cardCurrency}],cards,operations,true)
    principal=converted.missing?null:tradeUnits(converted.amount);approximate=!!converted.estimated
   }
   if(Number(rate)===0)amount=0n
   else if(principal!==null)amount=(principal*BigInt(Math.round(Number(rate)*100))+50000000n)/100000000n*10000n
   rates.add(String(rate))
  }
  if(amount===null){missing++;continue}
  sum+=amount;count++;estimated ||= !manual||manual.allocated||approximate
  entries.push({record,amount:tradeMoney(amount,2),manual:!!manual,allocated:!!manual?.allocated,percent:rate,estimated:!manual||approximate})
 }
 return {count,missing,pending,estimated,entries,percent:rates.size===1?[...rates][0]:null,usd:{amount:count?tradeMoney(sum,2):missing||pending?null:'0.00',missing,estimated}}
}
