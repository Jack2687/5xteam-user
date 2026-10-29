import assert from 'node:assert/strict'
import { cancellationTime, cancellationCompletionLookup } from '../src/ejh-cancel-time.js'
const card={id:'card-1',remote:{orderNo:'issuer-order'}}
const first={method:'cancel',card_id:card.id,status:'processing',created_at:'2026-10-20T10:00:00+08:00',updated_at:'2026-10-26T11:00:00+08:00'}
const ops=[first,{...first,method:'open',created_at:'2026-10-22T10:00:00+08:00'},{...first,card_id:'other',created_at:'2026-10-24T10:00:00+08:00'},{...first,status:'failed',created_at:'2026-10-25T10:00:00+08:00'}]
assert.equal(cancellationTime(card,ops).at,first.created_at)
assert.equal(cancellationTime(card,[{...first,status:'completed',updated_at:'2026-10-28T12:00:00+08:00'}]).at,first.created_at)
assert.equal(cancellationTime(card,[{...first,card_id:'',order_no:'issuer-order'}]).at,first.created_at)
assert.deepEqual(cancellationTime(card,[]),{at:'',detected:false})
assert.equal(cancellationTime(card,[{...first,created_at:'0001-01-01T00:00:00Z'}]).at,'')
assert.equal(cancellationTime(card,[{...first,note:'EJH 页面已提交销卡，后台仅查询处理结果'}]).detected,true)
console.log('EJH cancellation trigger-time checks passed')

const {sortCancellationCards}=await import('../src/ejh.js')
const closed=id=>({id,remote:{status:'2',orderNo:id},local:{created_at:'2026-10-29T10:00:00+08:00'}})
const rows=[closed('older'),closed('undated'),{id:'processing',remote:{status:'8'}},closed('newer'),{id:'unknown',local:{issuer_cancel_status:'unknown'}}]
const completed=(id,at)=>({...first,card_id:id,order_no:id,status:'completed',updated_at:at})
const evidence=[completed('older','2026-10-28T15:00:00+08:00'),completed('newer','2026-10-29T09:00:00+08:00'),{...completed('older','2026-10-30T10:00:00+08:00'),status:'failed'},{...completed('undated','2026-10-30T12:00:00+08:00'),method:'balance'}]
assert.deepEqual(sortCancellationCards(rows,evidence).map(c=>c.id),['processing','unknown','newer','older','undated'])
assert.deepEqual(rows.map(c=>c.id),['older','undated','processing','newer','unknown'],'sorting mutated input')
assert.equal(cancellationCompletionLookup(evidence)(closed('newer')),'2026-10-29T09:00:00+08:00')
assert.equal(cancellationCompletionLookup(evidence)(closed('undated')),'','unrelated operation invented success time')
assert.equal(cancellationCompletionLookup([{...completed('undated','invalid'),created_at:'2026-10-29T09:00:00+08:00'}])(closed('undated')),'')
assert.equal(cancellationCompletionLookup([{...completed('','2026-10-29T09:00:00+08:00'),order_no:'newer'}])(closed('newer')),'2026-10-29T09:00:00+08:00')
console.log('Passed: pending first, cancellation success time descending, absent/invalid/unrelated dates last, stable and immutable sorting')
