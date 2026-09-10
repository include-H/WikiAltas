import { useEffect, useMemo, useState } from 'react'
import { Modal, Input, Empty, Spin, Tag, Typography } from '@douyinfe/semi-ui'
import { useNavigate } from 'react-router-dom'
import { search } from '../../lib/api'
import type { SearchHit } from '../../types'
import { UNKNOWN_WORK_ID, docPath, workPath } from '../../lib/routes'

const { Text } = Typography

export default function SearchModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [q, setQ] = useState('')
  const [hits, setHits] = useState<SearchHit[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const nav = useNavigate()

  useEffect(() => {
    if (!open) {
      setQ('')
      setHits([])
      setError(null)
    }
  }, [open])

  useEffect(() => {
    if (!open || !q.trim()) {
      setHits([])
      return
    }
    const t = setTimeout(async () => {
      setLoading(true)
      setError(null)
      try {
        const res = await search(q.trim())
        setHits(res.hits ?? [])
      } catch (e) {
        setError(e instanceof Error ? e.message : '搜索失败')
        setHits([])
      } finally {
        setLoading(false)
      }
    }, 250)
    return () => clearTimeout(t)
  }, [q, open])

  const body = useMemo(() => {
    if (loading) return <Spin style={{ display: 'block', margin: '24px auto' }} />
    if (error) return <Empty description={error} style={{ padding: 24 }} />
    if (!q.trim()) return <Empty description="输入关键词搜索作品或资料" style={{ padding: 24 }} />
    if (hits.length === 0) return <Empty description="没有匹配结果" style={{ padding: 24 }} />
    return (
      <div className="search-hits">
        {hits.map((h) => (
          <div
            key={`${h.kind}-${h.id}`}
            role="button"
            tabIndex={0}
            className="doc-row search-hit"
            onClick={() => {
              onClose()
              // Resolve by UUID only; doc hits may not know the parent work
              if (h.kind === 'work') nav(workPath(h.id, h.slug))
              else nav(docPath(UNKNOWN_WORK_ID, h.id))
            }}
            onKeyDown={(e) => {
              if (e.key !== 'Enter') return
              onClose()
              if (h.kind === 'work') nav(workPath(h.id, h.slug))
              else nav(docPath(UNKNOWN_WORK_ID, h.id))
            }}
          >
            <div className="search-hit-row">
              <Tag color={h.kind === 'work' ? 'blue' : 'green'} size="small">
                {h.kind === 'work' ? '作品' : '资料'}
              </Tag>
              <Text strong>{h.title}</Text>
            </div>
            {h.snippet ? <Text type="tertiary" size="small">{h.snippet}</Text> : null}
          </div>
        ))}
      </div>
    )
  }, [loading, error, q, hits, onClose, nav])

  return (
    <Modal
      title="搜索"
      visible={open}
      onCancel={onClose}
      footer={null}
      width={520}
      style={{ top: 80 }}
    >
      <Input
        placeholder="搜索作品、资料…"
        value={q}
        onChange={setQ}
        showClear
        autoFocus
        prefix={<span style={{ opacity: 0.5 }}>⌕</span>}
      />
      <div style={{ marginTop: 12, maxHeight: 420, overflow: 'auto' }}>{body}</div>
    </Modal>
  )
}
