export const payProviderName = provider => provider === 'cmsnav' ? 'GPTPay · CMSNav' : 'GPTPay · Tokenseek'
export const proPlanName = code => ({ go: 'Go', plus: 'Plus', pro5: 'Pro 5x', pro20: 'Pro 20x', pro50: 'Pro 50x', pro5x: 'Pro 5x', pro20x: 'Pro 20x', pro50x: 'Pro 50x', pro5_up: 'Plus 升级 Pro 5x', pro20_up: 'Plus 升级 Pro 20x', pro50_up: 'Plus 升级 Pro 50x', codex_point: 'Codex 点数' })[code] || code
export const tokenseekPlans = ['go', 'plus', 'pro5', 'pro20', 'pro50', 'pro5_up', 'pro20_up', 'pro50_up', 'codex_point']
const tokenseekCountries = [
  { code: 'PH', name: '菲律宾' }, { code: 'EG', name: '埃及' },
  { code: 'CL', name: '智利' }, { code: 'US', name: '美国' }, { code: 'JP', name: '日本' },
]
export const paymentCountryLabel = code => { const country = tokenseekCountries.find(c => c.code === code); return country ? `${country.name} · ${code}` : code || '未记录' }
export const planCategory = code => code === 'codex_point' ? 'Codex 点数' : code.endsWith('_up') ? 'Plus 升级' : '普通订阅'
export function tokenseekCatalog(account) {
  return { countries: tokenseekCountries, products: (account.plans || []).map(p => ({ code: p.planCode, creditPrice: p.successCredits, enabled: true, availableCountries: p.availableCountries, defaultCountry: p.defaultCountry || (p.planCode === 'pro50' ? 'CL' : account.defaultCountry || 'PH') })) }
}
export function paymentCountries(provider, catalog, plan) {
  if (provider === 'cmsnav') return catalog?.countries || []
  const allowed = catalog?.products?.find(p => p.code === plan)?.availableCountries
  return tokenseekCountries.filter(c => !allowed || allowed.includes(c.code))
}
export function catalogProPlans(catalog, provider = 'cmsnav') {
  return (provider === 'cmsnav' ? ['pro5', 'pro20', 'pro50'] : tokenseekPlans).map(code => {
    const wireCode = provider === 'cmsnav' ? code + 'x' : code, product = catalog?.products?.find(p => p.code === wireCode)
    const price = catalog?.creditPrices?.[wireCode] ?? product?.creditPrice
    const available = catalog?.productAvailability?.[wireCode] ?? product?.enabled ?? (price != null || (!catalog && provider === 'tokenseek'))
    return { code, price, available: !!available, defaultCountry: product?.defaultCountry || (code === 'pro50' ? 'CL' : 'PH'), category: planCategory(code), label: `${proPlanName(code)}${price != null ? ` · ${price} Credits` : ''}${catalog && !available ? '（暂不可用）' : ''}` }
  })
}
export const payStatusName = status => ({ submitting: '正在提交', created: '已创建', processing: '开通中', success: '成功', failed: '失败', submission_unknown: '提交待确认', manual_review: '待人工核查', not_found: '未找到', not_applicable: '不适用', reserved: '已冻结', settled: '已结算', waiting: '等待取消续费', pending: '取消续费中' })[status] || status || '—'
export const needsPayPoll = order => !!order?.remote?.id && (!['success', 'failed'].includes(order.status) || ['waiting', 'pending'].includes(order.remote.cancellationStatus))
export const payRequestID = () => Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, '0')).join('')
export function parseCardText(raw) {
  const parts = raw.trim().split('----').map(s => s.trim())
  if (parts.length !== 3) throw new Error('格式应为：银行卡号----年月----CVV')
  const number = parts[0].replace(/[ -]/g, ''), cvv = parts[2]
  const date = parts[1], split = date.split(/[-/\s]+/)
  let year, month
  if (split.length === 2 && split[0].length === 4) [year, month] = split
  else if (split.length === 2) [month, year] = split
  else if (/^\d{6}$/.test(date)) [year, month] = [date.slice(0, 4), date.slice(4)]
  else if (/^\d{4}$/.test(date)) [year, month] = [date.slice(0, 2), date.slice(2)]
  if (!/^\d{12,19}$/.test(number) || !/^\d{3,4}$/.test(cvv)) throw new Error('请检查卡号和 CVV')
  if (!/^\d+$/.test(year || '') || !/^\d+$/.test(month || '')) throw new Error('年月使用 YYYYMM 或 YYYY-MM，也支持 MM/YY')
  const exp_year = Number(year) + (year.length === 2 ? 2000 : 0), exp_month = Number(month)
  if (exp_month < 1 || exp_month > 12) throw new Error('月份应为 1～12')
  return { number, cvv, exp_year, exp_month }
}
