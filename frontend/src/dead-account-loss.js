import { accountAmounts } from './ejh-account-amounts.js'
import { casePaymentTrades, summarizeClearingCosts } from './ejh-clearing.js'
import { summarizeCardAmountsUSD } from './ejh-card-funding.js'

// Only confirmed payments form principal loss. Keep actual refunds, fees and
// card balances independent; later receipts reduce this derived loss again.
export function ejhDeadLoss(data = {}) {
 const rows=[],seen=new Set(),cards=data.cards||[],operations=data.operations||[],trades=data.trades||[]
 for(const row of data.cases||[]){
  if(!row.pro_dead||row.payment_status!=='success')continue
  const card_id=row.ejh_card_id||row.card_id,raw=casePaymentTrades(row,trades)
  const key=card_id+':'+(raw.find(r=>r.trade?.authCode)?.trade.authCode||row.charge?.trade_no||row.id)
  if(seen.has(key))continue;seen.add(key)
  const a=accountAmounts(row,trades)
  const posted=raw.find(r=>['4','19'].includes(r.trade?.tradeType)&&r.trade.tradeStatus==='2'&&r.trade.billCurrency==='USD'&&Number(r.trade.billAmount)>0&&Number(r.trade.cardAmount)===Number(a.paid))
  const convert=(amount,currency)=>summarizeCardAmountsUSD([{card_id,amount,currency}],cards,operations,true)
  const paid=posted?convert(posted.trade.billAmount,'USD'):convert(a.paid,a.currency)
  const receiptIDs=new Set()
  const received=(row.returns||[]).filter(r=>r.confirmed&&(r.trade?.tradeType==='2'?['1','2'].includes(r.trade.tradeStatus):['5','16'].includes(r.trade?.tradeType)&&r.trade.tradeStatus==='2')).filter(r=>{const key=r.id||r.trade?.tradeNo;if(!key||receiptIDs.has(key))return false;receiptIDs.add(key);return true})
  const returned=summarizeCardAmountsUSD(received.map(r=>({card_id,amount:r.trade?.billCurrency==='USD'&&Number(r.trade.billAmount)>0?r.trade.billAmount:r.confirmed_amount,currency:r.trade?.billCurrency==='USD'&&Number(r.trade.billAmount)>0?'USD':r.confirmed_currency})),cards,operations,true)
  const missing=paid.missing||paid.amount==null||a.invalid||returned.missing
  const amount=missing?null:Math.max(0,Math.round((Number(paid.amount)-Number(returned.amount||0))*100))
  const clearing=summarizeClearingCosts(raw,cards,operations,[row])
  const alreadyCost=amount==null?0:Math.min(amount,Math.max(0,Math.round(Number(clearing.usd.amount||0)*100)))
  rows.push({id:row.id,email:row.email,card_id,at:row.pro_dead.at,amount,alreadyCost,missing:!!missing,estimated:paid.estimated||returned.estimated})
 }
 return {rows,amount:rows.reduce((n,r)=>n+(r.amount||0),0),alreadyCost:rows.reduce((n,r)=>n+r.alreadyCost,0),missing:rows.filter(r=>r.missing).length,estimated:rows.some(r=>r.estimated)}
}
