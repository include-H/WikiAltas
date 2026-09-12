import { useCallback, useEffect, useRef, useState } from 'react'
import { Button, Tag, Toast, Typography } from '@douyinfe/semi-ui'
import type { LibrarySuggestion, Work } from '../../types'
import { archiveLibrary, suggestLibrary } from '../../lib/api'
import type { LibraryApiSource } from '../../lib/api'
import { useAppStore } from '../../lib/store'
import { FORMAT_LABEL, KIND_LABEL, SOURCE_LABEL } from '../../lib/mediaTags'

const { Text } = Typography

/**
 * 集合页（系列/宇宙）的「建议建档」面板：拉出库里属于本集合、还没挂链的条目，
 * 一键建 stub 子节点 + 挂链。GameAtlas 按系列名匹配；Emby 按同名 Series /
 * 同名 BoxSet（其成员电影）匹配。未配置 / 拉不到 / 没有建议时静默不渲染。
 */
export default function LibrarySuggest({ work, source }: { work: Work; source: LibraryApiSource }) {
  const { me, refreshTree } = useAppStore()
  const [items, setItems] = useState<LibrarySuggestion[]>([])
  const [archiving, setArchiving] = useState<string | null>(null)

  const isCollection = work.kind === 'series' || work.kind === 'universe'
  const label = SOURCE_LABEL[source]

  // 请求序号：晚到的旧响应不许盖掉新状态（effect 双跑 / 建档后的刷新都会叠请求）
  const seqRef = useRef(0)
  const load = useCallback(async () => {
    const seq = ++seqRef.current
    try {
      const res = await suggestLibrary(source, work.id)
      if (seq !== seqRef.current) return
      setItems(res.suggestions ?? [])
    } catch {
      if (seq !== seqRef.current) return
      setItems([])
    }
  }, [source, work.id])

  useEffect(() => {
    if (!me.authed || !isCollection) return
    void load()
  }, [load, me.authed, isCollection])

  if (!me.authed || !isCollection || items.length === 0) return null

  const archive = async (item: LibrarySuggestion) => {
    setArchiving(item.publicId)
    try {
      const res = await archiveLibrary(source, work.id, item.publicId)
      Toast.success(`已建档「${res.work?.title ?? item.title}」`)
      setItems((prev) => prev.filter((i) => i.publicId !== item.publicId))
      void load()
      await refreshTree()
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '建档失败')
    } finally {
      setArchiving(null)
    }
  }

  return (
    <div className="ga-suggest">
      <div className="ga-suggest-head">
        <Text strong>{label} 建议建档</Text>
        <Text type="tertiary" size="small">
          「{work.title}」在 {label} 里还有 {items.length} 条没建档
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
                {item.titleAlt ? `${item.titleAlt} · ` : ''}
                {item.releaseDate ? String(item.releaseDate).slice(0, 4) : '日期未知'}
              </Text>
            </span>
            <a className="ga-suggest-link" href={item.url} target="_blank" rel="noreferrer">
              打开
            </a>
            <Button
              size="small"
              theme="solid"
              type="primary"
              loading={archiving === item.publicId}
              onClick={() => void archive(item)}
            >
              建档
            </Button>
          </div>
        ))}
      </div>
    </div>
  )
}
