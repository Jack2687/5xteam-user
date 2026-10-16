<script setup>
import { computed } from 'vue'
import { forecastDetails, rateLabel, windowLabel } from '../restockForecast'
import { formatTime } from '../utils'
const props = defineProps({decision:{type:Object,default:()=>({})},historical:Boolean,now:{type:Number,default:()=>Date.now()}})
const report = computed(()=>forecastDetails(props.decision,props.historical ? Date.parse(props.decision.at) : props.now))
const number = value => typeof value === 'number' && Number.isFinite(value) ? value.toLocaleString('zh-CN',{maximumFractionDigits:3}) : '—'
</script>
<template>
 <section class="forecast-detail" aria-label="额度与消耗预测详情">
  <header><h3>额度与消耗预测</h3><span>{{report.supply}}</span></header>
  <p>单位为同套餐额度的百分点：1 百分点等于一个账号该窗口额度的 1%。各窗口分别计算，不相加；当前数据不提供 Token 或美元换算。</p>
  <p v-if="decision.supply_capped">在配置的 {{decision.config?.forecast_minutes}} 分钟预测范围内未出现缺口，实际可支撑时间可能更长。</p>
  <p v-if="report.stale" role="status">快照已失效，下面保留历史样本，不能作为当前消耗或新增补货依据。</p>
  <p v-else-if="!report.cohorts.length">尚无可用速率：需同套餐、同窗口的连续有效样本。零消耗会显示 0，样本缺失不会按 0 处理。</p>
  <p v-if="report.partial" role="status">以下仅为有效样本小计，覆盖 {{number(report.coverage)}}%；账号窗口组合不一致或存在缺失样本时，不能当作全组消耗。</p><div class="forecast-scroll" v-if="report.cohorts.length"><table><thead><tr><th>额度窗口 / 账号数</th><th>当前采样消耗</th><th>预测采用速度</th><th>按预测速度估算剩余</th><th v-for="n in [10,30,60]" :key="n">{{n}} 分钟预计消耗</th></tr></thead><tbody><tr v-for="g in report.cohorts" :key="g.key"><td>{{windowLabel(g.seconds)}} · {{g.accounts}} 个</td><td>{{rateLabel(g.observed)}}</td><td>{{rateLabel(g.rate)}}</td><td>{{number(g.remaining)}} 百分点</td><td v-for="n in [10,30,60]" :key="n">{{number(g.rate*n)}} 百分点</td></tr></tbody></table></div>
  <p>当前消耗 = 最近两次有效额度差 ÷ 间隔分钟；预测速度在消耗上升时立即跟随，下降时平滑回落。10 / 30 / 60 分钟是按预测速度计算的需求，不等于承诺可用额度。</p>
  <p>支撑时间按 15 秒步长检查首次供给缺口，仅考虑 Sub2 分组的额度、消耗速度、额度重置、账号到期和并发数据；不使用 Pro 推送时限，也不把尚未入组的新号计入当前支撑时间。预测范围 {{decision.config?.forecast_minutes || '—'}} 分钟；可信覆盖 {{number(decision.coverage)}}%；预计就绪 {{number(decision.lead_minutes)}} 分钟。</p>
  <details :open="historical"><summary>查看逐账号采样与额度明细（{{report.rows.length}} 项）</summary><div class="forecast-scroll"><table><thead><tr><th>账号 / 套餐</th><th>额度窗口</th><th>已用 / 剩余</th><th>当前 / 预测速度</th><th>采样依据</th><th>重置 / 账号到期</th><th>是否计入</th></tr></thead><tbody><tr v-for="(w,i) in report.rows" :key="i"><td>{{w.name}}<small>{{w.plan || '套餐未确认'}}</small></td><td>{{w.seconds ? windowLabel(w.seconds) : '—'}}</td><td>{{number(w.used)}}% / {{number(typeof w.used==='number'?100-w.used:null)}}%</td><td>{{rateLabel(w.observed_rate)}}<small>预测 {{w.trusted ? rateLabel(w.rate) : '暂不可用'}}</small></td><td>{{formatTime(w.at)}}<small>{{w.samples||0}} 次有效变化 · 间隔 {{number(w.sample_seconds)}} 秒</small></td><td>{{w.reset_at ? formatTime(new Date(w.reset_at*1000).toISOString()) : '—'}}<small>到期 {{formatTime(w.expires_at)}}</small></td><td>{{w.reason || '可信样本'}}</td></tr></tbody></table></div></details>
 </section>
</template>
<style scoped>
.forecast-detail{border:1px solid var(--line,#dce7e9);border-radius:11px;padding:20px;margin:0 0 20px;background:var(--surface,transparent);font-size:12px}.forecast-detail header{display:flex;justify-content:space-between;gap:16px;align-items:center}.forecast-detail h3{font-size:15px;margin:0}.forecast-detail header span{font-size:18px;font-weight:600}.forecast-detail p{color:var(--muted,#708a94);line-height:1.8;margin:12px 0}.forecast-scroll{overflow:auto}table{width:100%;min-width:740px;border-collapse:collapse}td,th{padding:12px;text-align:left;border-bottom:1px solid var(--line,#dce7e9);vertical-align:top}th{font-size:11px;color:var(--muted,#708a94)}td small{display:block;line-height:1.7;color:var(--muted,#708a94)}summary{cursor:pointer;padding:8px 0;font-weight:600}details table{min-width:1050px}
</style>
