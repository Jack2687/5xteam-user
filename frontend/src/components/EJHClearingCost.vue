<script setup>
import { usdToR } from '../ejh-profit'
defineProps({summary:{type:Object,required:true}})
</script>
<template><div class="clearing-cost"><span>清算成本</span><div><strong>{{summary.rows.map(r=>r.amount+' '+r.currency).join(' / ')||'0.00'}}</strong><small>{{summary.estimated?'≈ ':''}}{{summary.usd.amount??'—'}} USD · {{usdToR(summary.usd.amount)??'—'}} R</small><small v-if="summary.missing||summary.pending">部分清算待核实</small></div></div></template>
<style scoped>.clearing-cost{display:flex;justify-content:space-between;gap:12px;font-size:11px;border-top:1px solid var(--line);padding-top:8px;margin-top:8px}.clearing-cost>span{color:var(--muted)}.clearing-cost>div{text-align:right}.clearing-cost strong{font-size:12px}.clearing-cost small{display:block;font-size:10px;color:var(--muted);line-height:1.6}</style>
