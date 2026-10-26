<script setup>
import { onBeforeUnmount, ref } from 'vue'
import { formatTime } from '../utils'
const props=defineProps({record:Object})
const anchor=ref(null),opened=ref(false),pinned=ref(false),position=ref({}),copied=ref(false)
let timer
function keep(){clearTimeout(timer)}
function show(pin=false){clearTimeout(timer);if(pin)pinned.value=true;const r=anchor.value?.getBoundingClientRect();if(!r)return;const width=Math.min(480,innerWidth-24);position.value={left:Math.max(12,Math.min(r.left,innerWidth-width-12))+'px',width:width+'px',maxHeight:Math.max(160,Math.min(420,innerHeight-40))+'px',...(r.top>innerHeight/2?{bottom:innerHeight-r.top+8+'px'}:{top:Math.min(r.bottom+8,innerHeight-200)+'px'})};opened.value=true}
function leave(){clearTimeout(timer);if(!pinned.value)timer=setTimeout(()=>opened.value=false,160)}
function close(){clearTimeout(timer);opened.value=false;pinned.value=false}
async function copy(){try{await navigator.clipboard.writeText(props.record?.answer||'');copied.value=true}catch{copied.value=false}}
onBeforeUnmount(()=>clearTimeout(timer))
</script>
<template>
 <span ref="anchor" class="oai-reply-anchor" tabindex="0" aria-label="查看 Help 完整回答" @mouseenter="show()" @mouseleave="leave" @focusin="show()" @focusout="leave" @click="show(true)" @keydown.enter.prevent="show(true)" @keydown.esc="close"><slot/></span>
 <Teleport to="body"><aside v-if="opened" class="oai-reply-popover" :style="position" role="dialog" aria-label="Help 完整回答" @mouseenter="keep" @focusin="keep" @focusout="leave" @mouseleave="leave" @keydown.esc="close">
  <header><strong>Help 完整回答</strong><button type="button" aria-label="关闭 Help 回答" @click="close">关闭</button></header>
  <small v-if="record?.checked_at">询问时间 {{formatTime(record.checked_at)}}</small>
  <p class="oai-answer">{{record?.answer || record?.error || '尚未收到 Help 完整回答'}}</p>
  <footer><span>确认退款已发起／处理；到账以卡台记录为准。</span><button v-if="record?.answer" type="button" @click="copy">{{copied?'已复制':'复制回答'}}</button></footer>
  <details v-if="record?.replies?.length>1"><summary>历史回答（{{record.replies.length}}）</summary><section v-for="reply in record.replies" :key="reply.message_id"><small>{{formatTime(reply.at)}}</small><p class="oai-answer">{{reply.answer}}</p></section></details>
 </aside></Teleport>
</template>
<style scoped>
.oai-reply-anchor{display:inline-flex;cursor:pointer;border-radius:5px}.oai-reply-anchor:focus-visible{outline:2px solid var(--green)}.oai-reply-popover{position:fixed;z-index:1600;overflow:auto;padding:16px;border:1px solid var(--line);border-radius:12px;background:var(--surface);color:var(--text);box-shadow:0 10px 36px #0003;font-size:12px}.oai-reply-popover header,.oai-reply-popover footer{display:flex;justify-content:space-between;align-items:center;gap:14px}.oai-reply-popover small,.oai-reply-popover footer{color:var(--muted);font-size:11px}.oai-reply-popover small{display:block;margin-top:8px}.oai-reply-popover button{border:1px solid var(--line);border-radius:5px;padding:5px 9px;white-space:nowrap}.oai-answer{white-space:pre-wrap;overflow-wrap:anywhere;line-height:1.8;margin:12px 0}.oai-reply-popover details{margin-top:12px;border-top:1px solid var(--line);padding-top:10px}.oai-reply-popover section+section{border-top:1px solid var(--line)}
</style>
