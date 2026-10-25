export const isOAICard = item => !item?.platform || item.platform === 'oai'
export const oaiTemplates = templates => (templates || []).filter(isOAICard)

// Legacy cards remain OAI even if their old template is later changed to CC.
export function cardOrigin(row, operations = [], templates = []) {
 const card = row.card || row
 const op = operations.find(o => (o.action === 'open' || o.method === 'open') &&
  (o.card_id === card.id || o.order_no && o.order_no === card.remote?.orderNo || o.serial_no && o.serial_no === card.serial_no))
 const id = card.template_id || op?.template_id || ''
 return { platform: card.platform || op?.platform || 'oai',
  name: card.template_name || op?.template_name || templates.find(t => t.id === id)?.name || (id ? '已移除模板' : '未记录模板') }
}
