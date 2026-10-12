<script setup>
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { RefreshCw, Save, Download, Play } from 'lucide-vue-next'
import { api } from '../api'
import MessageBar from './MessageBar.vue'
const props = defineProps({ provider: { type: String, default: 'sub2' }, status: { type: Object, default: null } })
const emit = defineEmits(['saved'])
const form = reactive({})
const loaded = ref(false), busy = ref(false), groups = ref([]), initial = ref(null)
const message = reactive({ text: '', type: '' })
const isVM = computed(() => form.provider === 'vm2api')
const sourceLabel = computed(() => isVM.value ? 'vm2api' : 'Sub2')
const supported = computed(() => ['sub2', 'vm2api'].includes(initial.value?.provider || props.provider))
const stats = computed(() => (props.status?.provider === initial.value?.provider ? props.status : initial.value)?.summary || {})
const metrics = computed(() => [['total','监控账号'],['normal','正常'],['suspect','疑似异常'],['degraded','降智'],['errors','查询／检测失败'],['processed','已处理'],['routed','降智分组'],['kicked','已踢出']].filter(item => !isVM.value || !['suspect', 'routed'].includes(item[0])))
function setForm(settings) { Object.assign(form, settings, { provider: settings.provider || 'sub2', normal_group_ids: settings.normal_group_ids || [], degraded_group_ids: settings.degraded_group_ids || [], poll_interval_seconds: settings.poll_interval_seconds || 60, max_result_age_seconds: settings.max_result_age_seconds || 900, condition_mode: settings.condition_mode || 'any' }) }
let loadVersion = 0
async function load() { const version = ++loadVersion; loaded.value = false; try { const data = await api('/api/quality/settings'); if (version !== loadVersion) return; initial.value = data; setForm(data.settings); loaded.value = true } catch(e) { if (version === loadVersion) { message.text=e.message;message.type='error' } } }
async function loadGroups() { busy.value=true;try { groups.value=(await api('/api/sub2-settings/test',{method:'POST',body:{}})).groups || [] } catch(e) { message.text=e.message;message.type='error' } finally { busy.value=false } }
async function save() { busy.value=true;try { const data=await api('/api/quality/settings',{method:'PUT',body:form});initial.value=data;setForm(data.settings);message.text='结果查询与处理设置已保存';message.type='success';emit('saved',data) } catch(e) { message.text=e.message;message.type='error' } finally { busy.value=false } }
async function run() { const savedAction = initial.value?.settings?.action; if (!window.confirm(isVM.value ? (savedAction === 'kick' ? '读取 vm2api 最新结果，有效红色结果将触发母号强制踢出，并删除对应槽位和容器。继续？' : '仅获取 vm2api 最新降智结果，不执行踢出。继续？') : '立即读取 Sub2 已保存的检测结果，符合条件将执行踢出或切组。继续？')) return;busy.value=true;try { await api('/api/quality/run',{method:'POST'});message.text='已启动结果查询';message.type='success';emit('saved') } catch(e) { message.text=e.message;message.type='error' } finally { busy.value=false } }
function exportLegacy() { const url=URL.createObjectURL(new Blob([JSON.stringify(form,null,2)],{type:'application/json'}));const a=document.createElement('a');a.href=url;a.download='quality-settings.json';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000) }
onMounted(load)
watch(() => props.provider, load)
</script>
<template>
<section aria-label="降智处理统计" class="quality-stats">
 <article v-for="item in metrics" :key="item[0]"><span>{{ item[1] }}</span><strong>{{ stats[item[0]] || 0 }}</strong></article>
</section>
<section class="quality-settings">
 <div class="panel-title"><h2>降智结果与处理</h2></div>
 <MessageBar v-if="message.text" :message="message" />
 <p v-if="!supported" class="danger-text">结果查询仅支持 Sub2 和 vm2api，请先保存推送设置。</p>
 <p v-else class="panel-description">当前结果来源：{{ sourceLabel }}，跟随已保存的推送设置。</p>
 <p v-if="isVM" class="panel-description">题目、模型和定时检测在 vm2api 配置，Space 读取自动及手动检测结果。默认仅获取情况；选择强制踢出后，最新有效红色（单次检测不通过）才会触发移出并删除槽位和容器。黄色失败、无结果或过期均不踢出。已开始的移出清理会继续完成。</p>
 <form v-if="loaded" @submit.prevent="save">
  <label class="field check"><input v-model="form.enabled" type="checkbox" :disabled="!supported"><span>启用定时查询，按所选方式处理</span></label>
  <div class="quality-grid">
   <label class="field"><span>查询间隔（秒）</span><input v-model.number="form.poll_interval_seconds" type="number" min="10" max="86400" required></label>
   <label class="field"><span>检测结果有效期（秒）</span><input v-model.number="form.max_result_age_seconds" type="number" min="30" max="86400" required></label>
   <label v-if="!isVM" class="field check"><input v-model="form.question_enabled" type="checkbox"><span>答题已确认异常</span></label>
   <label v-if="!isVM" class="field check"><input v-model="form.model_audit_enabled" type="checkbox"><span>模型已确认不一致</span></label>
   <label v-if="!isVM" class="field"><span>处理条件</span><select v-model="form.condition_mode"><option value="any">任一勾选条件满足</option><option value="all">所有勾选条件同时满足</option></select></label>
   <label class="field"><span>符合条件后的处理</span><select v-model="form.action"><option v-if="isVM" value="observe">仅获取降智情况，不踢出</option><option value="kick">母号强制踢出并停止复用</option><option v-if="!isVM" value="groups">保留在空间，切换 Sub2 分组</option></select></label>
   <label class="field"><span>处理并发数</span><input v-model.number="form.concurrency" type="number" min="1" max="8" required></label>
   <label class="field"><span>每账号处理记录显示上限</span><input v-model.number="form.history_limit" type="number" min="1" max="1000" required></label>
   <template v-if="!isVM && form.action === 'groups'">
    <div class="wide"><button class="btn ghost" type="button" :disabled="busy" @click="loadGroups"><RefreshCw :size="15" />读取 Sub2 分组</button></div>
    <div v-for="field in [{ key: 'normal_group_ids', label: '移除的正常分组' }, { key: 'degraded_group_ids', label: '加入的降智分组' }]" :key="field.key" class="field">
     <span :id="field.key + '-label'">{{ field.label }} <small>已选 {{ form[field.key].length }} 个</small></span>
     <div class="quality-group-picker" role="group" :aria-labelledby="field.key + '-label'">
      <label v-for="group in groups" :key="group.id">
       <input v-model="form[field.key]" type="checkbox" :value="Number(group.id)" :disabled="busy" />
       <span>{{ group.name }}</span><small>#{{ group.id }}</small>
      </label>
      <p v-if="!groups.length">暂无可选分组</p>
     </div>
     <small>已选 ID：{{ form[field.key].join(', ') || '无' }}</small>
    </div>
    <label class="field check"><input v-model="form.auto_restore" type="checkbox"><span>所有勾选检测恢复正常后自动恢复分组</span></label>
    <label v-if="form.auto_restore" class="field"><span>恢复至少需要连续正常次数</span><input v-model.number="form.recovery_limit" type="number" min="1" max="20" required></label>
   </template>
  </div>
  <div class="heading-actions"><button class="btn primary" type="submit" :disabled="busy || !supported"><Save :size="15" />保存设置</button><button class="btn ghost" type="button" :disabled="busy || !form.enabled || !supported" @click="run"><Play :size="15" />立即查询并处理</button><button class="btn ghost" type="button" @click="exportLegacy"><Download :size="15" />导出配置</button></div>
 </form>
</section>
</template>
<style scoped>
.quality-stats{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));grid-template-rows:repeat(2,minmax(88px,auto));gap:10px;margin-bottom:20px}.quality-stats article{border:1px solid var(--line);border-radius:6px;padding:12px;min-width:0}.quality-stats span{font-size:12px;color:var(--muted);overflow-wrap:anywhere}.quality-stats strong{display:block;font-size:24px}.quality-settings{padding:16px 0}.quality-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:20px;margin:20px 0}.quality-grid>*{min-width:0}.quality-grid .wide{grid-column:1/-1}.check{display:flex;flex-direction:row;align-items:center;gap:10px}.check input{width:18px;height:18px;min-height:18px;padding:0}@media(max-width:700px){.quality-grid{grid-template-columns:minmax(0,1fr)}.quality-stats{gap:6px}.quality-stats article{padding:8px}.quality-stats strong{font-size:20px}}
.quality-group-picker { display: grid; align-content: start; min-height: 132px; max-height: 190px; overflow-y: auto; padding: 7px; gap: 5px; border: 1px solid var(--line); border-radius: 5px; background: var(--bg-elevated); }
.quality-group-picker label { display: grid; grid-template-columns: 18px minmax(0, 1fr) auto; align-items: center; min-height: 34px; padding: 6px 8px; gap: 8px; border-radius: 4px; cursor: pointer; }
.quality-group-picker label:hover, .quality-group-picker label:focus-within { background: var(--surface-2); }
.quality-group-picker input[type="checkbox"] { width: 16px; height: 16px; min-height: 16px; margin: 0; padding: 0; }
.quality-group-picker label span { overflow-wrap: anywhere; font-size: 12px; font-weight: 600; }
.quality-group-picker small, .quality-group-picker p { color: var(--muted); font-size: 11px; }
.quality-group-picker p { margin: 0; padding: 9px; }
</style>
