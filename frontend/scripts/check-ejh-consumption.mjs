import assert from 'node:assert/strict'
import { accountConsumption, consumptionText, cardConsumption } from '../src/ejh-consumption.js'

const account = { email: 'One@example.com', consumption_total: 67.0766, cost_downstream_snapshot: 335.3832 }
assert.equal(consumptionText(accountConsumption(account)), '67.0766 R')
assert.equal(consumptionText(accountConsumption({ consumption_total: 0 })), '0.0000 R')
for (const value of [undefined, null, '', -1, NaN, Infinity]) {
  assert.equal(consumptionText(accountConsumption({ consumption_total: value })), '未查询')
}
assert.deepEqual(cardConsumption([
  account,
  { ...account, id: 'another-order', email: ' one@EXAMPLE.com ' },
  { email: 'two@example.com', consumption_total: 2.1234 },
  { email: 'zero@example.com', consumption_total: 0 },
  { email: 'unknown@example.com' },
]), { text: '69.2000 R', missing: 1 })
assert.deepEqual(cardConsumption([]), { text: '0.0000 R', missing: 0 })
assert.deepEqual(cardConsumption([{ email: 'unknown@example.com' }]), { text: '未查询', missing: 1 })
assert.deepEqual(cardConsumption([{ email: 'One@example.com' }, account]), { text: '67.0766 R', missing: 0 })
console.log('Passed: user consumption, R display, account deduplication, card totals, known zero and missing data')
