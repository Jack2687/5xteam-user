<script setup>
import { computed } from 'vue'
import { caseAmounts, money, rmb } from '../nexovcc-finance'
const props=defineProps({row:{type:Object,required:true}})
const value=computed(()=>caseAmounts(props.row))
</script>
<template><div class="pro-nex-cost"><small v-if="row.pro_dead">死号亏损 {{money(value.deadLoss)}} USD</small><strong>成本 {{rmb(value.cost)}} R</strong><small>{{money(value.cost)}} USD · 结余 {{money(value.profit)}} R</small><small>付款 {{money(value.paid)}} / 回款 {{money(value.returned)}} USD</small><small v-if="!row.match">待匹配流水</small><small v-else-if="!value.closed">回款未完成 · 暂计</small></div></template>
<style scoped>.pro-nex-cost{font-size:12px;min-width:150px;line-height:1.8}.pro-nex-cost strong{font-weight:500}.pro-nex-cost small{display:block;font-size:10px;color:var(--muted)}</style>
