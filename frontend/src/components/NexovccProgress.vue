<script setup>
import { LoaderCircle } from 'lucide-vue-next'
defineProps({job:{type:Object,default:()=>({})},batch:{type:Object,default:null},busy:Boolean})
</script>
<template>
 <div v-if="busy||job.status==='running'||batch?.running" class="nex-progress" role="status" aria-live="polite">
  <LoaderCircle :size="16" class="spinner"/>
  <div><strong>{{job.card_label||batch?.title||'正在处理'}}</strong><span>{{job.message||'正在提交请求…'}}</span>
   <small v-if="job.card_total">已处理 {{job.card_done||0}} / {{job.card_total}} 张</small><small v-else-if="batch">{{batch.done}} / {{batch.total}} 张</small>
   <progress v-if="job.card_total||batch" :value="job.card_total?job.card_done:batch.done" :max="job.card_total||batch.total"/>
  </div>
 </div>
</template>
<style scoped>
.nex-progress{display:flex;align-items:flex-start;gap:10px;padding:12px 14px;border-radius:8px;background:var(--green-soft);color:var(--green-strong);font-size:12px}.nex-progress>div{flex:1;min-width:0}.nex-progress strong{font-weight:600;margin-right:10px}.nex-progress span{line-height:1.8}.nex-progress small{display:block;margin-top:4px}progress{display:block;width:100%;height:4px;margin-top:7px;accent-color:var(--green-strong)}.spinner{flex-shrink:0;margin-top:3px;animation:spin 1s linear infinite}@keyframes spin{to{transform:rotate(360deg)}}@media(prefers-reduced-motion:reduce){.spinner{animation:none}}
</style>
