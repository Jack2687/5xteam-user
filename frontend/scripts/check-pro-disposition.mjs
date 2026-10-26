import assert from 'node:assert/strict'
import { accountDisposition, normalizeOAISettings } from '../src/pro-disposition.js'

const modes = ['merge','deactivate','merchant','merge_v2']
const filters = ['merged','deleted','merchant','merge_v2']
for (const configured of modes) {
  const settings = {auto_merge_enabled:configured==='merge',auto_delete_enabled:configured==='deactivate',auto_merchant_enabled:configured==='merchant',auto_merge_v2_enabled:configured==='merge_v2'}
  for (let i=0;i<modes.length;i++) {
    const mode=modes[i]
    const row={pro_auto:{stage:mode,steps:{[mode]:'completed'}}}
    assert.equal(accountDisposition(row,settings,filters[i]),mode)
    assert.equal(accountDisposition(row,settings,'all'),mode)
    // A record with several historical outcomes still follows the selected tab.
    assert.equal(accountDisposition({pro_merge_v2:{},pro_deactivation:{},pro_merchant:{request_id:'a'},space_merged_once:true},settings,filters[i]),mode)
  }
  assert.equal(accountDisposition({},settings),configured)
}
assert.equal(accountDisposition({pro_auto:{steps:{merge:'pending'}}},{auto_delete_enabled:true}),'deactivate')
assert.equal(accountDisposition({pro_auto:{steps:{deactivate:'completed'}}},{auto_merge_v2_enabled:true}),'deactivate')
const defaults=normalizeOAISettings()
assert.equal(Object.values(defaults).every(rule=>!rule.enabled&&rule.delay_seconds===300),true)
assert.equal(normalizeOAISettings({merge:{enabled:true,delay_seconds:18}}).merge.delay_seconds,18)
assert.equal(normalizeOAISettings({merge:{enabled:true,delay_seconds:18}}).merchant.delay_seconds,300)
console.log('PASS: historical filters and recorded flow override current settings; independent OAI defaults')
