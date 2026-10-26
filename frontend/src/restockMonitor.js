const timestamp = value => value && !String(value).startsWith('0001') ? Date.parse(value) : NaN

// Freshness uses the upstream sample time, never the HTTP response time.
export function restockMonitorStatus(state = {}, now = Date.now()) {
  const snapshot = state.snapshot || {}, config = state.config || {}, monitor = state.monitor || {}
  const at = timestamp(snapshot.at), limit = config.fresh_seconds || 20
  const hasSample = Number.isFinite(at), age = hasSample ? Math.max(0, (now - at) / 1000) : null
  const stale = !hasSample || now - at > limit * 1000 || at > now + 5000
  const running = !!monitor.running
  const error = String(monitor.error || snapshot.error || '').replace(/\s+/g, ' ').trim()
  const unavailable = stale || !snapshot.valid || !snapshot.load_valid || !!error
  const phase = {preparing:'准备采集', collecting:'采集并发', reconciling:'更新任务状态', evaluating:'计算补货'}[monitor.phase] || '更新监控'
  const elapsed = timestamp(monitor.started_at)
  const progress = running ? `${phase}中${Number.isFinite(elapsed) ? `，已用 ${Math.max(0, Math.floor((now - elapsed) / 1000))} 秒` : ''}。` : ''
  let kind = 'ready', title = '监控数据正常', message = ''
  if (!config.group_id) {
    kind = 'unconfigured'; title = '尚未选择监控分组'; message = '请在补货配置中选择监控分组。'
  } else if (error) {
    kind = 'error'; title = '本轮监控未完成'; message = `${error.slice(0, 180)}${running ? `；${progress}` : ''}`
  } else if (!hasSample) {
    kind = running ? 'collecting' : 'empty'; title = running ? '正在首次采集' : '等待首次采集'; message = progress || '后台尚未取得完整的并发数据。'
  } else if (stale) {
    kind = 'stale'; title = '并发快照已过期'; message = at > now + 5000 ? '快照时间超前，请检查服务器时钟。' : `最近数据距今 ${Math.floor(age)} 秒，有效期 ${limit} 秒。${progress || '等待后台更新。'}`
  } else if (!snapshot.valid || !snapshot.load_valid) {
    kind = 'incomplete'; title = '并发数据不完整'; message = progress || '账号列表或负载尚未核验完整，等待重新采集。'
  } else if (running) {
    kind = 'collecting'; title = '监控正在更新'; message = `${progress}当前快照仍在有效期内。`
  }
  const retryAt = timestamp(monitor.next_check_at)
  const retry = !running && Number.isFinite(retryAt) ? Math.max(0, Math.ceil((retryAt - now) / 1000)) : null
  const lastSuccess = timestamp(monitor.last_success_at)
  return {kind, title, message, unavailable, stale, running, age, limit, retry,
    lastSuccess: Number.isFinite(lastSuccess) ? monitor.last_success_at : snapshot.valid && snapshot.load_valid && hasSample ? snapshot.at : null,
    totalSeconds: Math.max(0, Number(monitor.total_ms || 0) / 1000),
  }
}
