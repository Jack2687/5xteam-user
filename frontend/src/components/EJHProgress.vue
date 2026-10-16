<script setup>
import { LoaderCircle } from 'lucide-vue-next'
defineProps({ progress: {type:Object,required:true} })
defineEmits(['stop','dismiss'])
</script>
<template>
 <section class="ejh-progress" aria-label="操作进度" :aria-busy="!progress.finished||progress.waitingForIssuer">
  <div class="progress-title"><LoaderCircle v-if="!progress.finished||progress.waitingForIssuer" class="spin" :size="18"/><strong>{{progress.label}}</strong><span>{{progress.waitingForIssuer?'卡台处理中':progress.cancellation&&progress.unknown?'待确认':progress.finished?'已结束':'等待中'}} · {{progress.elapsed}} 秒</span></div>
  <p role="status">{{progress.phase}}</p>
  <template v-if="progress.total">
   <progress :value="progress.done" :max="progress.total" :aria-label="'已处理 '+progress.done+' / '+progress.total"/>
   <p>已提交 {{progress.done}} / {{progress.total}} · 成功 {{progress.success}}<template v-if="progress.processing"> · 处理中 {{progress.processing}}</template> · 失败 {{progress.failed}} · 待核实 {{progress.unknown}} · 未提交 {{progress.total-progress.done}}</p>
   <button v-if="!progress.finished" class="btn ghost compact" :disabled="progress.stopRequested" @click="$emit('stop')">{{progress.stopRequested?'等待当前请求结束':'停止后续操作'}}</button>
   <details v-if="progress.results.length" :open="!!(progress.failed||progress.unknown)"><summary>逐项结果与请求编号</summary><p v-for="r in progress.results" :key="r.request_id">{{r.label}} · {{({success:'成功',failed:'失败',processing:'销卡处理中',unknown:'待核实'})[r.status]}}<br/><small>{{r.request_id}}</small><br v-if="r.error"/>{{r.error}}</p></details>
  </template>
  <p v-else-if="!progress.finished">等待服务器返回，完成后会自动更新；无需重复点击。</p>
  <button v-if="progress.finished" class="btn ghost compact" @click="$emit('dismiss')">收起进度</button>
 </section>
</template>
<style scoped>
.ejh-progress{padding:14px 18px;border:1px solid var(--line);border-radius:12px;background:var(--surface);font-size:12px;overflow-wrap:anywhere}.progress-title{display:flex;align-items:center;gap:8px;flex-wrap:wrap}.progress-title>span{color:var(--muted);margin-left:auto}.ejh-progress p{margin:9px 0;line-height:1.7}.ejh-progress progress{width:100%;height:8px;accent-color:var(--green)}.ejh-progress details{margin:10px 0}.ejh-progress summary{cursor:pointer}.ejh-progress small{color:var(--muted)}.spin{animation:ejh-spin 1s linear infinite}@keyframes ejh-spin{to{transform:rotate(360deg)}}
</style>
