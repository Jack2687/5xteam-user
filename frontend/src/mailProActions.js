export function oaiStatusLabel(records = {}) {
  const items = Object.values(records).filter(Boolean)
  if (items.some(item => item.status === 'running')) return '确认中'
  if (items.some(item => item.status === 'waiting' || item.next_attempt_at)) return '等待确认'
  if (items.length && items.every(item => item.status === 'confirmed')) return '已确认'
  if (items.some(item => item.status === 'review')) return '待核实'
  if (items.some(item => item.status === 'failed')) return '确认失败'
  if (items.some(item => item.status === 'unconfirmed')) return '未确认'
  if (items.some(item => item.status === 'stopped')) return '已停止'
  return '查看结果'
}

export function mailFinalStatusLabel(account) {
  if (account.pro_workflow_running) return '执行中'
  const mode = account.mail_pro_actions?.final_mode
  const record = { merge_v2: account.pro_merge_v2, merchant: account.pro_merchant, deactivate: account.pro_deactivation }[mode]
  const completed = mode === 'merge_v2' ? !!record?.completed_at
    : mode === 'merchant' ? !!record?.retired_at && record.status === 'completed'
    : mode === 'deactivate' ? !!record?.confirmed_at
    : account.pro_transfer_status === 'completed' && ['completed', 'team_removed'].includes(account.pro_remove_status)
  if (completed) return '已完成'
  if (record?.status === 'unknown' || account.pro_transfer_status === 'unknown') return '待核实'
  if (account.pro_auto?.stop_requested) return '已停止'
  if (record?.error || account.pro_last_error) return '待处理'
  if (record?.next_check_at) return '等待查询'
  return '查看进度'
}

export function mailActionsPending(account) {
  if (!account.mail_pro_actions) return false
  if (Object.values(account.pro_oai_confirmations || {}).some(v => v && (['running', 'waiting'].includes(v.status) || v.next_attempt_at))) return true
  if (account.pro_auto?.stop_requested) return false
  return !!(account.pro_workflow_running || account.pro_merchant?.next_check_at || account.pro_deactivation?.next_check_at || account.pro_merge_v2?.auto_resume_at)
}
