export function costProvider(account) {
  if (account?.vm2api) return 'vm2api'
  return account?.push_provider || (account?.cpa_auth_file_name ? 'cpa' : account?.sub2_account_id ? 'sub2' : '')
}

export function costSupported(account) {
  return !['cpa', 'vm2api'].includes(costProvider(account))
}

export function costText(account) {
  if (!costSupported(account)) return '不支持'
  if (!account?.cost_checked_at) return '未查询'
  return `$${Number(account.cost_downstream_snapshot || 0).toFixed(4)}`
}

export function userCostText(account) {
  if (!account?.user_cost_downstream_identity) return '用户 --'
  return `用户 $${Number(account.user_cost_downstream_snapshot || 0).toFixed(4)}`
}
