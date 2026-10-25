export function cardCopyText(secret) {
  const number = String(secret?.cardNumber ?? '').replace(/[\s-]/g, '')
  const month = Number(secret?.expMonth)
  const year = Number(secret?.expYear)
  const cvv = String(secret?.cvv ?? '').trim()
  if (!/^\d{12,19}$/.test(number) || !/^\d{3,4}$/.test(cvv) ||
      !Number.isInteger(month) || month < 1 || month > 12 ||
      !Number.isInteger(year) || year < 2000 || year > 2099) {
    throw new Error('卡密不完整，请先刷新该卡的完整信息')
  }
  return `${number}----${String(month).padStart(2, '0')}/${String(year).slice(-2)}----${cvv}`
}
