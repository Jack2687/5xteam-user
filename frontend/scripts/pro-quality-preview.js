import { createApp, h } from 'vue'
import ProManagementView from '../src/components/ProManagementView.vue'
import '../src/styles.css'

// All network operations are local fixtures. No real detection, group writes or merges.
const now = new Date().toISOString()
const fixture = window.proQualityFixture = {
  requests: [],
  policy: { enabled: false, poll_interval_seconds: 60, max_result_age_seconds: 900, error_failure_limit: 2, question_enabled: true, model_audit_enabled: false, condition_mode: 'any', action: 'merge', normal_group_ids: [], degraded_group_ids: [], auto_restore: true, recovery_limit: 2, concurrency: 4 },
  runtime: { running: false, total: 0, done: 0 },
  accounts: ['normal', 'degraded', 'stale', 'error'].map((status, index) => ({
    email: `quality-${index + 1}@example.com`, management_scope: 'pro', oauth_status: 'completed', push_status: 'completed', push_provider: 'sub2',
    pro_auto: { id: `test-${index}`, status: 'waiting_quota', steps: { push: 'completed', quota: 'waiting' } },
    pro_quality: { status, question_status: status, model_audit: { status: 'normal', response_model: 'gpt-test' }, fetched_at: now, source_question_at: now, action: index === 1 ? 'groups' : '', action_status: index === 1 ? 'completed' : '', routed: index === 1, error: status === 'error' ? 'Sub2 接口暂时不可用，不执行处理' : status === 'stale' ? '检测结果已过期' : '' },
  })),
}
const reply = data => new Response(JSON.stringify({ ok: true, data }), { headers: { 'Content-Type': 'application/json' } })
window.fetch = async (path, options = {}) => {
  const url = new URL(path, location.origin), method = options.method || 'GET'
  const body = options.body ? JSON.parse(options.body) : null
  fixture.requests.push({ path: url.pathname, method, body })
  if (url.pathname === '/api/pro-settings') return reply({ provider: 'sub2', sub2: { url: 'https://pro-sub2.invalid', group_ids: [], models: [] }, cpa: {}, vm2api: {} })
  if (url.pathname === '/api/pro-accounts') return reply({ items: fixture.accounts, total: 4, summary: { all: 4, in_progress: 4 } })
  if (url.pathname === '/api/pro-quality/settings') {
    if (method === 'PUT') fixture.policy = structuredClone(body)
    return reply({ settings: fixture.policy, provider: 'sub2', summary: { total: 4, normal: 1, degraded: 1, stale: 1, error: 1, routed: 1 }, runtime: fixture.runtime })
  }
  if (url.pathname === '/api/pro-settings/sub2/groups') return reply([{ id: 1, name: '正常分组' }, { id: 2, name: '降智分组' }, { id: 9, name: '其他分组' }])
  if (url.pathname === '/api/pro-quality/run') {
    Object.assign(fixture.runtime, { running: true, total: 4, done: 1 })
    setTimeout(() => { Object.assign(fixture.runtime, { running: false, done: 4 }) }, 6500)
    return reply({ started: true })
  }
  throw new Error('Unmocked request: ' + url.pathname)
}
createApp({ render: () => h(ProManagementView) }).mount('#app')
