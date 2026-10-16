import { summarizeReversalFees } from './ejh-reversal-fees.js'
import { summarizeCardBalancesUSD } from './ejh-card-funding.js'
import { summarizeInFlightPayments } from './ejh-account-amounts.js'

// Decimal cents for all USD/USDT arithmetic; R remains the downstream counter.
export function cents(value) {
  if (!/^-?\d{1,15}(\.\d{1,6})?$/.test(String(value ?? ''))) return null
  const negative=String(value).startsWith('-'),[whole,fraction='']=String(value).replace('-','').split('.')
  return (BigInt(whole)*100n+(BigInt(fraction.padEnd(6,'0'))+5000n)/10000n)*(negative?-1n:1n)
}
export function money(value) {
  if(value===null||value===undefined)return null
  const n=value<0n?-value:value
  return `${value<0n?'-':''}${n/100n}.${String(n%100n).padStart(2,'0')}`
}
const feeCents = value => {const n=cents(value);return n===null?null:(n+50n)/100n}
export function financeTime(value) {
  if(!value)return null
  const text=String(value),n=Date.parse(/^\d{4}-\d\d-\d\d[ T]\d\d:\d\d:\d\d(?:\.\d+)?$/.test(text)?text.replace(' ','T')+'+08:00':text)
  return Number.isFinite(n)&&n>0?n:null
}
export function beijingDay(value=Date.now()) {
  const n=typeof value==='number'?value:financeTime(value)
  return n===null?'':new Date(n+8*3600000).toISOString().slice(0,10)
}
export function consumptionSummary(cases=[],history=[]) {
  const accounts=new Map(),seen=new Set()
  for(const row of history){
    if(seen.has(row.id)||!Number.isFinite(row.total)||row.total<0)continue
    seen.add(row.id);const key=row.email?.toLowerCase()||row.id
    accounts.set(key,(accounts.get(key)||0)+row.total)
  }
  for(const row of cases){
    const key=row.email?.trim().toLowerCase()||row.id,n=row.consumption_total
    if(!accounts.has(key))accounts.set(key,null)
    if(typeof n==='number'&&Number.isFinite(n)&&n>=0)accounts.set(key,Math.max(accounts.get(key)||0,n))
  }
  let total=0,missing=0
  for(const n of accounts.values()){if(n===null)missing++;else total+=n}
  return {total:accounts.size&&missing===accounts.size?null:total,missing,count:accounts.size}
}
export function openingEntries(data) {
  const unique=new Map(),ops=data.operations||[]
  for(const card of data.cards||[]){
    if(card.issuer_linked===false||!card.remote?.orderNo)continue
    const op=ops.filter(o=>o.method==='open'&&['completed','resolved','import_pending'].includes(o.status)&&(o.order_no===card.remote.orderNo||o.card_id===card.id||o.result?.orderNo===card.remote.orderNo)).sort((a,b)=>(financeTime(a.created_at)||Infinity)-(financeTime(b.created_at)||Infinity))[0]
    const at=financeTime(op?.created_at||card.remote?.startTime)
    unique.set(card.remote.orderNo,{id:'open:'+card.remote.orderNo,kind:'opening',at,card:card.local?.name||card.remote.cardNo||card.remote.orderNo,reference:card.remote.orderNo,amount:'0.50',currency:'USDT',usd:null,note:at?'每张 0.50 USDT':'历史导入卡，开卡日期未记录'})
  }
  for(const op of ops){
    const order=op.order_no||op.result?.orderNo
    if(op.method!=='open'||!['completed','resolved','import_pending'].includes(op.status)||!order||unique.has(order))continue
    unique.set(order,{id:'open:'+order,kind:'opening',at:financeTime(op.created_at),card:order,reference:order,amount:'0.50',currency:'USDT',usd:null,note:'每张 0.50 USDT'})
  }
  return [...unique.values()]
}
export function financeOverview(data={}) {
  const cards=data.cards||[],operations=data.operations||[],cases=data.cases||[]
  const reversal=summarizeReversalFees(data.trades||[],data.reversal_fee_percent??6,false,cards,operations)
  const opening=openingEntries(data),consumption=consumptionSummary(cases,data.consumption_history||[])
  const gross=cents(data.recharge_ledger?.amount),deposit=feeCents(data.recharge_ledger?.amount),net=gross===null?null:gross-deposit
  const cardUSD=summarizeCardBalancesUSD(cards,operations),inFlight=summarizeInFlightPayments(cases,cards,operations)
  const reasons=[];let account=0n
  const balances=data.balance?.data?.abroadList
  if(!Array.isArray(balances)||!balances.length)reasons.push('EJH 账户余额未查询')
  else {
    const seen=new Set()
    for(const item of balances){
      const n=cents(item.balance)
      if(n===null||seen.has(item.currency)){reasons.push('EJH 账户余额待核实');continue}
      seen.add(item.currency)
      if(item.currency==='USD')account+=n
      else if(n!==0n)reasons.push('EJH 非 USD 余额缺少折算依据')
    }
  }
  if(net===null)reasons.push('充值总账未填写')
  if(cardUSD.missing)reasons.push(`${cardUSD.missing} 张卡余额或折算依据缺失`)
  if(inFlight.paymentUSD.missing)reasons.push(`${inFlight.paymentUSD.missing} 笔在途付款待核实`)
  const loss=reasons.length?null:net-account-(cents(cardUSD.amount)||0n)-(cents(inFlight.paymentUSD.amount)||0n)
  return {reversal,openingCount:opening.length,openingFee:money(BigInt(opening.length)*50n),consumption,
    gross:money(gross),depositFee:money(deposit),net:money(net),loss:money(loss),lossReasons:[...new Set(reasons)],
    estimated:!!(cardUSD.estimated||inFlight.paymentUSD.estimated),stale:cards.filter(c=>c.remote?.status!=='2'&&c.balance_error).length,
    accountUSD:money(account),cardUSD,inFlight}
}
export function periodRange(mode,date=beijingDay()) {
  const at=financeTime(date+'T00:00:00+08:00')
  if(mode==='all')return {start:-Infinity,end:Infinity,label:'全部记录'}
  if(at===null)return {start:Infinity,end:-Infinity,label:'日期无效'}
  let start=at,end=at+86400000,label=date
  if(mode==='week'){
    const weekday=new Date(at+8*3600000).getUTCDay()||7
    start=at-(weekday-1)*86400000;end=start+7*86400000;label=beijingDay(start)+' — '+beijingDay(end-1)
  }else if(mode==='month'){
    start=financeTime(date.slice(0,7)+'-01T00:00:00+08:00')
    const d=new Date(start+8*3600000);d.setUTCMonth(d.getUTCMonth()+1)
    end=d.getTime()-8*3600000;label=date.slice(0,7)
  }
  return {start,end,label}
}
export function financeEvents(data,history={}) {
  const events=openingEntries(data),seen=new Map(),cards=data.cards||[],operations=data.operations||[]
  for(const record of data.trades||[]){
    if(record.trade?.tradeType!=='2')continue
    const key=(record.card_id||record.trade.orderNo||'')+':'+(record.trade.tradeNo||record.id)
    const prev=seen.get(key)
    if(!prev||(financeTime(record.synced_at)||0)>=(financeTime(prev.synced_at)||0))seen.set(key,record)
  }
  for(const [key,record] of seen){
    const fee=summarizeReversalFees([record],data.reversal_fee_percent??6,false,cards,operations,true),t=record.trade
    events.push({id:'reversal:'+key,kind:'reversal',at:financeTime(t.tradeTime)||financeTime(t.createdAt),card:cards.find(c=>c.id===record.card_id)?.local?.name||t.orderNo,reference:t.tradeNo,
      email:(data.cases||[]).find(c=>c.id===record.payment_order_id)?.email||'',amount:fee.rows[0]?.amount??null,currency:fee.rows[0]?.currency||t.cardCurrency||'',usd:money(cents(fee.usd.amount)),usd_precise:fee.usd.amount,
      incomplete:!!(fee.missing||fee.pending||fee.usd.missing),note:fee.pending?'待确认，未计入':`按 ${data.reversal_fee_percent??6}% 估算`})
  }
  for(const event of history.recharges||[]){
    const after=feeCents(event.after||'0'),before=feeCents(event.before||'0')
    events.push({id:event.id,kind:'recharge',at:event.baseline?null:financeTime(event.at),reference:event.id,amount:money(after-before),currency:'USD',usd:money(after-before),note:event.baseline?'历史充值基数，未分摊日期':'充值总账变动 '+event.delta+' USD'})
  }
  for(const row of history.usage_days||[])events.push({id:row.id,kind:'consumption',at:financeTime(row.day+'T12:00:00+08:00'),email:row.email,reference:row.id,amount:row.amount,currency:'R',note:'下游用户消耗采集增量'})
  const deposit=feeCents(data.recharge_ledger?.amount)
  const recordedDeposit=events.filter(e=>e.kind==='recharge').reduce((n,e)=>n+(cents(e.usd)||0n),0n)
  if(deposit!==null&&deposit!==recordedDeposit)events.push({id:'legacy-recharge',kind:'recharge',at:null,amount:money(deposit-recordedDeposit),usd:money(deposit-recordedDeposit),currency:'USD',note:'未记录日期的历史充值手续费'})
  const consumption=consumptionSummary(data.cases||[],history.consumption||data.consumption_history||[])
  const recordedR=(history.usage_days||[]).reduce((n,r)=>n+r.amount,0)
  if(consumption.total!==null&&consumption.total>recordedR+0.0000001)events.push({id:'legacy-consumption',kind:'consumption',at:null,amount:consumption.total-recordedR,currency:'R',note:'首次采集的历史累计消费，无法还原每天发生额'})
  return events.sort((a,b)=>(b.at??-Infinity)-(a.at??-Infinity)||String(a.id).localeCompare(String(b.id)))
}
function summarizeEvents(events) {
  let usd=0n,usdt=0n,reversalUSD=0n,rechargeUSD=0n,r=0,missing=0,opening=0
  const original=new Map()
  for(const e of events){
    if(e.kind==='consumption'){r+=Number(e.amount)||0;continue}
    if(e.kind==='opening'){usdt+=cents(e.amount)||0n;opening++;continue}
    if(e.kind==='reversal'&&e.amount!==null){const n=cents(e.amount);if(n!==null)original.set(e.currency,(original.get(e.currency)||0n)+n)}
    const raw=e.usd_precise??e.usd
    const n=cents(raw)===null?null:BigInt(String(raw).replace('-','').split('.')[0])*1000000n+BigInt((String(raw).split('.')[1]||'').padEnd(6,'0'))
    if(n===null||e.incomplete){missing++;continue}
    const signed=String(raw).startsWith('-')?-n:n
    usd+=signed
    if(e.kind==='reversal')reversalUSD+=signed
    if(e.kind==='recharge')rechargeUSD+=signed
  }
  const rounded=n=>money(((n<0n?-n:n)+5000n)/10000n*(n<0n?-1n:1n))
  return {usd:rounded(usd),usdt:money(usdt),reversalUSD:rounded(reversalUSD),rechargeUSD:rounded(rechargeUSD),r,missing,opening,original:[...original].map(([currency,n])=>({currency,amount:money(n)}))}
}
export function financeReport(data,history={},mode='day',date=beijingDay()) {
  const range=periodRange(mode,date),all=financeEvents(data,history),overview=financeOverview({...data,consumption_history:history.consumption||data.consumption_history})
  const events=all.filter(e=>mode==='all'||e.at!==null&&e.at>=range.start&&e.at<range.end)
  const summary=summarizeEvents(events)
  if(mode==='all'){
    summary.r=overview.consumption.total
    summary.rechargeUSD=overview.depositFee
    summary.usd=overview.depositFee===null?null:money((cents(summary.reversalUSD)||0n)+cents(overview.depositFee))
    summary.missing+=overview.consumption.missing
  }
  const groups=new Map()
  for(const e of events){
    const key=e.at===null?'未分期历史':mode==='all'?beijingDay(e.at).slice(0,7):beijingDay(e.at)
    if(!groups.has(key))groups.set(key,[])
    groups.get(key).push(e)
  }
  const rows=[...groups].map(([day,list])=>({day,...summarizeEvents(list)})).sort((a,b)=>b.day.localeCompare(a.day))
  const baseline=all.filter(e=>e.kind==='consumption'&&e.at===null).reduce((n,r)=>n+Number(r.amount),0)
  const snapshots=(history.snapshots||[]).filter(s=>{const at=financeTime(s.at);return at!==null&&at>=range.start&&at<range.end}).sort((a,b)=>financeTime(a.at)-financeTime(b.at))
  const first=snapshots[0],last=snapshots.at(-1)
  const firstLoss=first?financeOverview({...first,operations:data.operations||[]}).loss:null,lastLoss=last?financeOverview({...last,operations:data.operations||[]}).loss:null
  const lossChange=first&&last&&financeTime(first.at)!==financeTime(last.at)&&firstLoss!==null&&lastLoss!==null?money(cents(lastLoss)-cents(firstLoss)):null
  return {range,summary,events,rows,overview,baseline,undated:all.filter(e=>e.at===null&&e.kind!=='consumption').length,lossChange,firstAt:first?.at,lastAt:last?.at,
    coverage:(history.usage_days||[]).map(r=>r.day).sort()[0]||null}
}
