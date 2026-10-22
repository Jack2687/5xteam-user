// Run on scripts/mail-management-preview.html; all network calls use fixtures.
(async () => {
  const fixture = window.mailExportFixture
  if (!fixture) throw new Error('Requires isolated mail fixture')
  fixture.oauthStageDelay = 10
  const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
  const assert = (ok, msg) => { if (!ok) throw new Error(msg) }
  const wait = async fn => { for (let n = 0; n < 160; n++) { if (fn()) return; await pause(50) } throw new Error('SIWC UI timed out') }
  const btn = (text, scope = document) => [...scope.querySelectorAll('button')].find(b => b.textContent.trim() === text)
  const menu = async index => {
    document.querySelectorAll('.action-menu>button')[index].dispatchEvent(new MouseEvent('mouseenter'))
    await wait(() => document.querySelector('.action-menu-popover'))
    return document.querySelector('.action-menu-popover')
  }
  let panel = await menu(0)
  const start = btn('获取 SIWC RT / AT', panel)
  assert(start && !start.disabled, 'Single SIWC action missing')
  start.click()
  await wait(() => document.body.textContent.includes('SIWC RT / AT 已安全保存'))
  assert(fixture.requests.filter(r => r.path.endsWith('/siwc')).length === 1, 'Single action sent wrong request')
  document.querySelector('[aria-label="关闭任务详情"]').click()
  panel = await menu(0)
  assert(btn('重新获取 SIWC RT / AT', panel), 'Saved SIWC not reflected in action')
  const view = [...panel.querySelectorAll('button')].find(b => b.textContent.includes('查看、复制'))
  view.click()
  await wait(() => document.querySelector('[aria-label="SIWC AT"]')?.value === 'fixture-siwc-at')
  assert(document.querySelector('[aria-label="SIWC RT"]').value === 'fixture-siwc-rt', 'SIWC RT missing')
  let copied = ''
  navigator.clipboard.writeText = async value => { copied = value }
  btn('复制 SIWC AT').click()
  await wait(() => copied === 'fixture-siwc-at')
  btn('复制 SIWC RT').click()
  await wait(() => copied === 'fixture-siwc-rt')
  const oldCreate = URL.createObjectURL, oldClick = HTMLAnchorElement.prototype.click
  let blob, filename
  try {
    URL.createObjectURL = value => { blob = value; return oldCreate(value) }
    HTMLAnchorElement.prototype.click = function () { filename = this.download }
    assert(!btn('导出 SIWC JSON').disabled, 'SIWC export incorrectly requires ordinary tokens')
    btn('导出 SIWC JSON').click()
    await wait(() => !!filename)
    const out = JSON.parse(await blob.text())
    assert(out.accounts[0].credentials.auth_mode === 'siwc', 'Export mode wrong')
    assert(out.accounts[0].credentials.client_id === 'oaiapp_fixture', 'Export missing dynamic client')
    assert(filename.endsWith('-siwc-sub2api-account.json'), 'Export filename wrong')
  } finally { URL.createObjectURL = oldCreate; HTMLAnchorElement.prototype.click = oldClick }
  document.querySelector('[aria-label="关闭凭证窗口"]').click()
  for (const email of ['fixture-02@example.com', 'fixture-03@example.com']) document.querySelector(`[aria-label="选择 ${email}"]`).click()
  await wait(() => [...document.querySelectorAll('button')].some(b => b.textContent.includes('获取 SIWC RT / AT') && b.textContent.includes('（2）')))
  const bulk = [...document.querySelectorAll('button')].find(b => b.textContent.includes('获取 SIWC RT / AT') && b.textContent.includes('（2）'))
  assert(bulk && !bulk.disabled, 'Bulk SIWC action missing')
  bulk.click()
  await wait(() => fixture.items[1].siwc_present && fixture.items[2].siwc_present)
  assert(fixture.requests.filter(r => r.path.endsWith('/siwc')).length === 3, 'Bulk action did not use SIWC for every selected account')
  assert(!fixture.requests.some(r => r.path.startsWith('/api/mail/siwc/')), 'Polling used a nonexistent route')
  return 'Passed: single and bulk SIWC, progress, saved state, AT/RT view/copy, SIWC-only JSON export'
})()
