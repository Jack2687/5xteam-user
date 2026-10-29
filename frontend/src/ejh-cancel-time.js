// Polling changes updated_at; the request's created_at is the stable trigger.
export function cancellationTime(card, operations = []) {
  const operation = operations.filter(op => op.method === 'cancel' && op.status !== 'failed' &&
    (op.card_id === card.id || op.order_no && op.order_no === card.remote?.orderNo) &&
    Number.isFinite(Date.parse(op.created_at)) && new Date(op.created_at).getFullYear() > 1970)
    .sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at))[0]
  return { at: operation?.created_at || '', detected: !!operation?.note?.includes('EJH 页面已提交销卡') }
}

// Completed operations stop polling: updated_at is the persisted success
// confirmation, unlike the card's mutable sync/balance time or trigger time.
export function cancellationCompletionLookup(operations = []) {
  const cards = new Map(), orders = new Map()
  const remember = (map, key, at) => {
    if (key && (!map.has(key) || Date.parse(at) > Date.parse(map.get(key)))) map.set(key, at)
  }
  for (const op of operations) {
    if (op.method !== 'cancel' || op.status !== 'completed') continue
    const at = op.updated_at
    if (!Number.isFinite(Date.parse(at)) || new Date(at).getFullYear() <= 1970) continue
    remember(cards, op.card_id, at); remember(orders, op.order_no, at)
  }
  return card => cards.get(card.id) || orders.get(card.remote?.orderNo) || ''
}
