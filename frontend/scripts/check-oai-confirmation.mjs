import assert from 'node:assert/strict'
import {caseOAIRecords,caseOAIConfirmed,waitingOAIConfirmedCount} from '../src/oai-confirmation.js'
const base={id:'order-1',email:'test@example.com',state:'waiting',created_at:'2026-10-25T00:00:00Z',pro_oai_confirmations:{merge_v2:{mode:'merge_v2',order_id:'order-1',status:'confirmed',confirmed_at:'2026-10-26T00:00:00Z',answer:'退款已处理。\n已成功'}}}
assert.equal(caseOAIConfirmed(base),true)
assert.equal(waitingOAIConfirmedCount([base,{...base,id:'other'}]),1)
assert.equal(caseOAIConfirmed({...base,id:'other'}),false)
const returned={...base,state:'returned',pro_oai_confirmations:{merge_v2:{status:'review',order_id:'order-1',answer:'无法查询',replies:[{answer:'无法查询'}]}}}
const result=caseOAIRecords(returned).merge_v2
assert.equal(result.status,'confirmed')
assert.equal(result.confirmation_source,'card_return')
assert.equal(result.confirmed_at,undefined,'do not fabricate a confirmation timestamp')
assert.equal(result.answer,'无法查询','retain actual Help answer')
assert.equal(result.replies.length,1)
assert.equal(caseOAIConfirmed(returned),true)
assert.equal(returned.pro_oai_confirmations.merge_v2.status,'review','do not mutate source record')
assert.equal(caseOAIRecords({...base,state:'returned'}).merge_v2.confirmation_source,undefined,'preserve genuine Help confirmation')
assert.equal(caseOAIConfirmed({state:'returned'}),true)
assert.equal(caseOAIConfirmed({...base,created_at:'2026-10-27T00:00:00Z',pro_oai_confirmations:{merge:{status:'confirmed',confirmed_at:'2026-10-26T00:00:00Z',completed_at:'2026-10-26T00:00:00Z'}}}),false)
console.log('OAI confirmation projections passed')
