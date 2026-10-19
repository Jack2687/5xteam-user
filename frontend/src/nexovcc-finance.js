export const usdRate = 6.7
export const tradeStates = {PENDING:'已授权',COMPLETE:'已清算',REVERSED:'已撤销',REFUNDED:'已退款',DECLINED:'已拒绝',EXPIRED:'已过期'}
export const fundNames = {1000:'账户充值',1001:'账户提现',3001:'开卡入金',3002:'销卡退回',3003:'开卡费',4001:'卡片充值',4002:'卡片提现',4004:'消费退款',2003:'授权撤销费',2007:'交易手续费',2008:'结算手续费',2009:'小额交易费',4005:'跨境费',4006:'卡片充值费',8888:'退款手续费',5000:'充值费返还',5001:'开卡费返还',5003:'退款费返还',5004:'撤销费返还',5008:'交易费返还',5009:'结算费返还',5010:'小额交易费返还'}
export const feeKinds = [3003,2003,2007,2008,2009,4005,4006,8888,5001,5003,5004,5008,5009,5010]
export function cents(value) { return value===null||value===undefined||value===''||!Number.isFinite(Number(value)) ? 0 : Math.round(Number(value)*100) }
export function money(value) { return value===null||value===undefined||!Number.isFinite(value) ? '—' : (value/100).toFixed(2) }
export function rCents(usdCents) { return Math.sign(usdCents)*Math.round(Math.abs(usdCents)*67/10) }
export function rmb(usdCents) { return usdCents===null||usdCents===undefined ? '—' : money(rCents(usdCents)) }
export function caseAmounts(row) {
 const g=row.match?.group,known=g?.valid
 const cost=known?cents(g.cost):null, revenue=row.revenue_r==null?null:cents(row.revenue_r)
 return {paid:known?cents(g.payment):null,returned:known?cents(g.returned):null,difference:known?cents(g.difference):null,fees:known?cents(g.fees):null,cost,revenue,profit:cost===null||revenue===null?null:revenue-rCents(cost),closed:!!(g?.fully_returned||g?.settled)}
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
 const cost=sum(funds.filter(r=>feeKinds.includes(r.kind)&&r.currency==='USD'),r=>-cents(r.amount))+cardLoss+settlement+rechargeFee
 const linked=new Set(cases.map(c=>c.match?.group?.id).filter(Boolean))
 return {ready,gross,received:serial?0:sum(recharges,r=>cents(r.received)),rechargeFee,opening:fees[3003]+fees[5001],reversal:fees[2003]+fees[5004],transaction:fees[2007]+fees[5008],otherFees:Object.entries(fees).filter(([k])=>![3003,5001,2003,5004,2007,5008].includes(Number(k))).reduce((s,[,v])=>s+v,0),cardLoss,settlement,cost,revenue,profit:revenue-rCents(cost),
  paid:sum(valid,g=>cents(g.payment)),returned:sum(valid,g=>cents(g.returned)),pending:sum(valid.filter(g=>!g.fully_returned&&!g.settled),g=>Math.max(0,cents(g.difference))),
  unknownRevenue:cases.filter(c=>c.revenue_r==null).length,unmatchedCases:cases.filter(c=>!c.match).length,unmatched:groups.filter(g=>cents(g.payment)>0&&!linked.has(g.id)).length,unverified:groups.filter(g=>!g.valid).length,
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
