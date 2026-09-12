import { useCallback, useEffect, useState } from 'react'
import { Button, Empty, Input, Modal, Spin, Tag, Toast, Typography } from '@douyinfe/semi-ui'
import { IconSearch } from '@douyinfe/semi-icons'
import type { LibrarySearchEntry, Work } from '../../types'
import { linkLibrary, searchLibrary } from '../../lib/api'
import type { LibraryApiSource } from '../../lib/api'
import { FORMAT_LABEL, KIND_LABEL, SOURCE_LABEL } from '../../lib/mediaTags'

const { Text } = Typography

/**
 * 「关联到 XX」弹窗：给节点挂上一条库条目。用节点标题预搜（防抖 300ms 自动搜）。
 * GameAtlas 一条即满（关联后关窗）；Emby 不限条数（关联后刷新状态、继续挑）。
 */
export default function LibraryLinkModal({
  work,
  source,
  visible,
  onClose,
  onLinksChanged,
}: {
  work: Work
  source: LibraryApiSource
  visible: boolean
  onClose: () => void
  onLinksChanged: () => void
}) {
  const [q, setQ] = useState('')
  const [entries, setEntries] = useState<LibrarySearchEntry[]>([])
  const [searching, setSearching] = useState(false)
  const [linking, setLinking] = useState<string | null>(null)
  const label = SOURCE_LABEL[source]

  const run = useCallback(
    async (query: string) => {
      setSearching(true)
      try {
        const res = await searchLibrary(source, query)
        setEntries(res.entries ?? [])
      } catch (e) {
        Toast.error(e instanceof Error ? e.message : '搜索失败')
        setEntries([])
      } finally {
        setSearching(false)
      }
    },
    [source],
  )

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

  const link = async (item: LibrarySearchEntry) => {
    setLinking(item.publicId)
    try {
      await linkLibrary(source, work.id, item.publicId)
      Toast.success(`已关联「${item.title}」`)
      onLinksChanged()
      if (source === 'gameatlas') {
        onClose()
      } else {
        // Emby 可以继续挑：刷新挂链状态，窗口留着
        void run(q)
      }
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '关联失败')
    } finally {
      setLinking(null)
    }
  }

  return (
    <Modal title={`关联到 ${label}`} visible={visible} onCancel={onClose} footer={null} width={640}>
      <Input
        value={q}
        onChange={setQ}
        prefix={<IconSearch />}
        placeholder={
          source === 'emby'
            ? '搜 Emby 条目（剧集 / 电影 / 专辑）'
            : source === 'komga'
              ? '搜 Komga 条目（系列 / 单册）'
              : '搜 GameAtlas 条目（标题 / 别名 / 系列名）'
        }
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
                {item.coverImage ? <img src={item.coverImage} alt="" loading="lazy" /> : null}
              </span>
              <span className="ga-suggest-meta">
                <span className="ga-suggest-title" title={item.title}>
                  {item.title}
                </span>
                <Text type="tertiary" size="small">
                  {item.format ? (
                    <Tag size="small" color="cyan" style={{ marginRight: 6 }}>
                      {FORMAT_LABEL[item.format] ?? item.format}
                    </Tag>
                  ) : null}
                  {item.kind ? (
                    <Tag size="small" color="blue" style={{ marginRight: 6 }}>
                      {KIND_LABEL[item.kind] ?? item.kind}
                    </Tag>
                  ) : null}
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
