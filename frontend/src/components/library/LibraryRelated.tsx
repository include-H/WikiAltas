import { useCallback, useEffect, useRef, useState } from 'react'
import { Button, Tag, Toast, Typography } from '@douyinfe/semi-ui'
import type { LibrarySearchEntry, Work } from '../../types'
import { linkLibrary, relatedEmby } from '../../lib/api'
import { useAppStore } from '../../lib/store'

const { Text } = Typography

const KIND_LABEL: Record<string, string> = {
  movie: '单集',
  series: '连载',
  album: '专辑',
}

/**
 * 节点页的「Emby 影像 / OST」候选（混杂库）：标题或目录命中本节点名的条目，
 * 一条条点关联——一个节点可以挂多条。没有候选时静默不渲染。
 */
export default function LibraryRelated({ work }: { work: Work }) {
  const { me } = useAppStore()
  const [items, setItems] = useState<LibrarySearchEntry[]>([])
  const [linking, setLinking] = useState<string | null>(null)

  // 请求序号：晚到的旧响应不许盖掉新状态（effect 双跑时的竞态）
  const seqRef = useRef(0)
  const load = useCallback(async () => {
    const seq = ++seqRef.current
    try {
      const res = await relatedEmby(work.id)
      if (seq !== seqRef.current) return
      setItems(res.related ?? [])
    } catch {
      if (seq !== seqRef.current) return
      setItems([])
    }
  }, [work.id])

  useEffect(() => {
    if (!me.authed || work.kind === 'universe') return
    void load()
  }, [load, me.authed, work.kind])

  if (!me.authed || work.kind === 'universe' || items.length === 0) return null

  const link = async (item: LibrarySearchEntry) => {
    setLinking(item.publicId)
    try {
      await linkLibrary('emby', work.id, item.publicId)
      Toast.success(`已关联「${item.title}」`)
      setItems((prev) =>
        prev.map((i) => (i.publicId === item.publicId ? { ...i, linked: true, linkedWorkId: work.id } : i)),
      )
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '关联失败')
    } finally {
      setLinking(null)
    }
  }

  const remaining = items.filter((i) => !i.linked || i.linkedWorkId !== work.id).length

  return (
    <div className="ga-suggest">
      <div className="ga-suggest-head">
        <Text strong>Emby 影像 / OST</Text>
        <Text type="tertiary" size="small">
          与「{work.title}」相关的条目{remaining > 0 ? `（${remaining} 条未关联）` : '（都已关联）'}
        </Text>
      </div>
      <div className="ga-suggest-list">
        {items.map((item) => (
          <div className="ga-suggest-row" key={item.publicId}>
            <span className="ga-suggest-cover">
              {item.coverImage ? <img src={item.coverImage} alt="" loading="lazy" /> : null}
            </span>
            <span className="ga-suggest-meta">
              <span className="ga-suggest-title" title={item.title}>
                {item.title}
              </span>
              <Text type="tertiary" size="small">
                {item.kind ? (
                  <Tag size="small" color="blue" style={{ marginRight: 6 }}>
                    {KIND_LABEL[item.kind] ?? item.kind}
                  </Tag>
                ) : null}
                {item.releaseDate ? String(item.releaseDate).slice(0, 4) : '日期未知'}
              </Text>
            </span>
            <a className="ga-suggest-link" href={item.url} target="_blank" rel="noreferrer">
              打开
            </a>
            {item.linked ? (
              <Tag size="small" color={item.linkedWorkId === work.id ? 'green' : 'grey'}>
                {item.linkedWorkId === work.id ? '已关联' : '已关联别处'}
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
        ))}
      </div>
    </div>
  )
}
