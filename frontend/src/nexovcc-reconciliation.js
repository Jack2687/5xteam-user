import { cents, money, financeTotals, balanceCostAdjustments } from './nexovcc-finance.js'

const hasAmount = v => v !== null && v !== undefined && String(v).trim() !== '' && Number.isFinite(Number(v))
const timestamp = v => v && !String(v).startsWith('0001') ? Date.parse(v) : NaN
const pending = op => !['succeeded', 'failed'].includes(op.status)
const moneyActions = ['open', 'deposit', 'withdraw', 'withdraw_all', 'withdraw_cancel', 'cancel']
const sum = (rows, fn) => rows.reduce((total, row) => total + fn(row), 0)

export function cardFunding(data, row) {
 const serial = row.card.serial_no
 const funds = (data.finance?.funds || []).filter(f => f.serial_no === serial)
 const transfers = funds.filter(f => [3001, 4001, 4002, 3002].includes(f.kind))
 const valid = transfers.every(f => f.currency === 'USD' && hasAmount(f.amount))
 const usd = transfers.filter(f => f.currency === 'USD' && hasAmount(f.amount))
 const initial = usd.filter(f => f.kind === 3001), additions = usd.filter(f => f.kind === 4001)
 const closure = usd.filter(f => f.kind === 3002)
 const funded = -sum([...initial, ...additions], f => cents(f.amount))
 const withdrawn = sum(usd.filter(f => f.kind === 4002), f => cents(f.amount))
 const returned = sum(closure, f => cents(f.amount))
 const ready = !!data.finance?.at && Number.isFinite(timestamp(data.finance.at)) && initial.length > 0 && valid
 const closed = row.card.status === '4'
 const waiting = (data.operations || []).some(op => (op.card_id === row.card.id || op.serial_no === serial) && moneyActions.includes(op.action) && pending(op))
 const totals = financeTotals(data, serial)
 // A posted deletion return moves the card's balance back to the main account.
 // An authorization left on that deleted card remains unresolved, but is no
 // longer a separate card asset. Keep the raw payment/refund status unchanged;
 // any principal not explained by transfers or costs stays in closurePending.
 const returnedAt = Math.max(...closure.map(f => timestamp(f.at)))
 const closureCredited = ready && closed && returned > 0 && !waiting && !totals.unverified && !totals.staleReviews &&
  Number.isFinite(returnedAt) && timestamp(data.balance?.at) >= returnedAt
 const closedAuthorization = closureCredited ? totals.pending : 0
 const pendingAssets = totals.pending - closedAuthorization
 // Active/closing cards still belong to card assets. Only a closed card's
 // uncredited residual can explain a missing transfer, never a new expense.
 const closurePending = ready && closed && !totals.unverified && !totals.staleReviews ? Math.max(0, funded - withdrawn - returned - totals.cardLoss - totals.settlement - pendingAssets - totals.manualFees - totals.balanceDifference) : null
 const settled = ready && closed && closurePending === 0 && !waiting
 return {ready, funded:ready ? funded : null, withdrawn:ready ? withdrawn : null, returned:ready ? returned : null,
  difference:settled ? funded - withdrawn - returned : null, additions:additions.length,
  closurePending, closed, waiting, pendingAssets, closedAuthorization,
  note:!ready ? '入金流水缺失或币种待核实' : waiting ? '资金划转处理中，差额待核实' : !closed ? '销卡结算后显示差额' : closurePending > 0 ? '销卡退回待核实，不计成本' : '差额＝累计额度－提现－销卡退回'}
}

export function cardBalanceDisplay(data, row) {
 if (row.card.status !== '4') return {amount:hasAmount(row.card.balance)?money(cents(row.card.balance)):'—',note:''}
 const funding = cardFunding(data,row)
 // The final balance endpoint no longer exists after deletion. Its cached
 // pre-deletion balance is historical, never the card's current balance.
 if (funding.closurePending === 0) return {amount:'0.00',note:'余款已退回'}
 return {amount:'—',note:funding.closurePending > 0?'余款待退回':'余款待核实'}
}

// The headline is normal-card money still outside the card balance, not the
// supplier's aggregate (which may retain deleted-card and released holds).
// Reuse the existing balance-recovery estimate; never change refund evidence,
// costs, occupancy, withdrawal, or closure rules to produce this display.
export function frozenBalanceDisplay(data) {
 const raw=data.balance?.frozen
 const platform=hasAmount(raw)&&cents(raw)>=0?cents(raw):null
 const cards=(data.cards||[]).map(row=>({row,totals:financeTotals(data,row.card.serial_no)}))
 const active=cards.filter(({row})=>row.card.status!=='4')
 const rows=cards.filter(({row,totals})=>row.card.status==='4'&&totals.pending>0).map(({row,totals})=>({serial:row.card.serial_no,last4:row.card.last4,amount:totals.pending,returned:cardFunding(data,row).returned}))
 const activeByID=new Map(active.map(({row})=>[row.card.id,row]))
 const settledRefunds=new Map(),seenGroups=new Set()
 let unlinkedSettledRefund=false
 for(const c of data.cases||[]){
  if(c.state!=='waiting'||c.payment_status!=='success'||c.removed_at)continue
  const card=activeByID.get(c.card_id)
  if(!card)continue
  const group=c.match?.group
  if(!group||!group.valid||group.serial_no!==card.card.serial_no){
   if((data.groups||[]).some(g=>g.serial_no===card.card.serial_no&&g.valid&&g.settled&&!g.fully_returned))unlinkedSettledRefund=true
   continue
  }
  if(!group.settled||group.fully_returned||seenGroups.has(group.id))continue
  seenGroups.add(group.id)
  const amount=Math.max(0,cents(group.difference))
  settledRefunds.set(card.card.serial_no,(settledRefunds.get(card.card.serial_no)||0)+amount)
 }
 // Apply the existing balance-recovery checks to these waiting refunds only
 // for the headline. Original settlement costs and refund evidence stay intact.
 const displayData={...data,groups:(data.groups||[]).map(g=>seenGroups.has(g.id)?{...g,settled:false}:g)}
 const displayRecovered=new Map(balanceCostAdjustments(displayData).map(r=>[r.serial,r.pendingAdjustment]))
 const recoveredRows=active.map(({row,totals})=>({serial:row.card.serial_no,last4:row.card.last4,amount:Math.max(totals.balanceRecoveredPending,displayRecovered.get(row.card.serial_no)||0)})).filter(row=>row.amount>0)
 const recoveredBySerial=new Map(recoveredRows.map(row=>[row.serial,row.amount]))
 const pendingRows=active.map(({row,totals})=>{
  const refundPending=settledRefunds.get(row.card.serial_no)||0
  const recovered=recoveredBySerial.get(row.card.serial_no)||0
  const amount=Math.max(0,totals.pending+refundPending-recovered)
  const item={serial:row.card.serial_no,last4:row.card.last4,amount}
  const remainingRefund=Math.max(0,refundPending-Math.max(0,recovered-totals.pending))
  if(remainingRefund)item.refundPending=remainingRefund
  return item
 }).filter(row=>row.amount>0)
 const historical=sum(rows,row=>row.amount),recovered=sum(recoveredRows,row=>row.amount)
 const knownSerials=new Set(cards.map(({row})=>row.card.serial_no))
 const ready=!unlinkedSettledRefund&&Number.isFinite(timestamp(data.finance?.at))&&active.every(({totals})=>!totals.unverified&&!totals.staleReviews)&&
  !(data.groups||[]).some(g=>!knownSerials.has(g.serial_no))
 const current=ready?sum(pendingRows,row=>row.amount):null
 return {platform,historical,recovered,current,rows,recoveredRows,pendingRows,...(unlinkedSettledRefund?{unlinkedSettledRefund:true}:{})}
}

export function cardReturnCounts(cases) {
 const counts = {unopened:0, opened:0, waiting:0, returned:0}
 for (const row of cases || []) if (Object.hasOwn(counts, row.state)) counts[row.state]++
 return counts
}

export function reconciliation(data, now = Date.now(), freshnessSeconds = 120) {
 const totals = financeTotals(data), cards = data.cards || [], reasons = []
 const active = cards.filter(r => r.card.status !== '4')
 const moneyMissing = !hasAmount(data.balance?.amount) || active.some(r => !hasAmount(r.card.balance))
 const allFunds = data.finance?.funds || []
 const withdrawals = allFunds.filter(f => f.kind === 1001 && f.currency === 'USD')
 const externalWithdrawn = -sum(withdrawals, f => cents(f.amount))
 const cardBalance = sum(active, r => cents(r.card.balance))
 const summaries = cards.map(r => cardFunding(data, r))
 const closedSerials = new Set(cards.filter((r,i) => summaries[i].closedAuthorization > 0).map(r => r.card.serial_no))
 const closedAuthorization = sum((data.groups || []).filter(g => closedSerials.has(g.serial_no) && g.valid && !g.fully_returned && !g.settled), g => Math.max(0, cents(g.difference)))
 const pendingAssets = totals.pending - closedAuthorization - totals.balanceRecoveredPending
 const assets = cents(data.balance?.amount) + cardBalance + pendingAssets
 const gap = totals.ready && !moneyMissing ? totals.gross - assets - (totals.cost - totals.merchantFee) - externalWithdrawn : null
 const closurePending = sum(summaries, s => s.closurePending || 0)
 if (!totals.ready) reasons.push('财务流水尚未同步')
 if (moneyMissing) reasons.push('账户或卡片余额缺失')
 if (data.finance_error) reasons.push('本次同步未完成，使用上次流水')
 if (totals.unverified || totals.foreign || totals.unknownFunds || allFunds.some(f => !hasAmount(f.amount))) reasons.push('部分流水金额、币种或类型待核实')
 if (totals.staleReviews) reasons.push('人工核实涉及的流水已变化，请重新核实')
 if (summaries.some(s => !s.ready)) reasons.push('部分卡片入金流水缺失')
 if (cards.some(r => r.card.error)) reasons.push('部分卡片查询异常')
 const serials = new Set(cards.map(r => r.card.serial_no))
 if (allFunds.some(f => f.serial_no && !serials.has(f.serial_no)) || (data.groups || []).some(g => g.serial_no && !serials.has(g.serial_no))) reasons.push('部分流水对应卡片尚未同步')
 const times = [data.finance?.at, data.balance?.at, ...cards.map(r => r.card.status === '4' ? r.card.synced_at : r.card.balance_at)].map(timestamp)
 const maxAge = Math.max(120, freshnessSeconds) * 1000
 const stale = times.some(t => !Number.isFinite(t) || now - t > maxAge || t - now > 300000)
 if (stale) reasons.push('余额或流水较旧，请同步全部')
 const moving = (data.operations || []).some(op => moneyActions.includes(op.action) && pending(op))
 if (moving) reasons.push('有资金操作处理中')
 if (totals.balanceDifference || totals.balanceRecoveredPending) reasons.push('含余额差额暂估，回款状态仍以流水为准')
 const unexplained = gap === null ? null : gap - closurePending
 const state = gap === null ? 'unknown' : reasons.length ? 'stale' : closurePending > 0 ? 'pending' : Math.abs(gap) <= 100 ? 'balanced' : 'difference'
 const label = {unknown:'数据不足', stale:'待同步核实', pending:'销卡退回待核实', balanced:gap === 0 ? '账面相符' : '小额差异', difference:'存在差额'}[state]
 return {state,label,reasons,totals,gap,assets,pendingAssets,closedAuthorization,account:hasAmount(data.balance?.amount)?cents(data.balance.amount):null,cardBalance:active.some(r=>!hasAmount(r.card.balance))?null:cardBalance,externalWithdrawn,closurePending,unexplained}
}
