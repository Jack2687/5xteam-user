<script setup>
import { computed, onBeforeUnmount, ref } from 'vue'
import ReturnCaseLine from './ReturnCaseLine.vue'
const props=defineProps({rows:{type:Array,default:()=>[]}})
const opened=ref(false),position=ref({}),anchor=ref(null)
const shown=computed(()=>props.rows.slice(0,30))
function show(){
 const rect=anchor.value?.getBoundingClientRect();if(!rect)return
 const width=Math.min(430,window.innerWidth-24)
 position.value={left:Math.max(12,Math.min(rect.left,window.innerWidth-width-12))+'px',width:width+'px',maxHeight:Math.min(340,window.innerHeight/2-20)+'px',...(rect.top>window.innerHeight/2?{bottom:(window.innerHeight-rect.top+8)+'px'}:{top:(rect.bottom+8)+'px'})}
 opened.value=true
}
function hide(){opened.value=false}
window.addEventListener('scroll',hide,true);window.addEventListener('resize',hide)
onBeforeUnmount(()=>{window.removeEventListener('scroll',hide,true);window.removeEventListener('resize',hide)})
</script>
<template><span ref="anchor" class="card-account-preview" @mouseenter="show" @mouseleave="hide" @focusin="show" @focusout="hide" @click="hide" @keydown.esc="hide"><slot/></span><Teleport to="body"><aside v-if="opened" role="tooltip" class="card-account-popover" :style="position"><strong>关联账号 · {{rows.length}} 笔订单</strong><ReturnCaseLine v-for="row in shown" :key="row.id" :row="row" compact/><p v-if="!rows.length">暂无关联账号</p><p>点击开通数量查看完整详情</p></aside></Teleport></template>
<style scoped>.card-account-preview{display:inline-flex}.card-account-popover{position:fixed;z-index:1200;padding:16px;border:1px solid var(--line);border-radius:12px;background:var(--surface);color:var(--text);box-shadow:0 8px 28px #0002;overflow:auto;pointer-events:none}.card-account-popover>strong{font-size:12px}.card-account-popover>p{font-size:11px;color:var(--muted);margin-top:12px}</style>
