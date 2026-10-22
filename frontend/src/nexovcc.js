export const providerName = value => value === 'nexovcc' ? '环球' : value === 'ejh' ? 'EJH' : '其他'
export const operationNames = {open:'开卡',deposit:'充值',withdraw:'自定义提现',withdraw_all:'提现留 1U',withdraw_cancel:'提现留 1U 并销卡',freeze:'冻结',unfreeze:'解冻',cancel:'一键销卡',remark:'修改备注',card_finance:'同步交易明细',pending_sync:'刷新正常待定卡'}
export const operationStates = {submitted:'已提交',processing:'处理中',import_pending:'待同步入库',unknown:'结果待核实',succeeded:'成功',failed:'失败'}
export const returnStates = {unopened:'未开通',opened:'已开通',waiting:'已申请待回款',returned:'已回款'}
export const terminal = op => ['succeeded','failed'].includes(op.status)
// Vue's number inputs produce numbers even without v-model.number. The API
// expects decimal strings; preserve their value without rounding or defaults.
export function nexovccSettingsPayload(settings) {
 return {...settings,templates:settings.templates.map(t=>({...t,
  product_id:templateProductIDs(t)[0]||'',product_ids:templateProductIDs(t),holder_id:String(t.holder_id??''),
  amount:String(t.amount??''),fee_buffer:String(t.fee_buffer??'')
 }))}
}
export function templateProductIDs(template){
 return (Array.isArray(template.product_ids)?template.product_ids:template.product_id?[template.product_id]:[]).map(String)
}
export function moveProduct(ids,from,to){
 const next=[...ids]
 if(from<0||to<0||from>=next.length||to>=next.length)return next
 next.splice(to,0,next.splice(from,1)[0]);return next
}
export function nexovccOperationPayload(input) {
 return {...input,...(Object.hasOwn(input,'amount')?{amount:String(input.amount??'')}:{})}
}
export function cardState(row) {
 if (row.card.status === '4') return '已销卡'
 if (row.local?.issuer_cancel_status) return '销卡处理中'
 if (row.card.control === 'frozen' || row.local?.issuer_frozen) return '已冻结'
 if (row.card.error || !row.card.imported) return '资料待同步'
 if (row.local?.issuer_locked) return '操作待核实'
 return row.card.status === '1' ? '正常' : `不可用（${row.card.status || '未知'}）`
}
export function cancelReason(row, operations) {
 if (row.card.status === '4') return '已销卡'
 if (operations.some(op => op.card_id === row.card.id && !terminal(op))) return '先核实原操作'
 if (row.local?.active_accounts || row.local?.pending_accounts) return '仍有付款或回款占用'
 if (!row.card.imported) return '请先同步卡片资料'
 return ''
}
export const requestID = () => 'nex-' + crypto.randomUUID().replaceAll('-','').slice(0,24)
let lastSync = 0
export const refreshDue = seconds => Date.now() - lastSync >= seconds * 1000
export const markRefreshed = () => { lastSync = Date.now() }
