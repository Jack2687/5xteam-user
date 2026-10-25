export const usdRate = 6.7
export const tradeStates = {PENDING:'已授权',COMPLETE:'已清算',REVERSED:'已撤销',REFUNDED:'已退款',DECLINED:'已拒绝',EXPIRED:'已过期'}
export const fundNames = {'api-merchant':'API 收费',1000:'账户充值',1001:'账户提现',3001:'开卡入金',3002:'销卡退回',3003:'开卡费',3004:'卡服务费（月）',3005:'卡服务费（年）',4001:'卡片充值',4002:'卡片提现',4004:'消费退款',2003:'授权撤销费',2007:'交易手续费',2008:'结算手续费',2009:'小额交易费',4005:'跨境费',4006:'卡片充值费',8888:'退款手续费',5000:'充值费返还',5001:'开卡费返还',5003:'退款费返还',5004:'撤销费返还',5008:'交易费返还',5009:'结算费返还',5010:'小额交易费返还'}
export const feeKinds = [3003,3004,3005,2003,2007,2008,2009,4005,4006,8888,5001,5003,5004,5008,5009,5010]
export function cents(value) { return value===null||value===undefined||value===''||!Number.isFinite(Number(value)) ? 0 : Math.round(Number(value)*100) }
export function money(value) { return value===null||value===undefined||!Number.isFinite(value) ? '—' : (value/100).toFixed(2) }
export function rCents(usdCents) { return Math.sign(usdCents)*Math.round(Math.abs(usdCents)*67/10) }
export function rmb(usdCents) { return usdCents===null||usdCents===undefined ? '—' : money(rCents(usdCents)) }
export function manualOtherFees(data,serial='') { return (data.groups||[]).filter(g=>(!serial||g.serial_no===serial)&&g.other_expense?.applied).map(g=>({...g.other_expense,serial_no:g.serial_no,group_id:g.id})) }
export function ledgerFunds(data) {
 return [...(data.finance?.funds||[]),...(data.merchant_fees||[]).map(r=>({id:'merchant:'+r.request_id,kind:'api-merchant',serial_no:(data.cards||[]).find(c=>c.card.id===r.card_id)?.card.serial_no||'',amount:'-'+r.amount,currency:'USD',at:r.at,link:r.request_id,external:true,reason:r.email+' · API 商户最终成功'})),...manualOtherFees(data).map(r=>({id:'review:'+r.group_id,kind:'manual-other',serial_no:r.serial_no,last4:(data.finance?.trades||[]).find(t=>t.serial_no===r.serial_no)?.last4,amount:'-'+r.amount,currency:'USD',at:r.updated_at,link:r.group_id,reason:r.reason,manual:true})),
  ...balanceCostAdjustments(data).filter(r=>r.extra>0).map(r=>({id:'balance:'+r.serial,kind:'balance-difference',serial_no:r.serial,last4:r.last4,amount:'-'+money(r.extra),currency:'USD',at:r.at,estimated:true,reason:`累计入金 ${money(r.funded)} − 已提现 ${money(r.withdrawn)} − 销卡退回 ${money(r.closedReturn)} − 卡余额 ${money(r.balance)} − 已计卡内损耗及清算 ${money(r.knownLoss)} = ${money(r.extra)} USD${r.pendingAdjustment?'；余额先恢复，退款流水仍待确认':''}`}))]
}

// Display-only estimate, never refund evidence or an admission rule. Recompute
// from the original ledger each time so later posted losses replace this amount.
export function balanceCostAdjustments(data, serial='') {
 const f=data.finance||{},time=v=>v&&!String(v).startsWith('0001')?Date.parse(v):NaN
 const amount=v=>v!==null&&v!==undefined&&String(v).trim()!==''&&Number.isFinite(Number(v))
 const moneyActions=['open','deposit','withdraw','withdraw_all','withdraw_cancel','cancel']
 if(data.finance_error||f.error)return []
 const result=[]
 for(const row of data.cards||[]){
  const card=row.card
  if(!card||serial&&card.serial_no!==serial||card.error)continue
  const s=card.serial_no,closed=card.status==='4'
  const balanceAt=time(card.balance_at),ledgerAt=time(f.card_at?f.card_at[s]:f.at)
  const funds=(f.funds||[]).filter(r=>r.serial_no===s),groups=(data.groups||[]).filter(g=>g.serial_no===s)
  const ops=(data.operations||[]).filter(op=>(op.card_id===card.id||op.serial_no===s)&&moneyActions.includes(op.action))
  if(!Number.isFinite(ledgerAt)||!funds.some(r=>r.kind===3001)||!groups.some(g=>cents(g.payment)>0)||
   funds.some(r=>r.currency!=='USD'||!fundNames[r.kind]||!amount(r.amount)||!Number.isFinite(time(r.at)))||
   groups.some(g=>!g.valid||g.other_expense?.applied||!amount(g.payment)||!amount(g.returned)||!Number.isFinite(time(g.at)))||
   ops.some(op=>!['succeeded','failed'].includes(op.status))||
   (data.trade_reviews||[]).some(r=>!r.voided&&(data.review_groups||[]).some(g=>g.id===r.group_id&&g.serial_no===s)))continue
  const transfers=funds.filter(r=>[3001,4001,4002,3002].includes(r.kind))
  if(transfers.some(r=>[3001,4001].includes(r.kind)?cents(r.amount)>0:cents(r.amount)<0))continue
  const sum=(rows,fn)=>rows.reduce((total,r)=>total+fn(r),0)
  const funded=-sum(funds.filter(r=>[3001,4001].includes(r.kind)),r=>cents(r.amount))
  const withdrawn=sum(funds.filter(r=>r.kind===4002),r=>cents(r.amount))
  const closures=funds.filter(r=>r.kind===3002),closedReturn=sum(closures,r=>cents(r.amount))
  const pending=groups.filter(g=>cents(g.payment)>0&&!g.fully_returned&&!g.settled)
  const knownLoss=sum(groups.filter(g=>g.fully_returned||g.settled),g=>cents(g.payment)-cents(g.returned))
  let balance=0,pendingAdjustment=0,at=card.balance_at
  if(closed){
   // A deletion alone or an uncredited residual is never a new cost. Require
   // the existing successful close + its actual final same-card receipt.
   if(pending.length||!closures.some(r=>ops.some(op=>['cancel','withdraw_cancel'].includes(op.action)&&op.status==='succeeded'&&op.evidence==='fund:'+r.id))||
    !Number.isFinite(time(data.balance?.at))||closures.some(r=>time(r.at)>time(data.balance.at)))continue
   at=closures.reduce((a,r)=>time(r.at)>time(a)?r.at:a,closures[0].at)
  }else{
   const transferred=ops.some(op=>['withdraw','withdraw_all'].includes(op.action)&&op.status==='succeeded'&&
    time(f.funds_at?.[s])>=time(op.created_at)&&funds.some(r=>r.kind===4002&&op.evidence==='fund:'+r.id&&
     cents(r.amount)===cents(op.amount)&&cents(card.balance)+cents(r.amount)===cents(op.before_balance)))
   if(!amount(card.balance)||!Number.isFinite(balanceAt)||ledgerAt<balanceAt&&!transferred||closedReturn||
    funds.some(r=>time(r.at)>balanceAt)||groups.some(g=>time(g.at)>balanceAt||time(g.returned_at)>balanceAt)||
    ops.some(op=>op.status==='succeeded'&&time(op.updated_at)>balanceAt))continue
   balance=cents(card.balance)
   if(balance<0)continue
   if(pending.length){
    const cases=(data.cases||[]).filter(c=>c.card_id===card.id&&c.payment_status==='success')
    if(pending.some(g=>!cases.some(c=>c.match?.group?.id===g.id&&c.state==='waiting'&&!c.removed_at&&Number.isFinite(time(c.requested_at))&&time(c.requested_at)<=balanceAt)))continue
    const outstanding=sum(pending,g=>Math.max(0,cents(g.payment)-cents(g.returned)))
    // The ordinary unreturned authorization stays in transit. Only an excess
    // over its ledger balance enables this explicitly provisional estimate.
    if(balance-(funded-withdrawn-knownLoss-outstanding)<=1)continue
    pendingAdjustment=outstanding
   }
  }
  const extra=Math.max(0,funded-withdrawn-closedReturn-balance-knownLoss)
  if(extra||pendingAdjustment)result.push({serial:s,last4:card.last4,funded,withdrawn,closedReturn,balance,knownLoss,extra,pendingAdjustment,at})
 }
 return result
}
export function caseAmounts(row) {
 const g=row.match?.group,known=g?.valid
 const shared=row.shared_fees
 const opening=shared?.ready?cents(shared.opening):null,monthly=shared?.ready?cents(shared.monthly):null
 const merchant=cents(row.merchant_fee_usd)
 const cost=known&&(!shared||shared.ready)?cents(g.cost)+(opening||0)+(monthly||0)+merchant:null, revenue=row.revenue_r==null?null:cents(row.revenue_r)
 return {opening,monthly,merchant,sharedCount:shared?.count||0,sharedApplied:shared?.applied,paid:known?cents(g.payment):null,returned:known?cents(g.returned):null,difference:known?cents(g.difference):null,fees:known?cents(g.fees):null,cost,revenue,profit:cost===null||revenue===null?null:revenue-rCents(cost),closed:!!(g?.fully_returned||g?.settled)}
}
export function financeTotals(data,serial='') {
 const f=data.finance||{}, ready=!!f.at&&!f.at.startsWith('0001')
 const groups=(data.groups||[]).filter(g=>!serial||g.serial_no===serial)
 const funds=(f.funds||[]).filter(r=>!serial||r.serial_no===serial)
 const cardIDs=new Set((data.cards||[]).filter(r=>!serial||r.card.serial_no===serial).map(r=>r.card.id))
 const cases=(data.cases||[]).filter(c=>(!serial||cardIDs.has(c.card_id))&&c.payment_status==='success')
 const recharges=(f.recharges||[]).filter(r=>r.status==='1'&&r.currency==='USD')
 const sum=(rows,fn)=>rows.reduce((a,r)=>a+fn(r),0)
 const fees=Object.fromEntries(feeKinds.map(k=>[k,-sum(funds.filter(r=>r.kind===k&&r.currency==='USD'),r=>cents(r.amount))]))
 const gross=serial?0:sum(recharges,r=>cents(r.amount)),rechargeFee=serial?0:Math.round(gross/100)
 const valid=groups.filter(g=>g.valid),revenue=sum(cases,c=>cents(c.revenue_r))
 const cardLoss=sum(valid.filter(g=>g.fully_returned),g=>cents(g.difference))
 const settlement=sum(valid.filter(g=>g.settled&&!g.fully_returned),g=>cents(g.difference))
 const manualFees=sum(manualOtherFees(data,serial),r=>cents(r.amount))
 const adjustments=balanceCostAdjustments(data,serial),balanceDifference=sum(adjustments,r=>r.extra),balanceRecoveredPending=sum(adjustments,r=>r.pendingAdjustment)
 const merchantFee=sum((data.merchant_fees||[]).filter(r=>!serial||cardIDs.has(r.card_id)),r=>cents(r.amount))
 const cost=merchantFee+sum(funds.filter(r=>feeKinds.includes(r.kind)&&r.currency==='USD'),r=>-cents(r.amount))+cardLoss+settlement+rechargeFee+manualFees+balanceDifference
 const linked=new Set(cases.map(c=>c.match?.group?.id).filter(Boolean))
 return {ready,gross,merchantFee,received:serial?0:sum(recharges,r=>cents(r.received)),rechargeFee,opening:fees[3003]+fees[5001],service:fees[3004]+fees[3005],reversal:fees[2003]+fees[5004],transaction:fees[2007]+fees[5008],otherFees:Object.entries(fees).filter(([k])=>![3003,3004,3005,5001,2003,5004,2007,5008].includes(Number(k))).reduce((s,[,v])=>s+v,0)+manualFees,manualFees,cardLoss,settlement,balanceDifference,balanceRecoveredPending,cost,revenue,profit:revenue-rCents(cost),
  paid:sum(valid,g=>cents(g.payment)),returned:sum(valid,g=>cents(g.returned)),pending:sum(valid.filter(g=>!g.fully_returned&&!g.settled),g=>Math.max(0,cents(g.difference))),
  unknownRevenue:cases.filter(c=>c.revenue_r==null).length,unmatchedCases:cases.filter(c=>!c.match).length,unmatched:groups.filter(g=>!g.non_pro&&!g.other_expense?.applied&&cents(g.payment)>0&&!linked.has(g.id)&&!linked.has(g.related_group_id)).length,unverified:groups.filter(g=>!g.valid&&!g.other_expense?.applied).length,
  staleReviews:(data.trade_reviews||[]).filter(r=>!r.voided&&!r.applied&&(!serial||(data.review_groups||[]).some(g=>g.id===r.group_id&&g.serial_no===serial))).length,
  foreign:funds.filter(r=>r.currency!=='USD').length+(serial?0:(f.recharges||[]).filter(r=>r.status==='1'&&r.currency!=='USD').length),
  unknownFunds:funds.filter(r=>!fundNames[r.kind]).length}
}
export function inDateRange(value,from,to){
 if(!from&&!to)return true
 const t=new Date(value).getTime()
 const start=from?new Date(from+'T00:00:00+08:00').getTime():-Infinity
 const end=to?new Date(to+'T23:59:59.999+08:00').getTime():Infinity
 return Number.isFinite(t)&&t>=start&&t<=end
}
