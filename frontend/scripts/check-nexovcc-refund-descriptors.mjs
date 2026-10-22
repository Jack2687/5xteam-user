import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import {caseDetails} from '../src/nexovcc-case-details.js'
import {financeTotals, rmb} from '../src/nexovcc-finance.js'
import {cardFunding, cardBalanceDisplay, frozenBalanceDisplay} from '../src/nexovcc-reconciliation.js'

// These expected groups are also checked against the Go parser. The redacted
// issuer ledger contains authorization, settlement, refund and changing names.
const {expected_groups:groups,...finance}=JSON.parse(readFileSync(new URL('../../internal/nexovcc/testdata/refund-descriptors.json',import.meta.url),'utf8'))
const refunded=groups.find(g=>g.authorization==='payment')
const reversed=groups.find(g=>g.authorization==='prior-reversed')
const row={id:'refunded-account',card_id:'card',state:'returned',manual_state:'returned',payment_status:'success',revenue_r:218.07,match:{group:refunded},shared_fees:{ready:true,applied:true,count:2,opening:'0.25',monthly:'0.10'}}
const details=caseDetails(row,finance)
assert.equal(details.returned,14123)
assert.equal(details.paid,14182)
assert.equal(details.difference,59)
assert.equal(details.cardLoss,59)
assert.equal(details.settlement,0,'the original settled principal is refunded, not a card cost')
assert.equal(details.transaction,242)
assert.equal(details.refundFee,474)
assert.equal(details.cost,810,'include actual fees, difference and allocated card fees once')
assert.equal(details.profit,16380)
assert.equal(details.manualUnconfirmed,false,'real refund supersedes the warning, preserving the manual state')
assert.equal(details.refundCompleted,true)
assert.equal(details.returnedAt,'2026-10-22T02:00:51+08:00')
assert.equal(details.pending,0)
assert.equal(details.trades.length,3)
const priorDetails=caseDetails({state:'returned',match:{group:reversed}},finance)
assert.equal(priorDetails.transaction,342,'the additional 1 USD fee remains assigned after its tiny settlement arrives')
assert.equal(priorDetails.settlement,2)
assert.equal(priorDetails.cost,644)
assert.equal(priorDetails.relatedTrades.length,2)
assert.deepEqual(details.merchantPaid,[{currency:'PHP',amount:891964}])
assert.deepEqual(details.merchantReturned,[{currency:'PHP',amount:889846}])

const card={id:'card',serial_no:'fixture-refund-card',status:'4',balance:'1.00',balance_at:finance.at,synced_at:finance.at}
const data={finance,groups,cards:[{card}],balance:{amount:'616.01',at:finance.at},operations:[],cases:[row,{id:'prior',card_id:'card',state:'returned',payment_status:'success',revenue_r:23.11,match:{group:reversed}}]}
const totals=financeTotals(data,card.serial_no)
assert.equal(totals.cost,1489)
assert.equal(rmb(totals.cost),'99.76')
assert.equal(totals.profit,14142)
assert.equal(totals.cardLoss,59)
assert.equal(totals.settlement,2,'separate real tiny settlement remains in cost')
assert.equal(totals.unverified,0)
assert.equal(totals.unmatched,0)
assert.equal(totals.pending,0)
assert.equal(cardFunding(data,data.cards[0]).closurePending,0)
assert.equal(cardBalanceDisplay(data,data.cards[0]).amount,'0.00')
assert.equal(frozenBalanceDisplay(data).current,0)
console.log('Passed: real changing-descriptor refund across account amounts, manual return warning, fees, card totals, profit and closed-card balances')
