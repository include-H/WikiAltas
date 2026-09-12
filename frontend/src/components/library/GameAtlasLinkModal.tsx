import { useCallback, useEffect, useState } from 'react'
import { Button, Empty, Input, Modal, Spin, Tag, Toast, Typography } from '@douyinfe/semi-ui'
import { IconSearch } from '@douyinfe/semi-icons'
import type { GameAtlasSearchEntry, Work } from '../../types'
import { linkGameAtlas, searchGameAtlas } from '../../lib/api'

const { Text } = Typography

/**
 * 「关联到 GameAtlas」弹窗：给已有节点挂上一条 GameAtlas 条目。
 * 打开时用节点标题预搜（防抖 300ms 自动搜）；行内直接关联，
 * 已挂到别处的条目标出来、不给点。
 */
export default function GameAtlasLinkModal({
  work,
  visible,
  onClose,
  onLinked,
}: {
  work: Work
  visible: boolean
  onClose: () => void
  onLinked: () => void
}) {
  const [q, setQ] = useState('')
  const [entries, setEntries] = useState<GameAtlasSearchEntry[]>([])
  const [gaUrl, setGaUrl] = useState('')
  const [searching, setSearching] = useState(false)
  const [linking, setLinking] = useState<string | null>(null)

  const run = useCallback(async (query: string) => {
    setSearching(true)
    try {
      const res = await searchGameAtlas(query)
      setEntries(res.entries ?? [])
      setGaUrl(res.gameatlasUrl ?? '')
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '搜索失败')
      setEntries([])
    } finally {
      setSearching(false)
    }
  }, [])

  useEffect(() => {
    if (!visible) return
    setQ(work.title)
  }, [visible, work.title])

  useEffect(() => {
    if (!visible) return
    const timer = setTimeout(() => {
      void run(q)
    }, 300)
    return () => clearTimeout(timer)
  }, [q, visible, run])

  const link = async (item: GameAtlasSearchEntry) => {
    setLinking(item.publicId)
    try {
      await linkGameAtlas(work.id, item.publicId)
      Toast.success(`已关联「${item.title}」`)
      onLinked()
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '关联失败')
    } finally {
      setLinking(null)
    }
  }

  return (
    <Modal title="关联到 GameAtlas" visible={visible} onCancel={onClose} footer={null} width={640}>
      <Input
        value={q}
        onChange={setQ}
        prefix={<IconSearch />}
        placeholder="搜 GameAtlas 条目（标题 / 别名 / 系列名）"
        showClear
      />
      <div className="ga-link-list">
        {searching && entries.length === 0 ? (
          <Spin style={{ display: 'block', margin: '24px auto' }} />
        ) : entries.length === 0 ? (
          <Empty description="没有搜到条目" style={{ padding: 24 }} />
        ) : (
          entries.map((item) => (
            <div className="ga-suggest-row" key={item.publicId}>
              <span className="ga-suggest-cover">
                {item.coverImage ? (
                  <img src={`${gaUrl}${item.coverImage}`} alt="" loading="lazy" />
                ) : null}
              </span>
              <span className="ga-suggest-meta">
                <span className="ga-suggest-title" title={item.title}>
                  {item.title}
                </span>
                <Text type="tertiary" size="small">
                  {item.series ? `${item.series.name} · ` : ''}
                  {item.releaseDate ? String(item.releaseDate).slice(0, 4) : '日期未知'}
                </Text>
              </span>
              <a className="ga-suggest-link" href={item.url} target="_blank" rel="noreferrer">
                打开
              </a>
              {item.linked ? (
                <Tag size="small" color={item.linkedWorkId === work.id ? 'green' : 'grey'}>
                  {item.linkedWorkId === work.id ? '已关联本节点' : '已关联别处'}
                </Tag>
              ) : (
                <Button
                  size="small"
                  theme="solid"
                  type="primary"
                  loading={linking === item.publicId}
                  onClick={() => void link(item)}
                >
                  关联
                </Button>
              )}
            </div>
          ))
        )}
      </div>
    </Modal>
  )
}
