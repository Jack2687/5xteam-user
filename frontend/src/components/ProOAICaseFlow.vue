<script setup>
import { computed } from 'vue'
import { dispositionLabels } from '../pro-disposition'
import { caseOAIRecords } from '../oai-confirmation'
import ProOAIConfirmationStages from './ProOAIConfirmationStages.vue'
import OAIConfirmationStatus from './OAIConfirmationStatus.vue'
const props=defineProps({row:{type:Object,required:true}})
const emit=defineEmits(['updated'])
const account=computed(()=>({...props.row,pro_oai_confirmations:caseOAIRecords(props.row)}))
const modes=computed(()=>[...new Set([...Object.keys(account.value.pro_oai_confirmations),...(props.row.pro_oai_available_modes||[])])])
</script>
<template>
 <div class="case-oai-flow">
  <template v-if="modes.length"><div v-for="mode in modes" :key="mode" class="case-oai-mode"><small v-if="modes.length>1">{{dispositionLabels[mode]||'回款确认'}}</small><ProOAIConfirmationStages :account="account" :mode="mode" :order-id="row.id" :actions-available="!!row.pro_oai_actions_available && row.state!=='returned'" @updated="emit('updated')"/></div></template>
  <OAIConfirmationStatus v-else :records="{}"/>
 </div>
</template>
<style scoped>.case-oai-mode+.case-oai-mode{margin-top:12px}.case-oai-mode>small{display:block;color:var(--muted);font-size:11px;margin-bottom:6px}</style>
