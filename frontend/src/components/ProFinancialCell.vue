<script setup>
import OAIConfirmationStatus from './OAIConfirmationStatus.vue'
import ActivationCost from './ActivationCost.vue'
import NexovccProCost from './NexovccProCost.vue'
import NexovccProCasesDialog from './NexovccProCasesDialog.vue'
import { ref } from 'vue'
import { tradeKinds } from '../ejh'
import ReturnStatusBadge from './ReturnStatusBadge.vue'
import { formatTime } from '../utils'
const props=defineProps({account:Object,defaultPageSize:{type:Number,default:10}})
const emit=defineEmits(['updated','open-card'])
const nexovcc=ref(null)
function open(){if(props.account.financial_cases?.length&&props.account.financial_cases.every(c=>c.card_type==='nexovcc'))nexovcc.value.open({email:props.account.email});else opened.value=true}
const opened=ref(false),costRows=ref({})
</script>
<template><NexovccProCost v-if="account.financial_cases?.[0]?.nexovcc" :row="account.financial_cases[0].nexovcc"/><NexovccProCasesDialog ref="nexovcc" :default-page-size="defaultPageSize" @updated="emit('updated')" @open-card="emit('open-card',$event)"/><ActivationCost v-if="account.financial_cases?.length && account.financial_cases[0].card_type!=='nexovcc'" :row="costRows[account.financial_cases[0].id]||account.financial_cases[0]"/><button class="btn ghost compact" @click="open"><ReturnStatusBadge :state="account.financial_cases?.[0]?.state || 'unopened'"/><small v-if="account.financial_cases?.length>1">{{account.financial_cases.length}} 笔订单</small></button><Teleport to="body"><div v-if="opened" class="modal-backdrop" @click.self="opened=false"><section class="modal financial-dialog"><h2>{{account.email}} · 开通与回款</h2><p>一次开通对应一次回款，金额可与付款不同；合并只标记已申请待回款。</p><section v-for="c in account.financial_cases||[]" :key="c.id"><h3><ReturnStatusBadge :state="c.state"/> · {{c.plan}}</h3><p>{{c.provider}} · {{c.card_name}} · 尾号 {{c.last4}}</p><p>订单 {{c.id}}</p><OAIConfirmationStatus :records="c.pro_oai_confirmations" labelled/><div v-if="c.card_type==='nexovcc'"><NexovccProCost v-if="c.nexovcc" :row="c.nexovcc"/><button class="btn ghost compact" @click="opened=false;nexovcc.open({email:account.email,cardID:c.nexovcc?.card_id})">付款 / 回款 / 成本明细</button></div><ActivationCost v-else :row="costRows[c.id]||c" details editable @updated="costRows[c.id]=$event"/><p>开通：{{formatTime(c.opened_at)}}；申请标记：{{formatTime(c.requested_at)}} {{c.request_source==='manual'?'（人工标记）':''}}</p><p v-for="t in c.returns" :key="t.id">{{tradeKinds[t.trade.tradeType]}} · <ReturnStatusBadge :state="t.confirmed ? 'returned' : 'unverified'"/> · {{t.confirmed_amount}} {{t.confirmed_currency}}<br/>{{t.evidence}} · {{t.source==='manual_verified'?'人工核实':'卡台流水'}}</p></section><p v-if="!account.financial_cases?.length">暂无本地开通订单记录</p><button class="btn" @click="opened=false">关闭</button></section></div></Teleport></template>
<style scoped>.financial-dialog{max-height:85vh;overflow:auto;width:min(760px,94vw)}.financial-dialog section{margin:16px 0;padding:12px;border:1px solid var(--line)}.financial-dialog p{margin:10px 0;overflow-wrap:anywhere}small{display:block}</style>
