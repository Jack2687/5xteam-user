<script setup>
import { computed } from 'vue'
import { cardFunding } from '../nexovcc-reconciliation'
import { money, rmb } from '../nexovcc-finance'
const props=defineProps({data:{type:Object,required:true},row:{type:Object,required:true}})
defineEmits(['details'])
const summary=computed(()=>cardFunding(props.data,props.row))
</script>
<template>
 <div class="funding" aria-label="额度、退回与差额">
  <div><span>累计额度</span><button @click="$emit('details',{type:'funds',kind:'funding'})">{{money(summary.funded)}} USD</button></div>
  <div v-if="summary.withdrawn"><span>已提现</span><button @click="$emit('details',{type:'funds',kind:'4002'})">{{money(summary.withdrawn)}} USD</button></div>
  <div><span>销卡退回</span><button @click="$emit('details',{type:'funds',kind:'3002'})">{{money(summary.returned)}} USD</button></div>
  <div class="difference"><span>差额</span><strong>{{money(summary.difference)}} USD<small v-if="summary.difference!==null">{{rmb(summary.difference)}} R</small></strong></div>
  <small>{{summary.note}}</small>
  <button class="detail" @click="$emit('details',{type:'funds',kind:'card-transfers'})">划转明细<template v-if="summary.additions"> · 追加 {{summary.additions}} 次</template></button>
 </div>
</template>
<style scoped>
.funding{min-width:190px;font-size:11px;line-height:1.6;font-variant-numeric:tabular-nums}.funding>div{display:flex;justify-content:space-between;gap:10px;margin-bottom:7px}.funding span,.funding>small{color:var(--muted)}.funding button{border:0;background:none;color:var(--green-strong);font:inherit;padding:0;cursor:pointer}.funding button:hover{text-decoration:underline}.difference{border-top:1px solid var(--line);padding-top:8px}.difference strong{text-align:right;font-weight:600}.funding small{display:block;font-size:10px;font-weight:400}.detail{margin-top:7px!important}
</style>
