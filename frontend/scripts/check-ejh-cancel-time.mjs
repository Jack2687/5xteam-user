import assert from 'node:assert/strict'
import { cancellationTime } from '../src/ejh-cancel-time.js'
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
