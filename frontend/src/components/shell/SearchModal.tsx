import { useCallback, useEffect, useMemo, useState } from 'react'
import { Empty, Input, Modal, Spin, Tag, Typography } from '@douyinfe/semi-ui'
import { IconSearch } from '@douyinfe/semi-icons'
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
  /** 键盘选择的光标位置（↑↓ 移动，Enter 打开） */
  const [active, setActive] = useState(0)
  const nav = useNavigate()

  useEffect(() => {
    if (!open) {
      setQ('')
      setHits([])
      setError(null)
      setActive(0)
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
        setActive(0)
      } catch (e) {
        setError(e instanceof Error ? e.message : '搜索失败')
        setHits([])
      } finally {
        setLoading(false)
      }
    }, 250)
    return () => clearTimeout(t)
  }, [q, open])

  const go = useCallback(
    (hit: SearchHit) => {
      onClose()
      // 只用 UUID 定位：资料命中不一定知道父作品
      if (hit.kind === 'work') nav(workPath(hit.id))
      else nav(docPath(UNKNOWN_WORK_ID, hit.id))
    },
    [nav, onClose],
  )

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setActive((i) => Math.min(i + 1, Math.max(hits.length - 1, 0)))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActive((i) => Math.max(i - 1, 0))
    } else if (e.key === 'Enter' && hits[active]) {
      e.preventDefault()
      go(hits[active])
    }
  }

  const body = useMemo(() => {
    if (loading) return <Spin style={{ display: 'block', margin: '24px auto' }} />
    if (error) return <Empty description={error} style={{ padding: 24 }} />
    if (!q.trim()) {
      return (
        <Empty
          image={<IconSearch size="extra-large" />}
          description="输入关键词搜索作品或资料"
          style={{ padding: 24 }}
        />
      )
    }
    if (hits.length === 0) return <Empty description="没有匹配结果" style={{ padding: 24 }} />
    return (
      <div className="search-hits">
        {hits.map((h, i) => (
          <div
            key={`${h.kind}-${h.id}`}
            role="button"
            tabIndex={0}
            className={`search-hit${i === active ? ' is-active' : ''}`}
            onMouseEnter={() => setActive(i)}
            onClick={() => go(h)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') go(h)
            }}
          >
            <div className="search-hit-row">
              <Tag color={h.kind === 'work' ? 'blue' : 'green'} size="small">
                {h.kind === 'work' ? '作品' : '资料'}
              </Tag>
              <Text strong>{h.title}</Text>
            </div>
            {h.snippet ? (
              <Text type="tertiary" size="small" className="search-hit-snippet">
                {h.snippet}
              </Text>
            ) : null}
          </div>
        ))}
      </div>
    )
  }, [loading, error, q, hits, active, go])

  return (
    <Modal
      className="search-modal"
      title="搜索"
      visible={open}
      onCancel={onClose}
      footer={null}
      width={560}
      style={{ top: 80 }}
    >
      <Input
        size="large"
        placeholder="搜索作品、资料…"
        value={q}
        onChange={setQ}
        showClear
        autoFocus
        prefix={<IconSearch />}
        onKeyDown={onKeyDown}
      />
      <div className="search-body">{body}</div>
      {hits.length > 0 && (
        <div className="search-foot">
          <Text type="tertiary" size="small">
            共 {hits.length} 条 · ↑↓ 选择 · Enter 打开
          </Text>
        </div>
      )}
    </Modal>
  )
}
