// Shared across page mounts. Only a timestamp is persisted, never card data.
export function createEJHRefreshGate({ now = Date.now, storage = () => globalThis.sessionStorage } = {}) {
  const key = 'ejh:last-page-refresh:v1'
  let lastAttempt = null, active = null
  function stamp() {
    lastAttempt = now()
    try { storage()?.setItem(key, String(lastAttempt)) } catch {}
  }
  return {
    begin(automatic = false, intervalSeconds = 30) {
      const cooldown = ejhPageRefreshSeconds(intervalSeconds) * 1000
      if (active) return { reason: 'running', done: active.done }
      try {
        const raw = storage()?.getItem(key)
        if (raw != null && Number.isFinite(Number(raw))) lastAttempt = Number(raw)
      } catch {}
      const age = lastAttempt == null ? Infinity : now() - lastAttempt
      if (automatic && age >= 0 && age < cooldown) return { reason: 'cooldown' }
      let resolve
      const done = new Promise(ok => { resolve = ok })
      const lease = { done, finish() {
        if (active !== lease) return
        // Failed or interrupted attempts also cool down; manual retry bypasses it.
        stamp(); active = null; resolve()
      } }
      active = lease; stamp()
      return lease
    },
  }
}
export const ejhRefreshGate = createEJHRefreshGate()

export function ejhPageRefreshSeconds(value) {
  const seconds = Number(value)
  return Number.isInteger(seconds) && seconds >= 5 && seconds <= 86400 ? seconds : 30
}
