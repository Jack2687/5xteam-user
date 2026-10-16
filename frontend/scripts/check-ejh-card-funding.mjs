import assert from 'node:assert/strict'
import { cardOpeningAmounts, cardFundedAmounts, cardClosureAmounts } from '../src/ejh-card-funding.js'

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
