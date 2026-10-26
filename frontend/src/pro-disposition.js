export const dispositionLabels = { merge: '空间合并', deactivate: '删除账户', merchant: 'API 商户', merge_v2: '合并空间2.0' }
export const dispositionColumns = { merge: '四步状态', deactivate: '删除账户状态', merchant: 'API 商户状态', merge_v2: '合并空间2.0状态' }
export const filterDisposition = filter => ({ merged: 'merge', deleted: 'deactivate', merchant: 'merchant', merge_v2: 'merge_v2' }[filter] || '')
export function configuredDisposition(settings = {}) {
  return settings.auto_merge_v2_enabled ? 'merge_v2' : settings.auto_merchant_enabled ? 'merchant' : settings.auto_delete_enabled ? 'deactivate' : 'merge'
}
export function accountDisposition(account = {}, settings = {}, filter = '') {
  const fixed = filterDisposition(filter)
  if (fixed) return fixed
  const auto = account.pro_auto || {}
  if (dispositionLabels[auto.stage]) return auto.stage
  if (account.pro_merge_v2) return 'merge_v2'
  if (account.pro_merchant?.request_id) return 'merchant'
  if (account.pro_deactivation) return 'deactivate'
  if (account.space_merged_once || ['running', 'completed', 'unknown', 'failed'].includes(account.pro_transfer_status)) return 'merge'
  const steps = account.pro_stage_progress?.steps || auto.steps || {}
  return Object.keys(dispositionLabels).find(key => steps[key] && !['pending', 'not_started'].includes(steps[key])) || configuredDisposition(settings)
}
export function normalizeOAISettings(value) {
  return Object.fromEntries(Object.keys(dispositionLabels).map(mode => [mode, { enabled: false, delay_seconds: 300, retry_interval_seconds: 300, max_attempts: 3, ...(value?.[mode] || {}) }]))
}
