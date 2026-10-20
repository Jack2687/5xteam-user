import { cents, money, fundNames } from './nexovcc-finance.js'

const amount = v => v !== null && v !== undefined && String(v).trim() !== '' && Number.isFinite(Number(v))
const time = v => v && !String(v).startsWith('0001') ? Date.parse(v) : NaN
const transferKinds = [3001, 4001, 4002, 3002]
const moneyActions = ['open', 'deposit', 'withdraw', 'withdraw_all', 'withdraw_cancel', 'cancel']
const waiting = c => c.payment_status === 'success' && c.state === 'waiting' && !c.removed_at &&
 !c.match?.group?.fully_returned && !c.match?.group?.settled

// Display only: neither a balance discrepancy nor a newer balance is evidence
// of a particular refund. Never change case state, ledger totals or eligibility.
export function cardReturnNotice(data, row) {
 const card = row?.card
 if (!card || card.status === '4' || !(data.cases || []).some(c => c.card_id === card.id && waiting(c))) return null
 const balanceAt = time(card.balance_at), finance = data.finance || {}
 const ledgerAt = time(finance.card_at?.[card.serial_no] || (!finance.card_at ? finance.at : null))
 if (!amount(card.balance) || !Number.isFinite(balanceAt) || !Number.isFinite(ledgerAt)) {
  return {text:'流水待同步', detail:'余额或本卡流水不完整，回款状态仍以对应退款流水为准。'}
 }
 if (ledgerAt < balanceAt) {
  return {text:'余额已更新，流水待同步', detail:'余额比本卡流水更新；余额变化不代表该笔回款已经确认。'}
 }
 const serial = card.serial_no
 const funds = (finance.funds || []).filter(f => f.serial_no === serial)
 const groups = (data.groups || []).filter(g => g.serial_no === serial)
 const moving = (data.operations || []).some(op => (op.card_id === card.id || op.serial_no === serial) &&
  moneyActions.includes(op.action) && !['succeeded', 'failed'].includes(op.status))
 const staleReview = (data.trade_reviews || []).some(r => !r.voided && !r.applied &&
  (data.review_groups || []).some(g => g.id === r.group_id && g.serial_no === serial))
 if (moving || staleReview || !funds.some(f => f.kind === 3001) || !groups.length ||
  funds.some(f => f.currency !== 'USD' || !amount(f.amount) || !fundNames[f.kind] || !Number.isFinite(time(f.at))) ||
  groups.some(g => !g.valid || g.other_expense?.applied || !amount(g.payment) || !amount(g.returned) || !Number.isFinite(time(g.at)))) {
  return {text:'余额与流水待核对', detail:'存在未完成划转或不完整流水，暂不能根据余额判断回款。'}
 }
 if (funds.some(f => time(f.at)>balanceAt) || groups.some(g => time(g.at)>balanceAt || time(g.returned_at)>balanceAt)) {
  return {text:'余额待同步，回款待确认', detail:'已有比余额更新的资金流水，等待同一时点的数据后再核对。'}
 }
 const funding = -funds.filter(f => transferKinds.includes(f.kind)).reduce((sum,f) => sum+cents(f.amount),0)
 const netPaid = groups.reduce((sum,g) => sum+cents(g.payment)-cents(g.returned),0)
 const expected = funding-netPaid, excess = cents(card.balance)-expected
 if (excess <= 1) return null // One cent of rounding alone is not a return signal.
 return {
  text:'余额高于流水，回款待确认',
  detail:`卡余额 ${money(cents(card.balance))} USD，现有流水推算 ${money(expected)} USD，相差 ${money(excess)} USD。仅提示本卡数据差异，不认定某笔到账；仍等待对应撤销 / 退款流水。`,
 }
}

export function caseReturnNotice(data, row) {
 if (!waiting(row)) return null
 const notice = cardReturnNotice(data, (data.cards || []).find(c => c.card.id === row.card_id))
 return notice ? {...notice, text:'本卡'+notice.text} : null
}
