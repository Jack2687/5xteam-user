import { cents, money, financeTotals } from './nexovcc-finance.js'

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
 // Active/closing cards still belong to card assets. Only a closed card's
 // uncredited residual can explain a missing transfer, never a new expense.
 const closurePending = ready && closed && !totals.unverified ? Math.max(0, funded - withdrawn - returned - totals.cardLoss - totals.settlement - totals.pending) : null
 const settled = ready && closed && closurePending === 0 && !waiting
 return {ready, funded:ready ? funded : null, withdrawn:ready ? withdrawn : null, returned:ready ? returned : null,
  difference:settled ? funded - withdrawn - returned : null, additions:additions.length,
  closurePending, closed, waiting,
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
 const assets = cents(data.balance?.amount) + cardBalance + totals.pending
 const gap = totals.ready && !moneyMissing ? totals.gross - assets - totals.cost - externalWithdrawn : null
 const summaries = cards.map(r => cardFunding(data, r))
 const closurePending = sum(summaries, s => s.closurePending || 0)
 if (!totals.ready) reasons.push('财务流水尚未同步')
 if (moneyMissing) reasons.push('账户或卡片余额缺失')
 if (data.finance_error) reasons.push('本次同步未完成，使用上次流水')
 if (totals.unverified || totals.foreign || totals.unknownFunds || allFunds.some(f => !hasAmount(f.amount))) reasons.push('部分流水金额、币种或类型待核实')
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
 const unexplained = gap === null ? null : gap - closurePending
 const state = gap === null ? 'unknown' : reasons.length ? 'stale' : closurePending > 0 ? 'pending' : Math.abs(gap) <= 100 ? 'balanced' : 'difference'
 const label = {unknown:'数据不足', stale:'待同步核实', pending:'销卡退回待核实', balanced:gap === 0 ? '账面相符' : '小额差异', difference:'存在差额'}[state]
 return {state,label,reasons,totals,gap,assets,account:hasAmount(data.balance?.amount)?cents(data.balance.amount):null,cardBalance:active.some(r=>!hasAmount(r.card.balance))?null:cardBalance,externalWithdrawn,closurePending,unexplained}
}
