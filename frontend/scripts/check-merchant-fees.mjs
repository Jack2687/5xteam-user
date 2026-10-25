import assert from 'node:assert/strict'
import {financeTotals,caseAmounts,ledgerFunds} from '../src/nexovcc-finance.js'
import {reconciliation} from '../src/nexovcc-reconciliation.js'
import {financeOverview,financeReport} from '../src/ejh-finance.js'
import {cardOrigin,oaiTemplates} from '../src/card-platform.js'
const at=new Date().toISOString()
const fee={request_id:'request',order_id:'order',card_id:'card',amount:'1.25',at}
const nex={cards:[{card:{id:'card',serial_no:'serial',status:'1',balance:'10.00',balance_at:at}}],cases:[],groups:[],finance:{at,funds:[],recharges:[]},balance:{amount:'10.00',at}}
const withNex={...nex,merchant_fees:[fee]}
assert.equal(financeTotals(withNex).cost-financeTotals(nex).cost,125)
assert.equal(financeTotals(withNex,'serial').merchantFee,125)
assert.equal(financeTotals(withNex,'other').merchantFee,0)
assert.equal(reconciliation(withNex).gap,reconciliation(nex).gap,'external fee changed issuer reconciliation')
assert.equal(caseAmounts({merchant_fee_usd:'1.25',match:{group:{valid:true,cost:'2.00'}}}).cost,325)
assert.equal(ledgerFunds(withNex).filter(x=>x.kind==='api-merchant').length,1)
const ejh={cards:[],cases:[],trades:[],operations:[],recharge_ledger:{amount:'0.00'},balance:{data:{abroadList:[{currency:'USD',balance:'0.00'}]}}}
const withEJH={...ejh,merchant_fees:[fee]}
assert.equal(financeOverview(withEJH).earnings.costUSD,'1.25')
assert.equal(financeOverview(withEJH).gap,financeOverview(ejh).gap)
assert.equal(financeReport(withEJH,{},'all').summary.merchantUSD,'1.25')
assert.equal(cardOrigin({id:'legacy',template_id:'t'},[],[{id:'t',platform:'cc',name:'edited'}]).platform,'oai')
assert.equal(cardOrigin({platform:'cc',template_name:'saved'},[],[]).name,'saved')
assert.equal(oaiTemplates([{id:1},{id:2,platform:'cc'},{id:3,platform:'oai'}]).length,2)
console.log('Passed: merchant account/card/total costs, unchanged issuer reconciliation, ledger attribution, legacy platform and template snapshots')
