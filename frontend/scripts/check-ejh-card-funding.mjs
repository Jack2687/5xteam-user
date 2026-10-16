import assert from 'node:assert/strict'
import { summarizeCardBalances } from '../src/ejh.js'
import { cardOpeningAmounts, cardFundedAmounts, cardClosureAmounts, cardFundingSummary, summarizeCardBalancesUSD, summarizeCardAmountsUSD } from '../src/ejh-card-funding.js'

const card = { id: 'c', remote: { orderNo: 'original', cardAmount: '90000', billAmount: '9999', cardCurrency: 'PHP', billCurrency: 'USD' } }
const open = { id: 'open', card_id: 'c', method: 'open', status: 'completed', created_at: '2026-10-01', request: { cardAmount: '13000', cardCurrency: 'PHP', billCurrency: 'USD' }, result: { cardAmount: '13000.00', cardCurrency: 'PHP', billAmount: '208.65', billCurrency: 'USD', actualConvertedAmount: '209.15' } }
assert.deepEqual(cardOpeningAmounts(card, [open]), { amount: '13000.00 PHP', settlement: '208.65 USD' })
assert.deepEqual(cardOpeningAmounts(card, [{ ...open, method: 'increase' }, open]), cardOpeningAmounts(card, [open]))
assert.deepEqual(cardOpeningAmounts(card, [{ ...open, status: 'failed' }]), { amount: '未记录', settlement: '未记录' })
assert.deepEqual(cardOpeningAmounts(card, [{ ...open, result: null }]), { amount: '13000.00 PHP', settlement: '未记录' })
assert.deepEqual(cardOpeningAmounts(card, [{ ...open, card_id: '', order_no: 'original' }]), cardOpeningAmounts(card, [open]))
assert.deepEqual(cardOpeningAmounts(card, []), { amount: '未记录', settlement: '未记录' }, 'current card totals are not initial amounts')
const increase = { id: 'increase', card_id: 'c', method: 'increase', status: 'completed', amount: '2000.10', currency: 'PHP', result: { billAmount: '8000' } }
const funded = cardFundedAmounts(card, [open, increase, increase, { ...increase, id: 'failed', status: 'failed' }, { ...increase, id: 'unknown', status: 'unknown' }])
assert.deepEqual(funded, { amounts: ['15000.10 PHP'], increases: 1, missing: 0, settlement: '208.65 USD', missingSettlements: 1 })
assert.deepEqual(cardFundedAmounts(card, [open]).amounts, ['13000.00 PHP'])
assert.deepEqual(cardFundedAmounts(card, [open, { ...increase, status: 'resolved' }]).amounts, ['15000.10 PHP'])
assert.equal(cardFundedAmounts(card, [increase]).missing, 1)
assert.equal(cardFundedAmounts(card, [open, { ...increase, currency: '???' }]).missing, 1)
const closure = { id: 't', card_id: 'c', trade: { tradeNo: 'closed', tradeType: '17', tradeStatus: '2', tradeAmount: '13000.00', tradeCurrency: 'PHP', billAmount: '208.15', billCurrency: 'USD', cardAmount: '99999', cardCurrency: 'PHP' } }
const got = cardClosureAmounts(card, [closure, closure, { ...closure, card_id: 'other' }, { ...closure, trade: { ...closure.trade, tradeType: '5' } }])
assert.deepEqual(got, { transaction: ['13000.00 PHP'], settlement: ['208.15 USD'], posted: 1, pending: 0, unknown: 0, missingTransaction: 0, missingSettlement: 0 })
const pending = { ...closure, trade: { ...closure.trade, tradeStatus: '1' } }
assert.equal(cardClosureAmounts(card, [pending]).posted, 0)
assert.equal(cardClosureAmounts(card, [pending]).pending, 1)
for (const rows of [[pending, closure], [closure, pending]]) assert.equal(cardClosureAmounts(card, rows).posted, 1)
assert.deepEqual(cardClosureAmounts(card, []).transaction, [])
const missing = cardClosureAmounts(card, [{ ...closure, trade: { ...closure.trade, billAmount: '', tradeCurrency: '' } }])
assert.equal(missing.missingTransaction, 1); assert.equal(missing.missingSettlement, 1)
const zero = cardClosureAmounts(card, [{ ...closure, trade: { ...closure.trade, tradeAmount: '0', billAmount: '0' } }])
assert.deepEqual(zero.transaction, ['0.00 PHP']); assert.deepEqual(zero.settlement, ['0.00 USD'])
const extra = { ...closure, id: 'second', trade: { ...closure.trade, tradeNo: 'second', tradeAmount: '0.10', tradeCurrency: 'EUR', billAmount: '0.20' } }
const multi = cardClosureAmounts(card, [closure, extra])
assert.deepEqual(multi.transaction, ['0.10 EUR', '13000.00 PHP']); assert.deepEqual(multi.settlement, ['208.35 USD'])
console.log('Passed: initial opening snapshots, top-up exclusion, closure trade/settlement amounts, precision, currencies, zero/missing/pending values and deduplication')

const difference = (operations = [open], trades = [closure]) => cardFundingSummary(card, operations, trades).difference
const expected = (transaction, settlement) => ({ transaction: { amounts: transaction, reason: '' }, settlement: { amounts: settlement, reason: '' } })
assert.deepEqual(difference(), expected(['0.00 PHP'], ['0.50 USD']))
assert.deepEqual(difference([open], [closure, closure]), difference(), 'duplicate closure trades must not double the refund')
const toppedUp = difference([open, increase, increase, { ...increase, id: 'failed', status: 'failed' }])
assert.deepEqual(toppedUp.transaction.amounts, ['2000.10 PHP'])
assert.deepEqual(toppedUp.settlement.amounts, [])
assert.match(toppedUp.settlement.reason, /追加.*结算/)
assert.deepEqual(difference([open, { ...increase, status: 'unknown' }, { ...increase, id: 'failed', status: 'failed' }]), difference())
const unknownTotal = difference([increase])
assert.deepEqual(unknownTotal.transaction.amounts, [])
assert.match(unknownTotal.transaction.reason, /初始或追加/)
assert.deepEqual(difference([], [closure]).settlement.amounts, [])
const unknownRefund = difference([open], [{ ...closure, trade: { ...closure.trade, tradeAmount: '', billCurrency: '' } }])
assert.deepEqual(unknownRefund.transaction.amounts, [])
assert.deepEqual(unknownRefund.settlement.amounts, [])
assert.match(unknownRefund.transaction.reason, /销卡退回.*缺失/)
const mismatched = difference([open], [{ ...closure, trade: { ...closure.trade, tradeCurrency: 'EUR', billCurrency: 'CNY' } }])
assert.match(mismatched.transaction.reason, /币种/)
assert.match(mismatched.settlement.reason, /币种/)
assert.deepEqual(difference([open], [closure, extra]).transaction.amounts, [], 'an unmatched currency cannot be treated as zero funding')
for (const trades of [[], [pending], [{ ...pending, trade: { ...pending.trade, tradeStatus: 'unknown' } }], [closure, { ...pending, id: 'pending-2', trade: { ...pending.trade, tradeNo: 'pending-2' } }]]) {
  const result = difference([open], trades)
  assert.deepEqual(result.transaction.amounts, [])
  assert.deepEqual(result.settlement.amounts, [])
  assert.ok(result.transaction.reason)
}
const refund = (tradeAmount, billAmount) => [{ ...closure, trade: { ...closure.trade, tradeAmount, billAmount } }]
assert.deepEqual(difference([open], refund('0', '0')), expected(['13000.00 PHP'], ['208.65 USD']), 'a recorded zero is distinct from an absent refund')
assert.deepEqual(difference([open], refund('13001.25', '208.66')), expected(['-1.25 PHP'], ['-0.01 USD']))
assert.deepEqual(difference([open], refund('12999.999999', '208.650001')), expected(['0.000001 PHP'], ['-0.000001 USD']))
assert.deepEqual(difference([open, { ...increase, amount: '0.10' }, { ...increase, id: 'second', amount: '0.20' }], refund('13000.30', '208.65')).transaction.amounts, ['0.00 PHP'], 'decimal addition and subtraction must stay exact')
const summary = cardFundingSummary(card, [open, increase], [closure])
assert.deepEqual(summary.opening, cardOpeningAmounts(card, [open, increase]))
assert.deepEqual(summary.funded, cardFundedAmounts(card, [open, increase]))
assert.deepEqual(summary.closure, cardClosureAmounts(card, [closure]))
console.log('Passed: funding-minus-closure differences, top-ups, exact signed decimals, duplicate/pending refunds, missing records and currency mismatch')

const balance = (id, value, currency = 'USD', extra = {}) => ({ id, balance: value, balance_at: '2026-10-16', remote: { cardCurrency: currency }, ...extra })
const phpBalance = balance('c', '10000', 'PHP', { remote: { ...card.remote, cardAmount: '90000', billAmount: '9999' } })
assert.deepEqual(summarizeCardBalancesUSD([phpBalance], [open, increase]), { amount: '160.50', known: 1, estimated: 1, missing: 0, total: 1 }, 'estimate uses original opening ratio, not current limit or top-up balance')
assert.deepEqual(summarizeCardBalancesUSD([phpBalance, balance('usd1','0.1'), balance('usd2','0.2'), balance('eur','10','EUR')], [open]), { amount:'160.80',known:3,estimated:1,missing:1,total:4 })
assert.equal(summarizeCardBalancesUSD([balance('c','-1000','PHP')], [open]).amount, '-16.05')
assert.equal(summarizeCardBalancesUSD([balance('usd','-0.005')]).amount, '-0.01')
assert.equal(summarizeCardBalancesUSD([balance('usd1','0.004'),balance('usd2','0.004')]).amount, '0.01', 'round once after summing')
assert.equal(summarizeCardBalancesUSD([balance('zero','0','PHP')]).amount, '0.00')
assert.equal(summarizeCardBalancesUSD([balance('c','100','PHP',{balance_error:'timeout'})], [open]).amount, '1.61', 'saved balance is usable after a failed refresh')
for (const row of [balance('missing','10','PHP'),balance('c','bad','PHP'),balance('c','10','PHP',{balance_at:null}),balance('c','10','EUR')]) {
 const result=summarizeCardBalancesUSD([row], [open]);assert.equal(result.amount,null);assert.equal(result.missing,1)
}
assert.equal(summarizeCardBalancesUSD([phpBalance], [{...open,result:{...open.result,billCurrency:'EUR'}}]).amount,null)
assert.equal(summarizeCardBalancesUSD([]).amount,null)
console.log('Passed: historical USD balance estimates, exact decimal aggregation, signed rounding, missing conversion evidence and saved balances')

// Closed cards can retain a nonzero historical snapshot. Exclude it from both
// currencies and counts, but keep closure-in-progress until status becomes 2.
const activeBalance=balance('active','10','USD',{remote:{status:'1',cardCurrency:'USD'}})
const closingBalance={...phpBalance,remote:{...phpBalance.remote,status:'8'}}
const closedBalance=balance('closed','999','EUR',{remote:{status:2,cardCurrency:'EUR'},balance_error:'historical failure'})
const scopedCards=[activeBalance,closingBalance,closedBalance,{...closedBalance,id:'closed-missing',balance_at:null},balance('local-closed','888','USD',{local:{source:'ejh',issuer_status:'2'}})]
assert.deepEqual(summarizeCardBalances(scopedCards),{totals:[{currency:'PHP',count:1,amount:'10000.00'},{currency:'USD',count:1,amount:'10.00'}],known:2,missing:0,failed:0,total:2})
assert.deepEqual(summarizeCardBalancesUSD(scopedCards,[open]),{amount:'170.50',known:2,estimated:1,missing:0,total:2})
closingBalance.remote.status='2'
assert.deepEqual(summarizeCardBalances(scopedCards).totals,[{currency:'USD',count:1,amount:'10.00'}])
assert.deepEqual(summarizeCardBalancesUSD(scopedCards,[open]),{amount:'10.00',known:1,estimated:0,missing:0,total:1})
assert.deepEqual(summarizeCardBalances([closedBalance]),{totals:[],known:0,missing:0,failed:0,total:0})
assert.deepEqual(summarizeCardBalancesUSD([closedBalance]),{amount:null,known:0,estimated:0,missing:0,total:0})
const missingClosing=balance('pending','10','USD',{remote:{status:'8',cardCurrency:'USD'},balance_at:null,balance_error:'timeout'})
assert.deepEqual(summarizeCardBalances([missingClosing]),{totals:[],known:0,missing:1,failed:1,total:1})
assert.deepEqual(summarizeCardBalancesUSD([missingClosing]),{amount:null,known:0,estimated:0,missing:1,total:1})
for(const status of ['1','3','4','5','6','7','8']){
 const row=balance(status,'1','USD',{remote:{status,cardCurrency:'USD'},local:{enabled:false}})
 assert.equal(summarizeCardBalances([row]).known,1,'unclosed card funds remain included regardless of availability')
 assert.equal(summarizeCardBalancesUSD([row]).amount,'1.00')
}
console.log('Passed: completed closures excluded from balances/counts/errors, pending closures retained, issuer status fallback and status transitions')

const debit = { card_id: 'c', amount: '1000', currency: 'PHP' }
assert.equal(summarizeCardAmountsUSD([debit], [card], [open, increase]).amount, '16.05')
assert.equal(summarizeCardAmountsUSD([debit], [{ ...card, remote: { ...card.remote, status: '2' } }], [open]).amount, '16.05', 'closed card transaction history still converts')
assert.equal(summarizeCardAmountsUSD([{ ...debit, card_id: '', order_no: 'original' }], [], [{ ...open, card_id: '', order_no: 'wrong' }]).amount, null, 'blank IDs cannot match another opening')
assert.equal(summarizeCardAmountsUSD([{ ...debit, card_id: '', order_no: 'original' }], [], [{ ...open, card_id: '', order_no: 'original' }]).amount, '16.05')
assert.equal(summarizeCardAmountsUSD([{ ...debit, card_id: 'alias' }], [{ id: 'alias', remote: { orderNo: 'original' } }], [{ ...open, order_no: 'original' }]).amount, '16.05')
for (const result of [{ cardAmount: '0' }, { billAmount: '0' }, { billCurrency: 'EUR' }, { cardCurrency: 'JPY' }]) {
 assert.equal(summarizeCardAmountsUSD([debit], [card], [{ ...open, result: { ...open.result, ...result } }]).amount, null)
}
assert.equal(summarizeCardAmountsUSD([debit], [card], [{ ...open, status: 'failed' }, increase]).amount, null)
assert.deepEqual(summarizeCardAmountsUSD([debit, { amount: '0.1', currency: 'USD' }, { amount: '0.2', currency: 'USD' }], [], [open]), { amount: '16.35', known: 3, estimated: 1, missing: 0, total: 3 })
console.log('Passed: transaction USD estimates, closed-card history, order fallback, alias mapping, blank-ID isolation and invalid opening evidence')
