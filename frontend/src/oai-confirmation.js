import { dispositionLabels } from './pro-disposition.js'

export const defaultOAIInquiry = {
  prompt: '请查询订阅邮箱【{{email}}】对应的最近一笔网页订阅退款，确认是否已经发起或处理，并给我预计入账时间和发票号。请以实际查询结果为准，不要仅凭我的描述推断；这次仅查询，不要新建退款、取消订阅或删除账户申请。\n如果确认退款已发起或已处理，请在最后一行仅输入：{{success_marker}}\n如果确认退款未发起或没有退款，请在最后一行仅输入：{{failure_marker}}\n如果无法查询或不能确认，请在最后一行仅输入：{{review_marker}}',
  success_marker: '已成功', failure_marker: '已失败', review_marker: '待核实',
}

export function caseOAIRecords(row = {}) {
  const records = Object.fromEntries(Object.entries(row.pro_oai_confirmations || {}).filter(([,r]) => {
    if (r?.order_id) return r.order_id === row.id
    return !(Date.parse(row.created_at) > Date.parse(r?.completed_at))
  }))
  if (row.state !== 'returned') return records
  const modes = new Set([...Object.keys(records), ...(row.pro_oai_available_modes || [])])
  if (!modes.size) modes.add('returned')
  return Object.fromEntries([...modes].map(mode => {
    const record = records[mode]
    if (record?.status === 'confirmed' && record.confirmed_at) return [mode, record]
    return [mode, {...record, mode, status:'confirmed', confirmation_source:'card_return', confirmed_at:row.state_override?.updated_at || row.match?.group?.returned_at || undefined, next_attempt_at:null, error:'', steps:{wait:'skipped', login:'skipped', authorize:'skipped', send:'skipped', reply:'skipped', confirm:'confirmed'}}]
  }))
}

const recordConfirmed = r => r?.status === 'confirmed' && (r.confirmed_at || r.confirmation_source === 'card_return')
export const caseOAIConfirmed = row => Object.values(caseOAIRecords(row)).some(recordConfirmed)
export const waitingOAIConfirmedCount = rows => rows.filter(r => r.state === 'waiting' && caseOAIConfirmed(r)).length

export const waitingOAIReplies = rows => rows.filter(row => row.state === 'waiting').flatMap(row =>
  Object.entries(caseOAIRecords(row)).filter(([,record]) => recordConfirmed(record)).map(([mode,record]) => ({
    key: `${row.id}:${mode}`,
    label: `${row.email || row.id} · ${dispositionLabels[mode] || 'OAI 确认'}`,
    record,
  }))
)
