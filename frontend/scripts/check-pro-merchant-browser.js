(async()=>{
 const f=window.merchantFixture
 if(!f)throw Error('Isolated merchant fixture required')
 const pause=ms=>new Promise(r=>setTimeout(r,ms))
 const assert=(ok,message)=>{if(!ok)throw Error(message)}
 const button=text=>[...document.querySelectorAll('button')].find(b=>b.textContent.trim()===text)
 const wait=async fn=>{for(let i=0;i<150;i++){if(fn())return;await pause(40)}throw Error('UI wait timed out')}
 const toggle=text=>[...document.querySelectorAll('label.toggle-row')].find(l=>l.querySelector('strong')?.textContent===text)?.querySelector('input')
 ;[...document.querySelectorAll('nav button')].find(b=>b.textContent.includes('空间合并/删除账户/API 商户')).click()
 await wait(()=>toggle('查额度达标后 API 商户'))
 const merge=toggle('查额度达标后自动合并'),deletion=toggle('查额度达标后自动删除'),merchant=toggle('查额度达标后 API 商户')
 assert(merchant.checked&&!merge.checked&&!deletion.checked,'saved mode missing')
 merge.click();await pause(40);assert(merge.checked&&!deletion.checked&&!merchant.checked,'merge not exclusive')
 deletion.click();await pause(40);assert(deletion.checked&&!merge.checked&&!merchant.checked,'delete not exclusive')
 merchant.click();await pause(40);assert(merchant.checked&&!merge.checked&&!deletion.checked,'merchant not exclusive')
 const key=[...document.querySelectorAll('label.field')].find(l=>l.textContent.includes('API 商户密钥')).querySelector('input')
 const setter=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set
 setter.call(key,'fixture-only-key');key.dispatchEvent(new Event('input',{bubbles:true}))
 await wait(()=>!button('保存 Pro 配置').disabled)
 button('保存 Pro 配置').click()
 await wait(()=>f.requests.some(r=>r.path==='/api/pro-settings/automation'))
 await wait(()=>key.value==='')
 const saved=f.requests.find(r=>r.path==='/api/pro-settings/automation').body
 assert(saved.merchant_api_key==='fixture-only-key'&&saved.automation.auto_merchant_enabled&&!saved.automation.auto_delete_enabled&&!saved.automation.auto_merge_enabled,'wrong saved payload')
 button('测试商户列表').click()
 await wait(()=>document.querySelector('.pro-table')?.textContent.includes('商户核验中'))
 assert(document.querySelector('thead').textContent.includes('API 商户状态')&&!document.querySelector('thead').textContent.includes('四步状态'),'wrong status column')
 const row=[...document.querySelectorAll('tbody tr')].find(r=>r.textContent.includes('pending@example.com'))
 assert(row.textContent.includes('下次查询')&&row.textContent.includes('申请编号')&&row.querySelector('[data-stage="merchant"]')?.textContent.includes('API 商户'),'pending progress missing')
 assert(!row.querySelector('[data-stage="merchant"] select'),'merchant success must reflect upstream evidence')
 const tab=[...document.querySelectorAll('.space-filter-tabs button')].find(b=>b.textContent.includes('API 商户'))
 tab.click();await wait(()=>f.requests.some(r=>r.path==='/api/pro-accounts'&&r.query.includes('merchant')))
 await pause(80)
 assert(document.querySelector('tbody').textContent.includes('done@example.com')&&!document.querySelector('tbody').textContent.includes('pending@example.com'),'merchant filter not respected')
 assert(!document.querySelector('tbody').textContent.includes('下次查询'),'completed account still schedules progress')
 return {passed:true,checks:['three exclusive toggles','encrypted-key form payload and clearing','merchant final automation stage','separate progress column','final-success-only filter','no timer after completion']}
})()
