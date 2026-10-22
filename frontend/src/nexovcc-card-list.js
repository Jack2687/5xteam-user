const validTime = value => value && !String(value).startsWith('0001') && Number.isFinite(Date.parse(value))

function closedTime(row, operations) {
 const card = row.card || {}
 if (validTime(card.closed_at)) return card.closed_at
 const closeOperation = operations
  .filter(operation => operation.card_id === card.id && ['cancel','withdraw_cancel'].includes(operation.action) && operation.status === 'succeeded' && validTime(operation.updated_at))
  .sort((a,b) => Date.parse(b.updated_at) - Date.parse(a.updated_at))[0]
 return closeOperation?.updated_at || card.synced_at || card.created_at || ''
}

export function sortClosedCards(rows, operations = []) {
 return [...rows].sort((a,b) => (Date.parse(closedTime(b,operations))||0) - (Date.parse(closedTime(a,operations))||0) || String(b.card?.id||'').localeCompare(String(a.card?.id||'')))
}
