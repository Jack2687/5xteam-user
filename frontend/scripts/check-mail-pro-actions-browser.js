// Run via agent-browser eval on a fresh isolated mail-management-preview.html.
(async () => {
  const fixture = window.mailExportFixture
  if (!fixture) throw new Error('Requires isolated mail fixture')
  const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
  const assert = (ok, message) => { if (!ok) throw new Error(message) }
  const wait = async fn => { for (let i=0;i<200;i++) { if(fn()) return; await pause(50) } throw new Error('Mailbox actions UI timeout') }
  const dialog = () => document.querySelector('.mail-actions-dialog')
  const click = async (name, root=document) => {
    const el = [...root.querySelectorAll('button')].find(el=>el.textContent.trim()===name || el.getAttribute('aria-label')===name)
    assert(el && !el.disabled, `Missing enabled button: ${name}`)
    el.click(); await pause(70)
  }
  const row = index => document.querySelectorAll('tbody tr')[index]
  const finalPosts = () => fixture.requests.filter(r=>r.path.endsWith('/final-step'))
  const oaiPosts = () => fixture.requests.filter(r=>r.path.endsWith('/oai-confirmation'))
  assert(finalPosts().length===0 && oaiPosts().length===0, 'List load started a workflow')
  fixture.finalDelay=3000
  await click('执行收尾',row(0))
  await wait(()=>dialog()?.textContent.includes('后台执行中'))
  assert(finalPosts().length===1 && finalPosts()[0].body.mode==='merge_v2','Wrong configured final mode')
  assert(dialog().querySelector('[data-stage="login"]').textContent.includes('执行中'),'Live final stage missing')
  await click('关闭邮件账号流程')
  await wait(()=>!dialog())
  await click('收尾进度',row(0))
  assert(finalPosts().length===1,'Opening progress replayed final step')
  await wait(()=>dialog()?.textContent.includes('收尾已完成'))
  await click('查看流程日志',dialog())
  await wait(()=>document.querySelector('.pro-log-dialog'))
  const log=document.querySelector('.pro-log-dialog')
  assert(log.getAttribute('aria-label')==='邮件账号执行日志','Logs displayed in Pro scope')
  const r=log.getBoundingClientRect()
  assert(log.contains(document.elementFromPoint(r.x+r.width/2,r.y+r.height/2)),'Logs hidden behind flow dialog')
  await click('关闭日志',log)
  await click('关闭邮件账号流程')
  await click('执行收尾',row(0))
  assert(finalPosts().length===1,'Completed final step replayed')
  await click('关闭邮件账号流程')

  fixture.oaiDelay=10000
  await click('OAI 确认',row(1))
  await wait(()=>dialog()?.textContent.includes('停止确认'))
  assert(oaiPosts().length===1 && !fixture.items[1].pro_merge_v2,'Standalone OAI started a final workflow')
  await click('停止确认',dialog())
  await wait(()=>dialog()?.textContent.includes('重试确认'))
  fixture.oaiDelay=100
  await click('重新执行',dialog())
  await wait(()=>dialog()?.textContent.includes('查看回答'))
  const reply=dialog().querySelector('.oai-reply-anchor')
  assert(reply,'OAI answer control missing');reply.click()
  await wait(()=>document.querySelector('.oai-reply-popover'))
  assert(document.querySelector('.oai-reply-popover').textContent.includes('预计 5 至 10 个工作日入账。'),'Full answer missing')
  assert(fixture.items[1].pro_oai_confirmations.merge_v2.requested_at && !fixture.items[1].pro_oai_confirmations.merge_v2.completed_at,'Standalone query claimed final completion')
  assert(fixture.items.every(item=>item.management_scope==='mail'),'Mailbox moved into Pro')
  assert(!fixture.requests.some(r=>r.path.startsWith('/api/pro-accounts')||/management-scope|gptpay|auto-pro/.test(r.path)),'Unexpected Pro/provisioning/payment request')
  return 'Passed: configured final step, live progress, reopen/replay prevention, separate mail logs, standalone OAI, stop/restart, full answer, mail ownership, no Pro/provisioning/payment requests'
})()
