<script setup>
import {computed} from 'vue'
import {proPlanName} from '../gptpay'

const props=defineProps({timetable:Object,config:Object,demand:Object,purchase:Object,decision:Object,now:Number})
const periods=computed(()=>props.timetable?.periods||[])
const minute=computed(()=>{const d=new Date(props.now);return (d.getUTCHours()*60+d.getUTCMinutes()+480)%1440})
const currentIndex=computed(()=>periods.value.findIndex(p=>p.start_minute>p.end_minute?minute.value>=p.start_minute||minute.value<p.end_minute:minute.value>=p.start_minute&&minute.value<p.end_minute))
const current=computed(()=>periods.value[currentIndex.value])
const next=computed(()=>periods.value.length>1&&currentIndex.value>=0?periods.value[(currentIndex.value+1)%periods.value.length]:null)
const time=n=>`${String(Math.floor(n/60)).padStart(2,'0')}:${String(n%60).padStart(2,'0')}`
const beijingTime=computed(()=>time(minute.value))
const range=p=>p.start_minute===0&&p.end_minute===1440?'全天':`${time(p.start_minute)}～${p.start_minute>p.end_minute?'次日':''}${time(p.end_minute)}`
const rule=p=>p.blocked?(p.high_demand?'禁补 · 高并发可放行':'完全禁补'):`${({ejh:'EJH',nexovcc:'环球'})[p.card_provider]||'卡台未配置'} · ${proPlanName(p.plan_code)?.replace(/^Pro /,'')||'套餐未配置'}`
const source=p=>p===current.value&&liveDemand.value?.allowed?'高并发放行':({purchase:'分时开通',default:'默认设置',allowed_provider:'禁补时段允许',blocked:'暂停新增'})[p.source]||''
const liveDemand=computed(()=>{
 const g=props.demand
 if(!g?.active||!current.value?.high_demand)return null
 const valid=g.valid&&g.version===props.config?.version&&Date.parse(g.valid_until)>props.now&&g.windows?.every(w=>w.start>w.end?beijingTime.value>=w.start||beijingTime.value<w.end:beijingTime.value>=w.start&&beijingTime.value<w.end)
 return {...g,valid,allowed:valid&&g.allowed,phase:valid?g.phase:'unknown',reason:valid?g.reason:'并发数据待确认，等待后台检测'}
})
const currentBlocked=computed(()=>!!current.value?.blocked&&!liveDemand.value?.allowed)
const currentTitle=computed(()=>!current.value?'时段规则读取中':!liveDemand.value?rule(current.value):liveDemand.value.allowed?'禁补期间 · 高并发允许补货':liveDemand.value.phase==='unknown'?'禁补中 · 数据待确认':liveDemand.value.phase==='blocked'?'完全禁补 · 重叠时段限制':'禁补中 · 等待并发达标')
const detectedAt=computed(()=>{const at=liveDemand.value?.checked_at;return at&&!at.startsWith('0001')?new Date(at).toLocaleTimeString('zh-CN',{timeZone:'Asia/Shanghai',hour12:false}):'等待首次检测'})
const hint=computed(()=>!props.config?.enabled?'新增已暂停；以下为已保存的每日安排。':props.config?.mode!=='auto'?'观察模式，仅计算建议，不自动开通。':currentBlocked.value?'当前暂停新增；已开始的流程继续。':'满足补货条件才开通；已开始的流程继续。')
</script>

<template>
 <section class="purchase-timetable" :class="{blocked:currentBlocked}" aria-label="每日补货时段">
  <div class="timetable-head">
   <div class="current-rule" role="status">
    <span class="current-label">当前规则</span>
    <strong v-if="current">{{currentTitle}}</strong><strong v-else>时段规则读取中</strong>
    <span v-if="current" class="current-range">{{range(current)}} · {{source(current)}}</span>
   </div>
   <span class="beijing-time">北京时间 {{beijingTime}}</span>
  </div>
  <div class="timetable-info"><span>{{hint}}</span><span v-if="next">{{time(current.end_minute%1440)}} 切换：{{rule(next)}}</span></div>
  <div v-if="liveDemand" class="demand-summary" role="status" aria-live="polite">
   <p><b>总在线并发 {{liveDemand.valid?liveDemand.total_used:'—'}}</b><span>监控账号 {{liveDemand.valid?liveDemand.monitor_used:'—'}} ＋ 已勾选兜底账号 {{liveDemand.valid?liveDemand.fallback_used:'—'}}（已去重）</span></p>
   <p v-for="w in liveDemand.windows" :key="w.key"><span>{{w.start}}～{{w.end}} 放行条件：≥ {{w.threshold}} 并发</span><b>连续 {{liveDemand.valid?w.consecutive:0}} / {{w.confirmations}} 次</b></p>
   <p><span>每 {{config?.check_seconds||5}} 秒检查 · 最近检测 {{detectedAt}}</span><span>{{liveDemand.reason}}</span></p>
   <p v-if="liveDemand.allowed&&purchase"><span>开通方案：{{rule(purchase)}}<template v-if="purchase.template_id"> · 模板 {{purchase.template_id}}</template></span><span>{{!config?.enabled?'新增已手动暂停':config?.mode!=='auto'?'观察模式，仅计算建议':purchase.error||decision?.reason||'等待正常补货判断'}}</span></p>
  </div>
  <p v-if="current?.error" class="timetable-error" role="alert">{{current.error}}</p>
  <ol v-if="periods.length" class="timetable-periods">
   <li v-for="(period,index) in periods" :key="period.start_minute" :class="{active:index===currentIndex,paused:period.blocked&&!(index===currentIndex&&liveDemand?.allowed)}" :aria-current="index===currentIndex?'time':undefined">
    <div class="period-time"><span>{{range(period)}}</span><span v-if="index===currentIndex" class="current-badge">当前</span></div>
    <strong>{{index===currentIndex&&liveDemand?currentTitle:rule(period)}}</strong><small>{{period.error?'配置待完善':source(period)}}</small>
   </li>
  </ol>
 </section>
</template>

<style scoped>
.demand-summary{margin-top:12px;padding-top:8px;border-top:1px solid var(--border,#dce7e9);font-size:12px}.demand-summary p{display:flex;flex-wrap:wrap;gap:8px 18px;margin:6px 0;line-height:1.6}.demand-summary span{color:var(--muted,#708a94)}

.purchase-timetable{border:1px solid #cce2df;background:#edf7f5;border-radius:9px;padding:16px;margin:14px 0 18px;color:var(--ink,#234752)}
.timetable-head,.current-rule,.timetable-info,.period-time{display:flex;align-items:center;flex-wrap:wrap;gap:10px}.timetable-head,.timetable-info,.period-time{justify-content:space-between}.current-label,.beijing-time,.current-range,.timetable-info,small{font-size:12px;color:var(--muted,#708a94)}.current-rule strong{font-size:16px;color:var(--teal,#008786)}.beijing-time{font-variant-numeric:tabular-nums}.timetable-info{margin-top:8px;line-height:1.6;gap:4px 16px}.timetable-periods{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:10px;list-style:none;margin:14px 0 0;padding:0}.timetable-periods li{padding:11px 12px;border:1px solid var(--border,#dce7e9);background:#fff;border-radius:7px;min-width:0}.timetable-periods li.active{border:1px solid var(--teal,#008786);box-shadow:inset 0 0 0 1px var(--teal,#008786)}.period-time{font-size:12px;line-height:20px;gap:4px;color:var(--muted,#708a94);font-variant-numeric:tabular-nums}.timetable-periods strong{display:block;margin-top:5px;font-size:14px;font-weight:600}.timetable-periods small{display:block;margin-top:4px;font-size:11px}.current-badge{border-radius:4px;background:#e6f5f2;color:var(--teal,#008786);padding:0 5px;font-size:10px}.paused strong,.blocked .current-rule strong{color:#94661f}.blocked{background:#fff8ec;border-color:#eedcc0}.paused.active{border-color:#b17b2e!important;box-shadow:inset 0 0 0 1px #b17b2e!important}.paused .current-badge{background:#fff1d9;color:#94661f}.timetable-error{font-size:12px;color:#b34843;overflow-wrap:anywhere;margin:10px 0 0}
:global([data-theme=dark] .purchase-timetable){background:#193b38;border-color:#32615b}:global([data-theme=dark] .purchase-timetable.blocked){background:#3c3222;border-color:#6f5733}:global([data-theme=dark] .timetable-periods li){background:#192e38}:global([data-theme=dark] .timetable-periods .paused strong),:global([data-theme=dark] .blocked .current-rule strong){color:#efd3a1}:global([data-theme=dark] .current-badge){background:#193b38}:global([data-theme=dark] .paused .current-badge){background:#3c3222;color:#efd3a1}
@media(max-width:600px){.purchase-timetable{padding:12px}.timetable-periods{grid-template-columns:repeat(2,minmax(0,1fr));gap:8px}.timetable-periods li{padding:9px}.timetable-head{align-items:flex-start}.current-rule{gap:6px}.current-range{flex-basis:100%}.period-time{align-items:flex-start}.current-rule strong{font-size:15px}}
</style>
