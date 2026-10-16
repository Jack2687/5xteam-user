// The saved Pro "user" counter, displayed in R without currency conversion.
export function accountConsumption(row) {
  const value = row?.consumption_total
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null
}

export function consumptionText(value) {
  return value === null ? '未查询' : `${value.toFixed(4)} R`
}

export function cardConsumption(rows = []) {
  const accounts = new Map()
  for (const row of rows) {
    const key = row.email?.trim().toLowerCase() || row.id
    const value = accountConsumption(row)
    // One account can have several payment attempts or renewal orders.
    if (!accounts.has(key) || accounts.get(key) === null) accounts.set(key, value)
  }
  let total = 0, known = 0
  for (const value of accounts.values()) {
    if (value !== null) { total += value; known++ }
  }
  const missing = accounts.size - known
  const amount = known || !accounts.size ? total : null
  return { text: consumptionText(amount), total: amount, missing }
}
