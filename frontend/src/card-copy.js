export function cardCopyText(secret, includeHolder = false) {
  const number = String(secret?.cardNumber ?? '').replace(/[\s-]/g, '')
  const month = Number(secret?.expMonth)
  const year = Number(secret?.expYear)
  const cvv = String(secret?.cvv ?? '').trim()
  if (!/^\d{12,19}$/.test(number) || !/^\d{3,4}$/.test(cvv) ||
      !Number.isInteger(month) || month < 1 || month > 12 ||
      !Number.isInteger(year) || year < 2000 || year > 2099) {
    throw new Error('卡密不完整，请先刷新该卡的完整信息')
  }
  const expiry = `${String(month).padStart(2, '0')}/${String(year).slice(-2)}`
  const line = `${number}----${expiry}----${cvv}`
  if (!includeHolder) return line
  const holder = secret?.holder || {}
  const value = key => String(holder[key] ?? '').replace(/\s+/g, ' ').trim() || '未提供'
  return [line, `卡号: ${number}`, `CVV: ${cvv}`, `有效期: ${expiry}`,
    `FirstName: ${value('first_name')}`, `LastName: ${value('last_name')}`,
    `国家: ${value('country')}`, `洲: ${value('state')}`, `城市: ${value('city')}`,
    `街道: ${value('street')}`, `详细地址: ${value('address')}`, `邮编: ${value('zip_code')}`].join('\n')
}
