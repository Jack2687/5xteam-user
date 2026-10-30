// Read-only synchronization continues on the server when a page is closed.
// Poll the exact task, so another date range can never satisfy this refresh.
export async function refreshEJHSync({ api, onProgress = () => {}, stopped = () => false, delay = ms => new Promise(resolve => setTimeout(resolve, ms)), now = Date.now }) {
  const deadline = now() + 210000
  let task = await api('/api/ejh/sync', { method: 'POST', body: { async: true }, timeoutMs: 15000 })
  let readFailures = 0
  while (true) {
    if (task?.synced === true || task?.status === 'completed') return { synced: true }
    if (task?.status === 'failed') throw new Error(task.error || '卡片与交易同步失败，已保留已有数据')
    if (!task?.id || !['queued', 'running'].includes(task.status)) throw new Error('同步任务响应异常，请重新刷新')
    if (stopped()) return { stopped: true }
    onProgress(task.status === 'queued' ? '等待其他卡台操作结束，随后更新卡片与交易' : '正在更新卡片与交易，完成后自动读取结果')
    if (now() >= deadline) throw new Error('同步进度等待超时，请重试读取；已有数据保留')
    await delay(1500)
    if (stopped()) return { stopped: true }
    try {
      const next = await api('/api/ejh/sync/' + encodeURIComponent(task.id), { timeoutMs: 10000 })
      if (next?.id !== task.id) throw new Error('同步任务编号不一致')
      task = next
      readFailures = 0
    } catch (error) {
      if (error.status === 401 || error.status === 403 || error.status === 404 || ++readFailures >= 3) throw error
      onProgress('同步进度暂时读取失败，正在重新读取；后台任务继续执行')
    }
  }
}
