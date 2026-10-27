<script setup>
import { computed } from 'vue'
import { waitingOAIReplies } from '../oai-confirmation'
import OAIConfirmationReply from './OAIConfirmationReply.vue'
const props=defineProps({rows:{type:Array,default:()=>[]},withinButton:Boolean})
const entries=computed(()=>waitingOAIReplies(props.rows))
</script>
<template>
 <OAIConfirmationReply v-if="entries.length" :entries="entries" :pin-on-click="false" :trigger-tabindex="withinButton?-1:0" trigger-label="已申请待回款，查看 OAI 已确认的 Help 回答"><slot/></OAIConfirmationReply>
 <slot v-else/>
</template>
