import { createApp } from 'vue'
import FreePipelineView from '../src/components/FreePipelineView.vue'
import '../src/styles.css'

// All requests stay inside this fixture, including settings saved across reloads.
const defaults = {
  provider: 'sub2',
  sub2: { url: 'https://sub2.example.com', email: 'admin@example.com', password_present: true, relogin_failure_limit: 2, enable_401_check: true, quota_enabled: true },
  cpa: { url: '', key_present: false, relogin_failure_limit: 2, enable_401_check: true, quota_enabled: true },
  vm2api: { url: 'https://vm.example.com', key_present: true, bind_proxy: true, proxy_ids: ['px-a'], kernel: 'ubuntu-24.04', memory: '1g', locale: 'en_US.UTF-8', status_check_interval_seconds: 120, quota_check_interval_seconds: 120 },
}
const state = JSON.parse(sessionStorage.getItem('push-settings-fixture') || 'null') || defaults
window.pushSettingsFixture = { state, writes: [], proxyRequests: [], failProxy: false, delay: 0 }
const json = data => new Response(JSON.stringify({ ok: true, data }), { headers: { 'Content-Type': 'application/json' } })
window.fetch = async (path, options = {}) => {
  const url = new URL(path, location.origin)
  if (url.pathname === '/api/push-settings') {
    if (options.method === 'PUT') {
      const input = JSON.parse(options.body)
      window.pushSettingsFixture.writes.push(input)
      Object.assign(state, input, { sub2: { ...input.sub2, password_present: true } })
      sessionStorage.setItem('push-settings-fixture', JSON.stringify(state))
    }
    return json(state)
  }
  if (url.pathname === '/api/sub2-settings') return json(state.sub2)
  if (url.pathname === '/api/push-settings/vm2api/proxies') {
    const fixture = window.pushSettingsFixture
    fixture.proxyRequests.push(JSON.parse(options.body))
    if (fixture.delay) await new Promise(resolve => setTimeout(resolve, fixture.delay))
    if (fixture.failProxy) return new Response(JSON.stringify({ ok: false, error: '模拟代理列表读取失败' }), { status: 502, headers: { 'Content-Type': 'application/json' } })
    return json([
      { id: 'px-local', label: 'local代理', scheme: 'local', host: 'local', enabled: true, status: 'ok', bound_count: 2, bind_limit: 32 },
      { id: 'px-a', label: '出口 A', scheme: 'socks5', host: '10.0.0.1', port: 1080, enabled: true, status: 'ok', bound_count: 3, bind_limit: 32 },
      { id: 'px-b', label: '出口 B', scheme: 'socks5', host: '10.0.0.2', port: 1080, enabled: true, status: 'ok', bound_count: 1, bind_limit: 32 },
      { id: 'px-full', label: '已满出口', scheme: 'socks5', host: '10.0.0.3', port: 1080, enabled: true, status: 'ok', bound_count: 32, bind_limit: 32 },
      { id: 'px-disabled', label: '停用出口', scheme: 'socks5', host: '10.0.0.4', port: 1080, enabled: false, status: 'dead', bound_count: 0, bind_limit: 32 },
    ])
  }
  if (url.pathname === '/api/sub2-settings/groups') return json([])
  if (url.pathname === '/api/free-accounts') return json({ items: [], total: 0, summary: { all: 0, inside: 0, monitoring: 0 } })
  if (url.pathname === '/api/admin-capacity-snapshots') return json({})
  throw new Error('Unmocked request: ' + url.pathname)
}
createApp(FreePipelineView).mount('#app')
