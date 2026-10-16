import assert from 'node:assert/strict'
import { caseRequestTime, waitingRequestSummary } from '../src/ejhRequestTime.js'

const earlier={email:'first@example.test',state:'waiting',requested_at:'2026-10-16T09:30:00Z'}
const later={email:'second@example.test',state:'waiting',requested_at:'2026-10-16T18:00:00+08:00'}
const summary=waitingRequestSummary([later,{state:'opened'},earlier,{state:'returned',requested_at:'2026-01-01T00:00:00Z'}])
assert.equal(summary.count,2)
assert.equal(summary.at,earlier.requested_at)
assert.equal(summary.label,'最早申请')
assert.match(summary.title,/first@example.test/)
assert.match(summary.title,/second@example.test/)
assert.match(summary.title,/17:30:00/)
assert.equal(waitingRequestSummary([earlier]).label,'申请')
assert.equal(waitingRequestSummary([]).count,0)
assert.equal(caseRequestTime({}).at,undefined)
assert.equal(caseRequestTime({requested_at:'invalid'}).at,undefined)
const manual={state:'waiting',state_override:{state:'waiting',updated_at:'2026-10-16T17:31:00+08:00'}}
assert.equal(caseRequestTime(manual).label,'人工标记')
assert.equal(caseRequestTime({...manual,requested_at:earlier.requested_at}).label,'申请')
assert.equal(caseRequestTime({state_override:{state:'returned',updated_at:later.requested_at}}).at,undefined)
assert.match(waitingRequestSummary([{email:'old@example.test',state:'waiting'}]).title,/时间未记录/)
console.log('Passed: exact request dates, oldest card summary, missing times and manual-state labels')
