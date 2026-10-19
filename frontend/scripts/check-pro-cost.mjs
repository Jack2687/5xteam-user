import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { costProvider, costSupported, costText, userCostText } from '../src/proCost.js'

assert.equal(costText({}), '未查询')
assert.equal(userCostText({}), '用户 --')
const account = { push_provider:'sub2', cost_checked_at:'2026-10-15T00:00:00Z', cost_downstream_snapshot:12.34567, user_cost_downstream_identity:'42', user_cost_downstream_snapshot:3.25, total_cost_usd:999 }
assert.equal(costText(account), '$12.3457')
assert.equal(userCostText(account), '用户 $3.2500')
assert.equal(costText({ ...account, cost_downstream_snapshot:0 }), '$0.0000')
assert.equal(userCostText({ ...account, user_cost_downstream_snapshot:0 }), '用户 $0.0000')
for (const p of [{push_provider:'cpa'}, {push_provider:'vm2api'}, {vm2api:{slot_id:'fixture'}}, {cpa_auth_file_name:'fixture.json'}]) {
  assert.equal(costSupported(p), false)
  assert.equal(costText(p), '不支持')
}
assert.equal(costProvider({sub2_account_id:42}), 'sub2')
const view = readFileSync(new URL('../src/components/ProManagementView.vue', import.meta.url), 'utf8')
assert.ok(view.includes('消耗额度</th><th>5小时'))
assert.ok(view.includes('<ProCostCell :account="account"'))
assert.ok(view.includes('colspan="15"'))
assert.ok(view.includes("runBatch('quota')"))
assert.ok(view.includes("runOne(account, 'quota')"))
assert.equal((view.split('<thead>')[1].split('</thead>')[0].match(/<th[ >]/g) || []).length, 15)
console.log('Passed: Pro cost display, precision, zero/missing values, provider support and 15-column table')
