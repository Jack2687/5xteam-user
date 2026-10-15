const finite = value => typeof value === "number" && Number.isFinite(value)
export const windowLabel = seconds => seconds % 86400 === 0 ? seconds / 86400 + " 天窗口" : seconds % 3600 === 0 ? seconds / 3600 + " 小时窗口" : seconds / 60 + " 分钟窗口"
export const rateLabel = value => finite(value) ? value.toLocaleString("zh-CN", {maximumFractionDigits: 4}) + " 百分点/分钟" : "样本不足"
export function supplyLabel(decision, unavailable = false) {
  if (unavailable) return "数据已失效"
  if (!finite(decision?.supply_minutes)) return "暂不可估"
  return (decision.supply_capped ? "≥ " : "") + decision.supply_minutes.toFixed(1) + " 分钟"
}
export function forecastDetails(decision = {}, now = Date.now()) {
  const snapshot = decision.snapshot || {}, config = decision.config || {}
  const at = Date.parse(snapshot.at || "")
  const stale = !snapshot.valid || !snapshot.load_valid || !Number.isFinite(at) || now - at > (config.fresh_seconds || 20) * 1000 || at > now + 5000
  const rows = [], groups = new Map(), windowSets = new Set()
  let ready = 0, covered = 0
  for (const account of snapshot.accounts || []) {
    const windows = account.windows || [], seen = new Set()
    if (account.ready) ready++
    const reasons = windows.map(w => {
      const sampleAt = Date.parse(w.at || "")
      const duplicate = seen.has(w.seconds); seen.add(w.seconds)
      if (stale) return "监控快照失效"
      if (!account.ready) return "账号不可调度"
      if (!account.plan || account.plan !== snapshot.new_plan) return "套餐口径不匹配"
      if (duplicate || !finite(w.seconds) || w.seconds <= 0 || !finite(w.used) || w.used < 0 || w.used > 100 || !finite(w.rate) || w.rate < 0) return "额度数据无效"
      if (!Number.isFinite(sampleAt) || sampleAt > now + 5000 || now - sampleAt > (config.quota_seconds || 60) * 2000 || !finite(w.reset_at) || w.reset_at * 1000 <= now) return "额度样本或窗口已过期"
      if (!finite(w.samples) || w.samples < 2) return "有效变化样本不足 2 次"
      return ""
    })
    const eligible = windows.length > 0 && reasons.every(reason => !reason)
    if (eligible) { covered++; windowSets.add(windows.map(w=>w.seconds).sort((a,b)=>a-b).join(',')) }
    for (let i = 0; i < windows.length; i++) {
      const w = windows[i], reason = reasons[i] || (eligible ? "" : "同账号其他窗口不可信")
      rows.push({ ...w, account_id: account.id, name: account.name || "#" + account.id, plan: account.plan, expires_at: account.expires_at, reason, trusted: !reason })
      if (reason) continue
      const key = account.plan + ":" + w.seconds
      const group = groups.get(key) || {key, plan:account.plan, seconds:w.seconds, accounts:0, rate:0, observed:0, observedAccounts:0, remaining:0}
      group.accounts++; group.rate += w.rate
      group.remaining += Math.max(0, 100 - w.used - w.rate * Math.max(0, (now - Date.parse(w.at)) / 60000))
      if (finite(w.observed_rate) && w.observed_rate >= 0) { group.observed += w.observed_rate; group.observedAccounts++ }
      groups.set(key, group)
    }
    if (!windows.length) rows.push({account_id:account.id,name:account.name || "#" + account.id,plan:account.plan,reason:"尚无额度样本",trusted:false})
  }
  const cohorts = [...groups.values()].sort((a,b)=>a.seconds-b.seconds).map(g=>({...g,observed:g.observedAccounts === g.accounts ? g.observed : null}))
  const coverage = covered * 100 / Math.max(1,ready)
  const partial = coverage < 100 || windowSets.size > 1
  const compatible = windowSets.size === 1 && !(snapshot.accounts || []).some(a=>a.ready && a.plan !== snapshot.new_plan)
  const predictionUsable = compatible && coverage >= (config.quota_coverage || 80) && cohorts.some(g=>g.rate>0)
  return {stale, rows, cohorts, coverage, partial, primary:!partial ? cohorts.find(g=>g.seconds===18000) || cohorts[0] : null, supply:supplyLabel(predictionUsable ? decision : {},stale)}
}
