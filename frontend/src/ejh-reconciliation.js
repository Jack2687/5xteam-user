import { summarizeCardAmountsUSD } from './ejh-card-funding.js'
import { authorizationGroups, tradeMoney } from './ejh-clearing.js'

// Issuer returns the original principal on card closure. Reversal differences
// are displayed for reconciliation only, never deducted as a cost.
export function authorizationDiscrepancies(cases=[],trades=[],cards=[],operations=[],inFlightKeys=[]){
 const shortfalls=[],totals=new Map(),pending=new Map(),completed=new Map()
 for(const g of authorizationGroups(trades)){
  if(g.invalid||!g.hasReversal||g.authorization<=g.returned+g.cleared)continue
  const difference=g.authorization-g.returned-g.cleared,closed=cards.find(c=>c.id===g.card_id)?.remote?.status==='2'
  shortfalls.push({card_id:g.card_id,currency:g.currency,reference:g.reference,amount:tradeMoney(difference),closed,inFlight:inFlightKeys.includes(g.card_id+':auth:'+g.reference+':'+g.currency)})
  totals.set(g.currency,(totals.get(g.currency)||0n)+difference)
  const target=closed?completed:pending;target.set(g.currency,(target.get(g.currency)||0n)+difference)
 }
 const rows=map=>[...map].map(([currency,n])=>({currency,amount:tradeMoney(n,2)}))
 return {shortfalls,rows:rows(totals),pendingRows:rows(pending),completedRows:rows(completed),shortfallUSD:summarizeCardAmountsUSD(shortfalls,cards,operations),pendingUSD:summarizeCardAmountsUSD(shortfalls.filter(r=>!r.closed),cards,operations),unaccountedUSD:summarizeCardAmountsUSD(shortfalls.filter(r=>!r.closed&&!r.inFlight),cards,operations),completedUSD:summarizeCardAmountsUSD(shortfalls.filter(r=>r.closed),cards,operations)}
}
