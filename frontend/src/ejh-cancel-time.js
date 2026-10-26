// Polling changes updated_at; the request's created_at is the stable trigger.
export function cancellationTime(card, operations = []) {
  const operation = operations.filter(op => op.method === 'cancel' && op.status !== 'failed' &&
    (op.card_id === card.id || op.order_no && op.order_no === card.remote?.orderNo) &&
    Number.isFinite(Date.parse(op.created_at)) && new Date(op.created_at).getFullYear() > 1970)
    .sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at))[0]
  return { at: operation?.created_at || '', detected: !!operation?.note?.includes('EJH 页面已提交销卡') }
}
