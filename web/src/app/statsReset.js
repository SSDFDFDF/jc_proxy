export function buildStatsResetPayload(scope, vendorID, confirmation) {
  if (confirmation !== 'RESET') throw new Error('请输入 RESET 确认重置')
  if (scope === 'all') return { scope: 'all', confirmation }
  const id = String(vendorID || '').trim()
  if (scope !== 'vendor' || !id) throw new Error('请选择要重置的供应商')
  return { scope: 'vendor', vendor_id: id, confirmation }
}
