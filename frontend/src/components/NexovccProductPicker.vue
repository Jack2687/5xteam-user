<script setup>
import {computed,ref} from 'vue'
import {GripVertical,ChevronUp,ChevronDown,X} from 'lucide-vue-next'
import {moveProduct} from '../nexovcc'
const props=defineProps({modelValue:{type:Array,default:()=>[]},products:{type:Array,default:()=>[]},disabled:Boolean})
const emit=defineEmits(['update:modelValue'])
const dragged=ref(-1),over=ref(-1),expanded=ref(false)
const selected=computed(()=>props.modelValue.map(String))
const label=id=>{const p=props.products.find(p=>String(p.id)===id);return p?`${p.name} · ${p.bin||'BIN 待同步'} · 最低 ${p.minimum} USD`:`产品 #${id} · 当前不可用`}
function toggle(id,checked){if(props.disabled)return;emit('update:modelValue',checked?[...selected.value,String(id)]:selected.value.filter(v=>v!==String(id)))}
function move(from,to){if(!props.disabled)emit('update:modelValue',moveProduct(selected.value,from,to))}
function start(event,index){if(props.disabled)return;dragged.value=index;event.dataTransfer.effectAllowed='move';event.dataTransfer.setData('text/plain',String(index))}
function drop(index){if(dragged.value>=0)move(dragged.value,index);dragged.value=over.value=-1}
</script>
<template>
 <div class="product-picker">
  <div class="picker-heading"><span>产品 / BIN</span><button type="button" :disabled="disabled" @click="expanded=!expanded">{{expanded?'收起选择':`选择产品（${selected.length}）`}}</button></div>
  <div v-if="expanded" class="choices"><label v-for="p in products" :key="p.id"><input type="checkbox" :disabled="disabled" :checked="selected.includes(String(p.id))" @change="toggle(p.id,$event.target.checked)"/>{{label(String(p.id))}}</label><small v-if="!products.length">请先同步产品列表</small></div>
  <ol v-if="selected.length" aria-label="产品优先级"><li v-for="(id,i) in selected" :key="id" :draggable="!disabled" :class="{over:over===i}" @dragstart="start($event,i)" @dragover.prevent="over=i" @drop.prevent="drop(i)" @dragend="dragged=over=-1"><GripVertical :size="14" class="grip"/><b>{{i+1}}</b><span>{{label(id)}}</span><button type="button" :disabled="disabled||i===0" :aria-label="`提高产品 ${id} 优先级`" @click="move(i,i-1)"><ChevronUp :size="14"/></button><button type="button" :disabled="disabled||i===selected.length-1" :aria-label="`降低产品 ${id} 优先级`" @click="move(i,i+1)"><ChevronDown :size="14"/></button><button type="button" :disabled="disabled" :aria-label="`移除产品 ${id}`" @click="toggle(id,false)"><X :size="14"/></button></li></ol>
  <small>{{selected.length?'拖动调整优先级，优先使用上方可用产品':'至少选择一个产品，可多选备用卡段'}}</small>
 </div>
</template>
<style scoped>
.product-picker{display:grid;gap:9px;font-size:12px;min-width:0}.picker-heading{display:flex;justify-content:space-between;align-items:center;gap:12px}.product-picker button{background:transparent;border:0;color:var(--green-strong);cursor:pointer;display:inline-flex;align-items:center;padding:3px}.product-picker button:disabled{opacity:.35;cursor:default}.choices{display:grid;gap:10px;max-height:190px;overflow:auto;padding:12px;border:1px solid var(--line);border-radius:7px}.choices label{display:flex;align-items:center;gap:8px;line-height:1.5}.choices input{width:auto;accent-color:var(--green-strong)}ol{margin:0;padding:0;list-style:none;display:grid;gap:6px}li{display:flex;align-items:center;gap:7px;padding:9px 7px;border:1px solid var(--line);border-radius:7px;background:var(--surface)}li.over{border-color:var(--green-strong)}li>span{flex:1;min-width:0;overflow-wrap:anywhere;line-height:1.5}li b{color:var(--muted);font-size:11px}.grip{cursor:grab;flex-shrink:0;color:var(--muted)}small{font-size:11px;color:var(--muted);line-height:1.6}
</style>
