// Run only on the isolated mail-management-preview.html fixture.
(async () => {
  const fixture = window.mailExportFixture
  if (!fixture) throw new Error('Requires isolated mail fixture')
  const assert = (ok, message) => { if (!ok) throw new Error(message) }
  const wait = async fn => { for (let n = 0; n < 150; n++) { if (fn()) return; await new Promise(resolve => setTimeout(resolve, 20)) }; throw new Error('UI condition timed out') }
  const button = name => [...document.querySelectorAll('button')].find(el => el.textContent.trim() === name || el.textContent.trim().startsWith(name + '（'))
  const click = async name => { await wait(() => button(name) && !button(name).disabled); const el = button(name); el.click(); await new Promise(resolve => setTimeout(resolve, 30)) }
  const checkbox = () => document.querySelector('.mail-import-destination input')
  const input = (el, value) => { el.value = value; el.dispatchEvent(new Event('input', { bubbles:true })) }
  assert(button('批量进入 Pro 管理').disabled, 'Empty selection must disable batch move')
  await click('导入账号')
  assert(checkbox() && !checkbox().checked, 'Direct Pro checkbox defaults off')
  checkbox().click()
  input(document.querySelector('.mail-import-modal textarea'), Array.from({length:26}, (_, i) => 'direct-' + i + '@example.com----https://mail.example/pickup').join('\n'))
  await click('确认导入')
  await wait(() => document.querySelector('.mail-import-progress')?.textContent.includes('导入完成'))
  const imports = fixture.requests.filter(r => r.path === '/api/mail/accounts/import')
  assert(imports.length === 2 && imports.every(r => r.body.enter_pro === true), 'All import chunks must keep selected Pro destination')
  assert(fixture.items.filter(a => a.email.startsWith('direct-') && a.management_scope === 'pro').length === 26, 'All imported accounts go directly to Pro')
  assert(!document.querySelector('tbody').textContent.includes('direct-'), 'Pro imports must not appear in mail list')
  assert(!fixture.requests.some(r => /management-scope$/.test(r.path)), 'Direct import must not separately move every account')
  await click('关闭')
  await click('导入账号')
  assert(!checkbox().checked, 'Reopening resets destination to mail')
  input(document.querySelector('.mail-import-modal textarea'), 'ordinary@example.com----https://mail.example/pickup')
  await click('确认导入')
  await wait(() => document.querySelector('.mail-import-progress')?.textContent.includes('导入完成'))
  assert(fixture.items.find(a => a.email === 'ordinary@example.com').management_scope === 'mail', 'Unchecked import remains mail')
  await click('关闭')
  const first = document.querySelector('tbody input[type=checkbox]')
  const firstEmail = first.getAttribute('aria-label').replace('选择 ', '')
  first.click()
  document.querySelector('.pagination button:last-child').click()
  await wait(() => !document.querySelector('tbody input[type=checkbox]')?.getAttribute('aria-label').includes(firstEmail))
  const second = document.querySelector('tbody input[type=checkbox]')
  const secondEmail = second.getAttribute('aria-label').replace('选择 ', '')
  second.click()
  const previousConfirm = window.confirm
  try {
    window.confirm = () => false
    await click('批量进入 Pro 管理')
    assert(!fixture.requests.some(r => /management-scope$/.test(r.path)), 'Cancelled batch must not request a move')
    window.confirm = () => true
    fixture.moveFailEmails = [secondEmail]
    await click('批量进入 Pro 管理')
    await wait(() => document.body.textContent.includes('成功 1，失败 1'))
    assert(fixture.items.find(a => a.email === firstEmail).management_scope === 'pro', 'Cross-page selected first item moves')
    assert(fixture.items.find(a => a.email === secondEmail).management_scope === 'mail', 'Failed item remains mail')
    assert(button('批量进入 Pro 管理').textContent.includes('1'), 'Failed selection retained; succeeded selection removed')
    fixture.moveFailEmails = []
    await click('批量进入 Pro 管理')
    await wait(() => document.body.textContent.includes('成功 1，失败 0'))
    assert(button('批量进入 Pro 管理').disabled, 'Successful retry clears remaining selection')
    const moves = fixture.requests.filter(r => /management-scope$/.test(r.path))
    assert(moves.length === 3 && moves.every(r => r.body.scope === 'pro'), 'Reuse the single-account endpoint only for selected accounts')
    assert(!fixture.requests.some(r => /\/(team|recharge|merge|auto)$/.test(r.path)), 'No unrelated task started')
  } finally { window.confirm = previousConfirm }
  return 'PASS: unchecked default; 26 direct Pro imports across 2 chunks; normal mail import; cross-page selection; cancellation; partial failure; selected-only retry; no task API calls'
})()
