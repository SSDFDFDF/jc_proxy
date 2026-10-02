import { useMemo, useRef, useState } from 'react'

import { buttonClass, parseKeysText } from '../../app/utils'
import { Icon } from './shared'

/* ── Add keys modal: paste tab + backup-import tab ───── */

const TABS = [
  { id: 'paste', label: '粘贴输入', icon: 'plus' },
  { id: 'import', label: '导入备份', icon: 'download' }
]

export function AddKeysModal({
  vendorName,
  busy,
  existingKeys,
  vendorRows,
  upstreamItems,
  onAddKeys,
  onImportBackup,
  onClose
}) {
  const [tab, setTab] = useState('paste')
  const [draftText, setDraftText] = useState('')
  const [pasteHint, setPasteHint] = useState('')
  const [importBackup, setImportBackup] = useState(null)
  const [importHint, setImportHint] = useState('')
  const [importReport, setImportReport] = useState(null)
  const fileInputRef = useRef(null)

  /* ── Paste tab ── */
  const parsedKeys = useMemo(() => parseKeysText(draftText), [draftText])
  const newKeys = useMemo(() => parsedKeys.filter((key) => !existingKeys.has(key)), [parsedKeys, existingKeys])

  const submitAddKeys = async () => {
    if (!newKeys.length) {
      setPasteHint(parsedKeys.length ? '输入的密钥都已存在。若是已禁用密钥，请直接在列表中点击启用。' : '请输入至少一条有效密钥，一行一个。')
      return
    }
    const ok = await onAddKeys(newKeys)
    if (!ok) return
    setDraftText('')
    setPasteHint('')
    onClose()
  }

  /* ── Import tab ──
   * Analyse a parsed backup against the live vendor set (from vendorRows, which
   * is the union of configured vendors and any orphan key partitions). Vendors
   * missing from the live set are flagged and their keys are excluded from the
   * pending import set. This preview is recomputed whenever either the backup
   * or the vendor list changes, so the operator sees the consequences before
   * committing. */
  const importAnalysis = useMemo(() => {
    if (!importBackup) return null
    const liveVendorIDs = new Set(vendorRows.map((row) => row.id).filter(Boolean))
    const liveItems = upstreamItems || {}
    const backupVendors = Array.isArray(importBackup.vendors) ? importBackup.vendors : []

    // Merge duplicate vendor blocks so the preview table has unique keys and
    // counts are accurate even when the backup file repeats a vendor_id.
    const mergedBlocks = new Map()
    let totalKeys = 0
    for (const block of backupVendors) {
      const vendorID = String(block?.vendor_id || '').trim()
      const keys = Array.isArray(block?.keys) ? block.keys : []
      totalKeys += keys.length
      if (!vendorID) {
        const prev = mergedBlocks.get('')
        if (prev) prev.keys = [...prev.keys, ...keys]
        else mergedBlocks.set('', { vendor_id: '', vendor_name: block?.vendor_name || '', keys: [...keys] })
        continue
      }
      if (mergedBlocks.has(vendorID)) {
        const prev = mergedBlocks.get(vendorID)
        prev.keys = [...prev.keys, ...keys]
      } else {
        mergedBlocks.set(vendorID, { vendor_id: vendorID, vendor_name: block?.vendor_name || vendorID, keys: [...keys] })
      }
    }

    const valid = []
    const skipped = []
    let validKeys = 0
    for (const [vendorID, block] of mergedBlocks) {
      if (!vendorID) {
        skipped.push({ vendor_id: '(空)', vendor_name: block.vendor_name, count: block.keys.length, reason: '备份中缺少供应商 ID' })
        continue
      }
      if (!liveVendorIDs.has(vendorID)) {
        skipped.push({ vendor_id: vendorID, vendor_name: block.vendor_name, count: block.keys.length, reason: '供应商不存在，将跳过该供应商下全部 key' })
        continue
      }
      // Deduplicate keys within the merged block so counts reflect what will
      // actually be imported (the backend skips duplicates too).
      const existing = new Set((liveItems[vendorID] || []).map((item) => item.key))
      const seenInBackup = new Set()
      let newCount = 0
      let dupCount = 0
      let backupDupCount = 0
      for (const entry of block.keys) {
        const key = String(entry?.key || '').trim()
        if (!key) continue
        if (seenInBackup.has(key)) { backupDupCount += 1; continue }
        seenInBackup.add(key)
        if (existing.has(key)) dupCount += 1
        else newCount += 1
      }
      const uniqueCount = newCount + dupCount
      validKeys += uniqueCount
      valid.push({
        vendor_id: vendorID,
        vendor_name: block.vendor_name,
        count: uniqueCount,
        new_count: newCount,
        dup_count: dupCount,
        backup_dup_count: backupDupCount
      })
    }
    return { valid, skipped, totalKeys, validKeys }
  }, [importBackup, vendorRows, upstreamItems])

  const handleBackupFile = (file) => {
    setImportHint('')
    setImportReport(null)
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => {
      try {
        const parsed = JSON.parse(String(reader.result || ''))
        if (!Array.isArray(parsed?.vendors) || !parsed.vendors.length) {
          setImportHint('备份文件无效：未找到 vendors 字段或为空。')
          setImportBackup(null)
          return
        }
        setImportBackup(parsed)
      } catch (err) {
        setImportHint(`解析备份失败：${String(err?.message || err)}`)
        setImportBackup(null)
      }
    }
    reader.onerror = () => {
      setImportHint('读取文件失败，请重试。')
      setImportBackup(null)
    }
    reader.readAsText(file)
  }

  const submitImportBackup = async () => {
    if (!importBackup) {
      setImportHint('请先选择备份文件。')
      return
    }
    if (!importAnalysis?.valid.length) {
      setImportHint('备份中不存在可导入的供应商，请先在系统中创建对应供应商。')
      return
    }
    const skippedNames = importAnalysis.skipped.map((item) => item.vendor_name || item.vendor_id).join('、')
    const msg = importAnalysis.skipped.length
      ? `将导入 ${importAnalysis.valid.length} 个供应商，跳过 ${importAnalysis.skipped.length} 个不存在的供应商${skippedNames ? `（${skippedNames}）` : ''}。确认继续？`
      : `将导入 ${importAnalysis.valid.length} 个供应商、共 ${importAnalysis.validKeys} 条密钥。确认继续？`
    if (!window.confirm(msg)) return
    const report = await onImportBackup?.(importBackup)
    setImportReport(report || null)
    if (report?.ok) {
      setImportBackup(null)
      if (fileInputRef.current) fileInputRef.current.value = ''
    }
  }

  const canSubmit = tab === 'paste' ? newKeys.length > 0 : Boolean(importBackup && importAnalysis?.valid.length)

  return (
    <div className="modal-overlay animate-fade-in">
      <div className="modal-panel animate-slide-in max-w-3xl">
        <div className="modal-header">
          <div>
            <h3>添加密钥</h3>
            <p>{vendorName || '当前供应商'} · 粘贴新密钥或从备份文件恢复，均不会覆盖现有状态。</p>
          </div>
          <button className="modal-close" onClick={onClose}>✕</button>
        </div>

        <div className="kh-modal-tabs">
          {TABS.map((item) => (
            <button
              key={item.id}
              type="button"
              className={`kh-modal-tab ${tab === item.id ? 'kh-modal-tab-active' : ''}`}
              onClick={() => setTab(item.id)}
            >
              <Icon name={item.icon} size={13} />
              {item.label}
            </button>
          ))}
        </div>

        <div className="modal-body">
          {tab === 'paste' && (
            <>
              <textarea
                className="textarea-base min-h-[280px] w-full flex-1 font-mono text-[12px]"
                placeholder={'在此粘贴一行或多行密钥，例如：\nsk-1234\nsk-5678'}
                value={draftText}
                autoFocus
                onChange={(e) => {
                  setDraftText(e.target.value)
                  if (pasteHint) setPasteHint('')
                }}
              />
              <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-[var(--text-muted)]">
                <span>识别到 <strong className="font-mono text-[var(--text-secondary)]">{parsedKeys.length}</strong> 条有效密钥</span>
                <span>待新增 <strong className="font-mono text-[var(--success)]">{newKeys.length}</strong></span>
                <span>已存在 <strong className="font-mono text-[var(--text-secondary)]">{parsedKeys.length - newKeys.length}</strong></span>
              </div>
              {pasteHint && (
                <div className="rounded-lg border border-[rgba(245,158,11,0.3)] bg-[var(--warning-soft)] px-3 py-2 text-xs text-[var(--warning)]">
                  {pasteHint}
                </div>
              )}
            </>
          )}

          {tab === 'import' && (
            <>
              <input
                ref={fileInputRef}
                type="file"
                accept="application/json,.json"
                onChange={(e) => handleBackupFile(e.target.files?.[0])}
              />
              <p className="text-xs text-[var(--text-muted)]">
                从「全体导出」生成的 JSON 备份恢复。不存在的供应商会被跳过，不会自动创建。
              </p>
              {importHint && (
                <div className="rounded-lg border border-[rgba(239,68,68,0.3)] bg-[rgba(239,68,68,0.05)] px-3 py-2 text-xs text-[var(--danger)]">
                  {importHint}
                </div>
              )}
              {importAnalysis && (
                <div className="space-y-3">
                  <div className="rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] p-3 text-xs">
                    <div className="flex flex-wrap gap-x-6 gap-y-1">
                      <span>备份总计：<strong className="font-mono text-[var(--text-secondary)]">{importAnalysis.totalKeys}</strong> 条</span>
                      <span>可导入供应商：<strong className="font-mono text-[var(--success)]">{importAnalysis.valid.length}</strong></span>
                      <span className={importAnalysis.skipped.length ? 'text-[var(--warning)]' : ''}>跳过供应商：<strong className="font-mono">{importAnalysis.skipped.length}</strong></span>
                    </div>
                  </div>

                  {importAnalysis.valid.length > 0 && (
                    <div>
                      <div className="mb-1 text-xs font-medium text-[var(--text-secondary)]">可导入的供应商</div>
                      <div className="table-shell">
                        <table className="w-full text-xs">
                          <thead>
                            <tr>
                              <th className="!px-3 !py-2 text-left">供应商</th>
                              <th className="!px-3 !py-2 text-right">去重后</th>
                              <th className="!px-3 !py-2 text-right">新增</th>
                              <th className="!px-3 !py-2 text-right">已存在</th>
                              <th className="!px-3 !py-2 text-right">备份重复</th>
                            </tr>
                          </thead>
                          <tbody>
                            {importAnalysis.valid.map((row) => (
                              <tr key={row.vendor_id}>
                                <td className="!px-3 !py-2">
                                  <div className="font-medium text-[var(--text-primary)]">{row.vendor_name}</div>
                                  <div className="font-mono text-[10px] text-[var(--text-faint)]">{row.vendor_id}</div>
                                </td>
                                <td className="!px-3 !py-2 text-right font-mono">{row.count}</td>
                                <td className="!px-3 !py-2 text-right font-mono text-[var(--success)]">{row.new_count}</td>
                                <td className="!px-3 !py-2 text-right font-mono text-[var(--text-muted)]">{row.dup_count}</td>
                                <td className={`!px-3 !py-2 text-right font-mono ${row.backup_dup_count > 0 ? 'text-[var(--warning)]' : 'text-[var(--text-faint)]'}`}>{row.backup_dup_count}</td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    </div>
                  )}

                  {importAnalysis.skipped.length > 0 && (
                    <div>
                      <div className="mb-1 text-xs font-medium text-[var(--warning)]">将跳过的供应商（系统中不存在）</div>
                      <div className="table-shell">
                        <table className="w-full text-xs">
                          <thead>
                            <tr>
                              <th className="!px-3 !py-2 text-left">供应商</th>
                              <th className="!px-3 !py-2 text-right">跳过 key</th>
                              <th className="!px-3 !py-2 text-left">原因</th>
                            </tr>
                          </thead>
                          <tbody>
                            {importAnalysis.skipped.map((row) => (
                              <tr key={`${row.vendor_id}-skipped`} className="text-[var(--text-muted)]">
                                <td className="!px-3 !py-2">
                                  <div className="font-medium">{row.vendor_name}</div>
                                  <div className="font-mono text-[10px] text-[var(--text-faint)]">{row.vendor_id}</div>
                                </td>
                                <td className="!px-3 !py-2 text-right font-mono">{row.count}</td>
                                <td className="!px-3 !py-2 text-[var(--warning)]">{row.reason}</td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    </div>
                  )}
                </div>
              )}

              {importReport && (
                <div className={`rounded-lg border px-3 py-2 text-xs ${importReport.ok ? 'border-[rgba(34,197,94,0.3)] bg-[var(--success-soft)] text-[var(--success)]' : 'border-[rgba(239,68,68,0.3)] bg-[rgba(239,68,68,0.05)] text-[var(--danger)]'}`}>
                  <div className="font-medium">
                    {importReport.ok
                      ? `导入完成：新增 ${importReport.totalAdded} 条，跳过 ${importReport.totalSkipped} 条`
                      : `导入失败：${importReport.fatalError || '未知错误'}`}
                  </div>
                  {importReport.vendors?.length > 0 && (
                    <ul className="mt-1 list-inside list-disc space-y-0.5 text-[11px] opacity-90">
                      {importReport.vendors.map((row) => (
                        <li key={row.vendor_id}>
                          {row.vendor_id}：新增 {row.added} · 备注 {row.remarks} · 禁用 {row.disabled}
                          {row.failed ? ` · 失败: ${row.failed}` : ''}
                        </li>
                      ))}
                    </ul>
                  )}
                  {importReport.skippedVendors?.length > 0 && (
                    <div className="mt-1 text-[11px] opacity-80">
                      跳过的供应商：{importReport.skippedVendors.map((item) => item.vendor_name || item.vendor_id).join('、')}
                    </div>
                  )}
                </div>
              )}
            </>
          )}
        </div>

        <div className="modal-footer">
          <button className={buttonClass()} onClick={onClose}>关闭</button>
          {tab === 'paste' ? (
            <button className={buttonClass('primary')} disabled={busy || !canSubmit} onClick={submitAddKeys}>
              添加 {newKeys.length > 0 ? newKeys.length : ''} 条密钥
            </button>
          ) : (
            <button className={buttonClass('primary')} disabled={busy || !canSubmit} onClick={submitImportBackup}>
              确认导入
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
