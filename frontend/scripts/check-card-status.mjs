import assert from 'node:assert/strict'
import { cardUsage, cardOccupancy, paymentStatusLabel, isClosedCard } from '../src/card-status.js'
const now = new Date('2026-10-15T07:00:00Z')
const active = { source: 'ejh', issuer_status: '1', issuer_order_no: 'fixture', enabled: true, available: true, exp_year: 2031, exp_month: 10, remaining_accounts: 1 }
for (const enabled of [true, false]) {
  const closed = { ...active, enabled, issuer_status: '2' }
  assert.equal(isClosedCard(closed), true)
  assert.equal(cardUsage(closed, now).label, '已销卡', 'Closure must override even stale available=true')
  assert.equal(cardUsage(closed, now).detail, '不能用于开通')
  const dashboard = { local: closed, remote: { status: '2', isLocked: '1' }, error: 'missing card details' }
  assert.deepEqual(cardUsage(dashboard, now), cardUsage(closed, now), 'Both views must agree; a secondary lock/error must not hide closure')
}
assert.equal(cardUsage(active, now).label, '可用于开通')
assert.equal(cardUsage({ ...active, source: '', card_type: 'other', issuer_status: '2' }, now).label, '已销卡', 'Linked physical aliases still carry issuer restrictions')
assert.equal(isClosedCard({ card_type: 'ejh', enabled: true, available: true }), false, 'A manual EJH label is not a supplier binding')
assert.equal(cardUsage({ ...active, issuer_status: '8' }, now).label, '销卡处理中')
for(const status of ['processing','unknown','submitting']){
 const pending={...active,issuer_locked:true,issuer_cancel_status:status}
 assert.equal(cardUsage(pending,now).label,status==='processing'?'销卡处理中':'销卡结果确认中')
 assert.equal(cardUsage({local:pending,remote:{status:'3'}},now).tone,'pending')
 assert.equal(cardUsage({...pending,issuer_status:'2'},now).label,'已销卡')
}
assert.equal(cardUsage({ ...active, issuer_status: '5' }, now).label, '卡台状态受限')
assert.equal(cardUsage({ ...active, issuer_status: '4' }, now).label, '未激活')
assert.equal(cardUsage({ ...active, issuer_status: '' }, now).label, '卡台状态待核实')
assert.equal(cardUsage({ ...active, issuer_end: '2026-10-15 14:59:59' }, now).label, '已过期')
assert.equal(cardUsage({ ...active, issuer_end: '2026-10-15 15:00:01' }, now).label, '可用于开通')
assert.equal(cardUsage({ ...active, issuer_end: 'bad' }, now).label, '到期时间待核实')
assert.equal(cardUsage({ ...active, exp_year: 2025 }, now).label, '已过期')
assert.equal(cardUsage({ ...active, enabled: false }, now).label, '已禁用')
assert.equal(cardUsage({ ...active, single_active_account: true, active_accounts: 1 }, now).label, '使用中')
assert.equal(cardUsage({ ...active, remaining_accounts: 0 }, now).label, '名额已用尽')
assert.equal(cardUsage({ ...active, available: false }, now).label, '暂不可用')
assert.equal(cardUsage({ ...active, available: undefined }, now).label, '暂不可用')
console.log('Passed: shared card status, closure precedence, physical aliases, manual cards, expiration, occupancy and capacity')
for (const [status, label] of [['reserved','开通前预留'],['processing','开通处理中'],['manual_review','支付待核实'],['submission_unknown','支付待核实'],['success','使用中']]) {
 const held = {...active, single_active_account:true, active_accounts:1, remaining_accounts:0, occupants:[{email:'fixture@example.com',status,deleted:false}]}
 assert.equal(cardUsage(held,now).label,label)
 assert.equal(cardUsage({local:held},now).label,label)
 assert.ok(paymentStatusLabel(status))
 assert.equal(cardUsage({...held,occupants:[{...held.occupants[0],deleted:true}]},now).label,'已删除账号占用待核实')
 assert.equal(cardUsage({...held,issuer_status:'2'},now).label,'已销卡')
 assert.equal(cardUsage({...held,single_active_account:false,remaining_accounts:1},now).label,'可用于开通')
}
assert.equal(cardOccupancy({...active,occupants:[{status:'success'},{status:'manual_review'}]}).label,'支付待核实')
console.log('Passed: prepayment reservations, unresolved/deleted occupancy, issuer precedence and parallel capacity')

for (const source of ['ejh', 'nexovcc']) {
 const disabled = {...active, source, issuer_disabled: true}
 assert.equal(cardUsage(disabled, now).label, '卡台已停用', 'Merchant switch overrides a stale available flag')
 assert.equal(cardUsage({local: disabled}, now).label, '卡台已停用')
 assert.equal(cardUsage({...disabled, issuer_status: source === 'ejh' ? '2' : '4'}, now).label, '已销卡')
 assert.equal(cardUsage({...disabled, issuer_disabled: false}, now).label, '可用于开通')
}
console.log('Passed: disabled issuer blocks Pro use while preserving physical closure status')

assert.equal(cardUsage({...active,issuer_locked:true},now).label,'操作待核实')
assert.equal(cardUsage({local:{...active,issuer_locked:true},remote:{status:'3',isLocked:'0'}},now).label,'操作待核实')
