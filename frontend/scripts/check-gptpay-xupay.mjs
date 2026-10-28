import assert from 'node:assert/strict'
import { catalogProPlans, paymentCountries, paymentRetryBlocked, supplierCredits, payProviderName, xupayStates } from '../src/gptpay.js'
assert.equal(payProviderName('xupay'), '序付 · XUPAY')
assert.deepEqual(catalogProPlans(null, 'xupay').map(p => p.code), ['pro5', 'pro20'])
assert(!paymentCountries('xupay').some(c => c.code === 'CL'))
assert(paymentCountries('xupay').some(c => c.code === 'SG'))
assert.equal(supplierCredits(12345, 'xupay'), '123.45 R')
const catalog = { products: [{ code: 'pro20', creditPrice: 123, enabled: true }], countries: [{ code: 'PH' }] }
assert.equal(catalogProPlans(catalog, 'xupay')[1].label, 'Pro 20x · 1.23 R')
assert.equal(catalogProPlans(catalog, 'xupay')[0].available, false)
assert(paymentRetryBlocked({ provider: 'xupay', status: 'success', remote: { paymentState: 'succeeded' } }))
assert(paymentRetryBlocked({ provider: 'xupay', status: 'manual_review', remote: {} }))
assert(!paymentRetryBlocked({ provider: 'xupay', status: 'failed', remote: { paymentState: 'failed' } }))
assert.match(xupayStates({ paymentState: 'succeeded', activationStatus: 'review' }), /已付款.*待核实/)
console.log('XUPAY UI contract passed: plans, countries, R units, paid fence and activation state')
