// Run on console-style-preview.html only. Navigates existing controls; no writes.
(async () => {
  if (!window.styleFixtureRequests) throw new Error('Requires isolated style fixture')
  const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
  const results = []
  for (const name of ['Pro 管理', 'EJH 卡台', '空间合并', '邮件管理', 'Team 轮转', '母号管理', '接码管理', '执行历史', 'OpenAI 账号', '代理管理', '接口设置']) {
    const button = [...document.querySelectorAll('.desktop-nav button')].find(n => n.textContent.trim() === name)
    if (!button) throw new Error('Missing navigation: ' + name)
    button.click()
    await pause(550)
    const main = document.querySelector('.app-main')
    const heading = [...main.querySelectorAll('h1')].find(n => n.getClientRects().length)
    if (!heading) throw new Error('Missing page: ' + name)
    const bounds = main.getBoundingClientRect()
    const viewport = document.documentElement.clientWidth
    const overflow = [...main.querySelectorAll('section, header, nav, .panel, .config-workspace, .settings-section')].filter(n => {
      if (!n.getClientRects().length || n.closest('.table-shell,.table-wrap,.modal-backdrop,[hidden]')) return false
      const r = n.getBoundingClientRect()
      return r.width > 0 && (r.left < -1 || r.right > viewport + 1)
    }).map(n => n.className)
    results.push({ name, title: heading.textContent, width: Math.round(bounds.width), overflow })
  }
  if (window.styleFixtureRequests.some(r => r.method !== 'GET' && !['/api/sub2-settings/test','/api/ejh/sync','/api/ejh/balance'].includes(r.path) && !(r.path.startsWith('/api/ejh/cards/') && r.path.endsWith('/action')))) throw new Error('Unexpected mutation request')
  return { results, errors: window.styleFixtureErrors, writes: 0 }
})()
