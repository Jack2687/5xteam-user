// Both the Pro list and EJH dashboard expose the same saved issuer snapshot.
// The local enable switch alone never establishes that a card can be used.
function cardInfo(card) {
  const local = card?.local || card || {}
  const linked = local.source === 'ejh' || !!local.issuer_order_no || (!!card?.remote && card.issuer_linked !== false)
  return { local, linked, status: String(card?.remote?.status ?? local.issuer_status ?? ''), locked: String(card?.remote?.isLocked) === '1' || local.issuer_locked, end: card?.remote?.endTime || local.issuer_end }
}
export function isClosedCard(card) {
  const { linked, status } = cardInfo(card)
  return cardInfo(card).local.source==='nexovcc' ? status==='4' : linked && status === '2'
}
export function paymentStatusLabel(status) {
  return ({ reserved: '开通前预留', created: '订单待提交', submitting: '订单提交中', processing: '开通处理中', submission_unknown: '提交结果未知', manual_review: '支付待核实', success: '开通成功', failed: '开通失败' })[status] || '订单状态待核实'
}
export function cardOccupancy(card) {
  const { local } = cardInfo(card)
  const occupants = local.occupants || []
  if (occupants.some(row => row.deleted)) return { label: '已删除账号占用待核实', detail: '账号已删除，原订单仍占用；请核实开通结果；成功订单须等待确认回款' }
  if (occupants.some(row => !['reserved', 'created', 'submitting', 'processing', 'success'].includes(row.status))) return { label: '支付待核实', detail: '先查询原开通订单；确认失败后释放占用' }
  if (occupants.some(row => ['created', 'submitting', 'processing'].includes(row.status))) return { label: '开通处理中', detail: '等待原开通订单结果；确认失败后释放占用' }
  if (occupants.some(row => row.status === 'reserved')) return { label: '开通前预留', detail: '登录或开通准备占位，停止任务后释放' }
  return { label: '使用中', detail: '当前账号回款确认后才可再次分配' }
}
export function cardUsage(card, now = new Date()) {
  const { local, linked, status, locked, end } = cardInfo(card)
  const blocked = (label, detail = '不能用于开通', tone = 'danger') => ({ label, detail, tone })
  if (local.source==='nexovcc') {
    if (status==='4') return blocked('已销卡')
    if (local.issuer_cancel_status) return blocked('销卡处理中','等待卡片状态及余额回款核实','pending')
    if (local.issuer_frozen) return blocked('已冻结','可解冻或执行销卡','pending')
    if (locked) return blocked('操作待核实','请到环球卡台查询原操作','pending')
    if (status!=='1') return blocked('卡台状态不可用',`环球状态 ${status||'未知'}`)
  } else if (linked) {
    if (status === '2') return blocked('已销卡')
    if (status === '8' || local.issuer_cancel_status === 'processing') return blocked('销卡处理中', '卡台正在处理，完成后自动更新，无需重复提交', 'pending')
    if (['submitting','unknown'].includes(local.issuer_cancel_status)) return blocked('销卡结果确认中', '后台正在查询卡台结果，请勿重复提交', 'pending')
    if (card?.error) return blocked('资料待核实', '核实卡台资料后才能开通', 'pending')
    if (local.issuer_frozen || status === '5' || String(card?.remote?.isLocked) === '1') return blocked('卡台状态受限', 'EJH 返回的卡片状态暂不可用于开通')
    if (locked) return blocked('操作待核实', '有尚未确认的卡片操作，请查看 EJH 操作记录', 'pending')
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
  if (local.issuer_disabled) return blocked('卡台已停用', '开启对应卡台后才能用于新的 Pro 开通', 'pending')
  if (local.exp_year && local.exp_month && new Date(local.exp_year, local.exp_month, 1) <= now) return blocked('已过期')
  if (!local.enabled) return blocked('已禁用', '本地开关已关闭', 'pending')
  if (local.active_accounts > 0 && (local.single_active_account || local.remaining_accounts <= 0)) {
    const occupancy = cardOccupancy(card)
    return blocked(occupancy.label, occupancy.detail, 'pending')
  }
  if (local.remaining_accounts != null && local.remaining_accounts <= 0) return blocked('名额已用尽', '已开通和在途占用全部名额', 'pending')
  if (local.available === true) return { label: '可用于开通', detail: '本地开关已启用', tone: 'success' }
  return blocked('暂不可用', '请核对卡片资料及最新状态', 'pending')
}
