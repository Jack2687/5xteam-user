<script setup>
import OAIReturnReply from './OAIReturnReply.vue'
import { computed } from 'vue'
import { returnStates } from '../ejh'
import { waitingRequestSummary } from '../ejhRequestTime'
import {waitingOAIConfirmedCount} from '../oai-confirmation'
import ReturnStatusBadge from './ReturnStatusBadge.vue'
const props=defineProps({state:String,rows:{type:Array,default:()=>[]}})
const count=computed(()=>props.rows.filter(row=>row.state===props.state).length)
const waiting=computed(()=>waitingRequestSummary(props.rows))
</script>
<template><OAIReturnReply :rows="state==='waiting'?rows:[]" within-button><ReturnStatusBadge :oai-confirmed="waitingOAIConfirmedCount(rows)" :total="count" :state="state" :show-requested-at="state==='waiting'&&count>0" :requested-at="waiting.at" :request-label="waiting.label" :title="state==='waiting'?waiting.title:undefined">{{returnStates[state]}} {{count}}</ReturnStatusBadge></OAIReturnReply></template>
