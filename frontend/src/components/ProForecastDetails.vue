<script setup>
import { computed } from 'vue'
import { forecastDetails, moneyLabel, rateLabel, windowLabel } from '../restockForecast'
const props = defineProps({decision:{type:Object,default:()=>({})},historical:Boolean,now:{type:Number,default:()=>Date.now()}})
const report = computed(()=>forecastDetails(props.decision,props.historical ? Date.parse(props.decision.at) : props.now))
</script>
<template>
 <section class="forecast-detail" aria-label="美元额度与消耗详情">
  <header><h3>额度与消耗</h3><b>{{report.supply}}</b></header>
  <p>{{report.summary}}</p>
  <p class="hint">按最近 {{report.windowMinutes}} 分钟账号费用计算，剩余额度 ÷ 每分钟消耗。</p>
  <div class="forecast-scroll"><table><thead><tr><th>账号</th><th>预计总额</th><th>已用</th><th>剩余</th><th>每分钟消耗</th></tr></thead><tbody>
   <tr v-for="row in report.rows" :key="row.id"><td>{{row.name || '#'+row.id}}<small>{{row.reason || windowLabel(row.seconds)}}</small></td><td>{{moneyLabel(row.total_usd)}}</td><td>{{moneyLabel(row.used_usd)}}</td><td>{{moneyLabel(row.remaining_usd)}}</td><td>{{rateLabel(row.rate_usd)}}</td></tr>
   <tr v-if="!report.rows.length"><td colspan="5">{{report.supply}}</td></tr>
  </tbody></table></div>
 </section>
</template>
<style scoped>
.forecast-detail{border:1px solid var(--line,#dce7e9);border-radius:11px;padding:20px;margin:0 0 20px;background:var(--surface,transparent);font-size:12px}.forecast-detail header{display:flex;justify-content:space-between;gap:16px;align-items:center}.forecast-detail h3{font-size:15px;margin:0}.forecast-detail header b{font-size:18px}.forecast-detail p{line-height:1.8;margin:10px 0}.hint,td small{color:var(--muted,#708a94)}.forecast-scroll{overflow:auto}table{width:100%;min-width:600px;border-collapse:collapse}td,th{padding:12px;text-align:left;border-bottom:1px solid var(--line,#dce7e9);vertical-align:top}th{font-size:11px;color:var(--muted,#708a94)}td small{display:block;line-height:1.7}
</style>
