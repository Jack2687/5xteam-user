// Visual QA of the real App; never forwards requests to a business server.
const requests = window.styleFixtureRequests = []
window.styleFixtureErrors = []
window.addEventListener('error', e => window.styleFixtureErrors.push(e.message))
window.addEventListener('unhandledrejection', e => window.styleFixtureErrors.push(String(e.reason)))
localStorage.setItem('space-console-tab', new URLSearchParams(location.search).get('tab') || 'pro')
localStorage.setItem('space-console-theme-v2', new URLSearchParams(location.search).get('theme') || 'light')
const now = new Date().toISOString()
const costSamples = [
 {push_provider:'sub2',sub2_account_id:42,cost_checked_at:now,cost_downstream_snapshot:12.3456,user_cost_downstream_identity:'42',user_cost_downstream_snapshot:3.25},
 {push_provider:'sub2',sub2_account_id:43,cost_checked_at:now,cost_downstream_snapshot:0,user_cost_downstream_identity:'43',user_cost_downstream_snapshot:0},
 {push_provider:'sub2',sub2_account_id:44,cost_checked_at:now,cost_downstream_snapshot:4.5},
 {push_provider:'cpa'},
 {},
]
const profiles = ['atlas', 'olive', 'river', 'pine', 'clover'].map((name, i) => ({
  ...costSamples[i],
  id: 'demo-' + i, email: name + '@example.com', management_scope: 'pro', login_method: 'directurl',
  current_plan_type: i === 4 ? 'free' : 'pro', access_token_present: true, refresh_token_present: i !== 4,
  gpt_password_present: i !== 4, totp_secret_present: i !== 4, session_present: true, pickup_url_present: true,
  created_at: now, pro_managed_at: now, push_status: i < 3 ? 'completed' : 'pending',
  pro_stage_progress: { steps: { login: 'completed', recharge: i === 4 ? 'pending' : 'completed', oauth: i === 4 ? 'pending' : 'completed', push: i < 3 ? 'completed' : 'pending' } },
  pro_remove_status: i === 2 ? 'completed' : 'pending', pro_transfer_status: i === 2 ? 'completed' : 'pending',
  pro_invite_status: i < 3 ? 'completed' : 'pending', pro_accept_status: i < 3 ? 'completed' : 'pending',
}))
const pro = { provider: 'sub2', quota_enabled: false, quota_used_threshold: 100, quota_check_interval_seconds: 120, scheduled_enabled: false, max_unmerged: 5, schedule_interval_seconds: 120, concurrency: 2, retry_count: 2, retry_interval_seconds: 3, target_admin_id: 'owner-1', target_seat_type: 'default', sub2: { url: 'https://sub2.example.com', email: 'admin@example.com', password_present: true, group_ids: [], models: [], account_concurrency: 10 }, cpa: { url: '', group_ids: [] }, vm2api: { url: '', group_ids: [] } }
const pay = { provider: 'tokenseek', url: 'https://gptpay.tokenseek.app/api/v1', plan_code: 'pro5', country: 'US', key_present: true, card_account_limit: 3, session_timeout_minutes: 30 }
const template = { id:'demo-template', name:'标准 USD 卡', cardType:'demo', cardAmount:'50', cardCurrency:'USD', billCurrency:'USD', valid_days:30, useTime:1, companyRate:'0.01', supportedMccGroup:'ecom' }
const ejh = { enabled:true, url:'https://www.ejhcard.com', app_key:'DEMO_ONLY', secret_present:true, default_max_accounts:3, single_active_account:true, sync_enabled:false, sync_interval_seconds:300, alert_hours:24, templates:[template] }
const cards = ['0615','2089','5017'].map((last4,i)=>({id:'card-'+i,issuer_linked:true,local:{id:'card-'+i,type:'ejh',name:['工作卡 · A','工作卡 · B','备用卡'][i],last4,enabled:true,available:i!==0,opened_accounts:i===0?2:1,pending_accounts:0,remaining_accounts:i===0?1:2,account_limit:3,active_accounts:i===0?1:0,active_email:i===0?'atlas@example.com':'',single_active_account:true},remote:{orderNo:'DEMO-'+i,cardNo:'•••• •••• •••• '+last4,cardCurrency:'USD',validityDate:'1228',status:'1'},balance:String(32+i*9)+'.00',balance_at:now}))
const cases = ['opened','waiting','returned'].map((state,i)=>({id:'case-'+i,email:profiles[i].email,card_id:'card-'+i,payment_order_id:'order-'+i,payment_status:'success',state,payment_amount:'20.00',payment_currency:'USD',requested_at:now,confirmed_amount:i===2?'19.32':'',confirmed_currency:'USD'}))
const dashboard = {cards,cases,trades:[],operations:[],audit:[],balance:{at:now,data:{abroadList:[{currency:'USD',balance:'128.50'}]}},sync:{last_success:now}}
const admins = [{id:'owner-1',label:'Design Team',email:'owner@example.com',team_account_id:'demo-team',access_token_present:true,enabled:true,member_count:3,member_limit:5}]
const settings = {default_page_size:10,base_url:'https://chatgpt.com',role:'standard-user',request_timeout_seconds:60,network_retry_count:2,network_retry_interval_seconds:3,invite_delay_seconds:2,accept_delay_seconds:2,transfer_delay_seconds:2,account_interval_seconds:2,proxy_url:'',auto_cleanup:false}
const paged = (items, summary={}) => ({items,total:items.length,page:1,page_size:10,summary})
const routes = {
 '/api/pro-schedule':{enabled:false,active:false,maximum:5}, '/api/pro-schedule/runs':paged([]), '/api/gptpay/card-cases':cases,
 '/api/gptpay/account':{user:{email:'supplier@example.com'},wallet:{availableCredits:1000,reservedCredits:0},level:{name:'默认'},plans:[{planCode:'pro5',successCredits:100,failureCredits:5,availableCountries:['US'],defaultCountry:'US'}],apiKey:{}},
 '/api/auth/me':{}, '/api/settings':settings, '/api/admin-accounts':admins, '/api/proxies':[{id:'proxy-1',name:'默认代理',url:'http://proxy.example.com:10000'}],
 '/api/pro-settings':pro, '/api/ejh/settings':ejh, '/api/ejh/dashboard':dashboard, '/api/gptpay/settings':pay,
 '/api/gptpay/catalog':{countries:[{code:'US',name:'美国',currency:'USD'}],creditPrices:{pro5x:80},productAvailability:{pro5x:true}},
 '/api/mail/status':{configured:true,available:true}, '/api/mail/accounts':paged(profiles.map(p=>({...p,management_scope:'mail'}))),
 '/api/gptpay/cards':paged(cards.map(c=>c.local)), '/api/gptpay/orders':paged([]),
 '/api/pro-accounts/schedule-runs':paged([]), '/api/history':paged([]), '/api/openai-accounts':paged([]),
 '/api/sms/providers':{providers:[]}, '/api/sms/phones':{...paged([]),stats:{}}, '/api/sms/platform/config':{}, '/api/sms/platform/history':paged([]),
 '/api/free-accounts':paged([]), '/api/quality/settings':{provider:'sub2',settings:{provider:'sub2',enabled:false,poll_interval_seconds:60,max_result_age_seconds:900,action:'groups',concurrency:2,history_limit:100,normal_group_ids:[],degraded_group_ids:[],condition_mode:'any'},summary:{total:24,normal:20,degraded:4}}, '/api/push-settings':pro, '/api/sub2-settings':pro.sub2,
 '/api/admin-capacity-snapshots':[], '/api/auto-rotation/settings':{enabled:false,threshold_percent:50,interval_seconds:300,team_operation_interval_seconds:10,concurrency:2,max_per_run:10,retry_count:1,remove_method:'child_leave',switch_before_remove:false,join_method:'mother_invite',oauth_login_mode:'email_otp'}, '/api/auto-rotation/events':paged([]), '/api/auto-rotation/runs':paged([]),
 '/api/pro-settings/sub2/groups':[], '/api/pro-settings/cpa/groups':[], '/api/pro-settings/vm2api/pools':[],
}
window.fetch = async (path, options={}) => {
 const url = new URL(path, location.origin), method = options.method || 'GET'
 requests.push({path:url.pathname,method})
 if(url.origin===location.origin && method==='POST' && (['/api/ejh/sync','/api/ejh/balance'].includes(url.pathname) || url.pathname.startsWith('/api/ejh/cards/') && url.pathname.endsWith('/action') && JSON.parse(options.body || '{}').action==='balance')) return new Response(JSON.stringify({ok:true,data:{}}),{headers:{'Content-Type':'application/json'}})
 if (url.pathname === '/api/auth/me' && new URLSearchParams(location.search).has('login')) return new Response(JSON.stringify({ok:false,error:'Not authenticated'}),{status:401,headers:{'Content-Type':'application/json'}})
 if (url.origin === location.origin && url.pathname === '/api/sub2-settings/test' && method === 'POST') return new Response(JSON.stringify({ok:true,data:{groups:[]}}),{headers:{'Content-Type':'application/json'}})
 if (method !== 'GET' || url.origin !== location.origin || !url.pathname.startsWith('/api/')) throw new Error('Style fixture blocked write/external request: '+method+' '+url.pathname)
 let data = routes[url.pathname]
 if(url.pathname==='/api/proxies' && url.searchParams.has('page')) data=paged(routes['/api/proxies'])
 if(url.pathname==='/api/pro-accounts') data=url.searchParams.has('page')?paged(profiles,{all:5,oauth_ready:4,pushed:3,merged:1,unmerged:4,in_progress:0}):profiles
 if(data===undefined) { window.styleFixtureErrors.push('Missing mock: '+url.pathname); return new Response(JSON.stringify({ok:false,error:'Missing style mock'}),{status:404}) }
 return new Response(JSON.stringify({ok:true,data}),{headers:{'Content-Type':'application/json'}})
}
await import('../src/main.js')
