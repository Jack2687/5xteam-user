// Six decimal places, integer arithmetic, and no conversion between currencies.
const scale=1000000n
function units(value){
 const s=String(value??'')
 if(!/^\d{1,12}(\.\d{1,6})?$/.test(s))return null
 const [whole,fraction='']=s.split('.')
 return BigInt(whole)*scale+BigInt(fraction.padEnd(6,'0'))
}
function format(value){
 const n=value<0n?-value:value
 return (value<0n?'-':'')+String(n/scale)+'.'+String(n%scale).padStart(6,'0').replace(/0+$/,'').padEnd(2,'0')
}
const returnReceived=t=>['2','5','16'].includes(t?.tradeType)&&(t.tradeStatus==='2'||t.tradeType==='2'&&t.tradeStatus==='1')
const currencyOK=value=>/^[A-Z]{3}$/.test(value||'')
function tradeAt(t){
 const parse=s=>{if(!s)return 0;return Date.parse(/^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d$/.test(s)?s.replace(' ','T')+'+08:00':s)||0}
 const at=parse(t.trade?.tradeTime),recorded=parse(t.trade?.createdAt)
 if(!at)return recorded
 const corrected=at+8*3600000,lag=recorded-corrected
 return !String(t.trade.tradeTime).includes('T')&&recorded&&lag>=0&&lag<=30*60000?corrected:at
}
const earlier=(a,b)=>tradeAt(a)!==tradeAt(b)?tradeAt(a)<tradeAt(b):String(a.id||'')<String(b.id||'')
// Issuer card amounts, deduplicated by authorization. Never add clearing to
// its authorization, or count card-closure balance transfers as account refunds.
export function cardAmounts(card,trades=[]){
 const payments=new Map(),returns=new Map(),totals=new Map()
 const key=t=>t.trade.authCode?'auth:'+t.trade.authCode:'trade:'+t.trade.tradeNo
 const rank=t=>t.trade.tradeType==='4'&&t.trade.tradeStatus==='2'?3:t.trade.tradeType==='1'?2:1
 let invalid=false,pending=0
 for(const t of trades){
  if(t.card_id!==card.id)continue
  const v=t.trade||{},paid=['1','4'].includes(v.tradeType),returned=['2','5','16'].includes(v.tradeType)
  if(!paid&&!returned)continue
  if(returned&&!returnReceived(v)){pending++;continue}
  if(paid&&!['1','2'].includes(v.tradeStatus))continue
  const n=units(v.cardAmount)
  if(n===null||!currencyOK(v.cardCurrency)||!v.tradeNo){invalid=true;continue}
  const group=paid?payments:returns,k=key(t),old=group.get(k)
  const linked=t.confirmed&&!!t.payment_order_id,oldLinked=old?.confirmed&&!!old.payment_order_id
  const prefer=old&&(paid?(rank(t)>rank(old)||rank(t)===rank(old)&&String(t.id||'')<String(old.id||'')):(Boolean(linked)!==Boolean(oldLinked)?linked:earlier(t,old)))
  if(!old||prefer)group.set(k,t)
 }
 for(const [group,field] of [[payments,'paid'],[returns,'returned']])for(const t of group.values()){
  const v=t.trade,row=totals.get(v.cardCurrency)||{currency:v.cardCurrency,paid:0n,returned:0n,authorization:false}
  row[field]+=units(v.cardAmount);if(field==='paid'&&v.tradeType==='1')row.authorization=true
  totals.set(v.cardCurrency,row)
 }
 const rows=[...totals.values()].sort((a,b)=>a.currency.localeCompare(b.currency)).map(r=>({...r,paid:format(r.paid),returned:format(r.returned),difference:invalid?null:format(r.paid-r.returned)}))
 return {rows,invalid,pending,fee:'0.50 USDT'}
}
export function accountAmounts(row){
 const charge=row?.charge
 const paid=charge&&['1','4'].includes(charge.type)&&['1','2'].includes(charge.status)&&currencyOK(charge.card_currency)?units(charge.card_amount):null
 const totals=new Map(),seen=new Set()
 let pending=0,invalid=false
 for(const t of row?.returns||[]){
  if(!['2','5','16'].includes(t.trade?.tradeType))continue
  if(!t.confirmed||!returnReceived(t.trade)){pending++;continue}
  if(t.id&&seen.has(t.id))continue
  if(t.id)seen.add(t.id)
  const n=units(t.confirmed_amount),currency=t.confirmed_currency
  if(n===null||!currencyOK(currency)){invalid=true;continue}
  totals.set(currency,(totals.get(currency)||0n)+n)
 }
 const returned=[...totals].sort(([a],[b])=>a.localeCompare(b)).map(([currency,n])=>({amount:format(n),currency}))
 const comparable=paid!==null&&(charge.status==='2'||charge.type==='1'&&charge.status==='1')&&!invalid&&[...totals.keys()].every(c=>c===charge.card_currency)
 return {paid:paid===null?null:format(paid),currency:paid===null?'':charge.card_currency,authorization:charge?.type==='1',posted:charge?.status==='2',returned,pending,invalid,
  difference:comparable?format(paid-(totals.get(charge.card_currency)||0n)):null,
  reason:paid===null?'等待自动识别付款或金额缺失':charge.status!=='2'&&charge.type!=='1'?'付款流水待入账':invalid?'回款金额待核实':!comparable?'币种不同，不能直接相减':''}
}
