// Both the Pro list and EJH dashboard expose the same saved issuer snapshot.
// The local enable switch alone never establishes that a card can be used.
function cardInfo(card) {
  const local = card?.local || card || {}
  const linked = local.source === 'ejh' || !!local.issuer_order_no || (!!card?.remote && card.issuer_linked !== false)
  return { local, linked, status: String(card?.remote?.status ?? local.issuer_status ?? ''), locked: String(card?.remote?.isLocked) === '1' || local.issuer_locked, end: card?.remote?.endTime || local.issuer_end }
}
export function isClosedCard(card) {
  const { linked, status } = cardInfo(card)
  return linked && status === '2'
}
export function cardUsage(card, now = new Date()) {
  const { local, linked, status, locked, end } = cardInfo(card)
  const blocked = (label, detail = '不能用于开通', tone = 'danger') => ({ label, detail, tone })
  if (linked) {
    if (status === '2') return blocked('已销卡')
    if (status === '8') return blocked('销卡处理中', '暂不能用于开通', 'pending')
    if (card?.error) return blocked('资料待核实', '核实卡台资料后才能开通', 'pending')
    if (locked || status === '5') return blocked('已冻结 / 受限')
    if (!['1', '3'].includes(status)) {
      return blocked(({ '4': '未激活', '6': '销卡失败，待核实', '7': '非活跃' })[status] || '卡台状态待核实')
    }
    if (end) {
      // Issuer dates use Asia/Shanghai, independent of the browser timezone.
      const timestamp = /^[0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}$/.test(end) ? Date.parse(end.replace(' ', 'T') + '+08:00') : NaN
      if (!Number.isFinite(timestamp)) return blocked('到期时间待核实')
      if (timestamp <= now.getTime()) return blocked('已过期')
    }
  }
  if (local.exp_year && local.exp_month && new Date(local.exp_year, local.exp_month, 1) <= now) return blocked('已过期')
  if (!local.enabled) return blocked('已禁用', '本地开关已关闭', 'pending')
  if (local.single_active_account && local.active_accounts > 0) return blocked('使用中', '移出当前账号后才可再次分配', 'pending')
  if (local.remaining_accounts != null && local.remaining_accounts <= 0) return blocked('名额已用尽', '已开通和在途占用全部名额', 'pending')
  if (local.available === true) return { label: '可用于开通', detail: '本地开关已启用', tone: 'success' }
  return blocked('暂不可用', '请核对卡片资料及最新状态', 'pending')
}
