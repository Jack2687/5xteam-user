<script setup>
import { computed } from 'vue'
import { cardOpeningAmounts, cardFundedAmounts, cardClosureAmounts } from '../ejh-card-funding'
const props = defineProps({ card: { type: Object, required: true }, operations: { type: Array, default: () => [] }, trades: { type: Array, default: () => [] } })
const opening = computed(() => cardOpeningAmounts(props.card, props.operations))
const funded = computed(() => cardFundedAmounts(props.card, props.operations))
const closure = computed(() => cardClosureAmounts(props.card, props.trades))
</script>

<template>
 <div class="card-funding">
  <div class="opening-amount" title="原始开卡订单保存的额度与结算金额">
   <span>初始开卡额度</span><strong>{{opening.amount}}</strong><small>结算 {{opening.settlement}}</small>
  </div>
  <div class="funded-amount" title="初始额度加成功追加额度；已确认完成的追加只计入一次">
   <span>累计额度<span v-if="funded.increases">（成功追加 {{funded.increases}} 次）</span></span>
   <strong v-for="line in funded.amounts" :key="line">{{line}}</strong><strong v-if="!funded.amounts.length">未记录</strong>
   <small>结算 {{funded.settlement}}{{funded.missingSettlements&&funded.settlement!=='未记录'?'（已知部分）':''}}</small>
   <small v-if="funded.missing">初始或追加额度缺失，合计仅含已知数据</small>
   <small v-if="funded.missingSettlements">{{funded.missingSettlements}} 笔追加的结算金额未提供</small>
  </div>
  <div class="closure-amount" title="已入账销卡退回流水，按币种分别合计">
   <span>销卡退回</span>
   <strong v-for="line in closure.transaction" :key="line">{{line}}</strong>
   <strong v-if="!closure.transaction.length">{{closure.posted?'交易金额未记录':closure.pending?'待入账':closure.unknown?'待核实':'暂无退回记录'}}</strong>
   <small v-for="line in closure.settlement" :key="line">结算 {{line}}</small>
   <small v-if="closure.posted&&!closure.settlement.length">结算未记录</small>
   <small v-if="closure.pending">{{closure.pending}} 笔待入账，未计入</small>
   <small v-if="closure.unknown">{{closure.unknown}} 笔状态待核实</small>
   <small v-if="closure.missingTransaction||closure.missingSettlement">部分金额缺失，合计仅含已知数据</small>
  </div>
 </div>
</template>

<style scoped>
.card-funding{display:grid;gap:12px;min-width:140px;font-variant-numeric:tabular-nums}.card-funding span,.card-funding small,.card-funding strong{display:block;line-height:1.6;overflow-wrap:anywhere}.card-funding span{font-size:11px;color:var(--muted);margin-bottom:4px}.card-funding strong{font-size:12px}.card-funding small{font-size:10px;color:var(--muted);margin-top:3px}.closure-amount{padding-top:10px;border-top:1px solid var(--line)}
</style>
