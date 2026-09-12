import { useCallback, useEffect, useState } from 'react'
import { Button, Toast, Typography } from '@douyinfe/semi-ui'
import type { GameAtlasSuggestion, Work } from '../../types'
import { archiveGameAtlasEntry, suggestGameAtlas } from '../../lib/api'
import { useAppStore } from '../../lib/store'

const { Text } = Typography

/**
 * 集合页（系列/宇宙）的「GameAtlas 建议建档」面板：
 * 拉出 GameAtlas 里属于本系列、还没建档的条目，一键建 stub 子节点 + 挂链。
 * 未配置 / 拉不到 / 没有建议时都静默不渲染——不打扰。
 */
export default function GameAtlasSuggest({ work }: { work: Work }) {
  const { me, refreshTree } = useAppStore()
  const [items, setItems] = useState<GameAtlasSuggestion[]>([])
  const [gaUrl, setGaUrl] = useState('')
  const [archiving, setArchiving] = useState<string | null>(null)

  const isCollection = work.kind === 'series' || work.kind === 'universe'

  const load = useCallback(async () => {
    try {
      const res = await suggestGameAtlas(work.id)
      setItems(res.suggestions ?? [])
      setGaUrl(res.gameatlasUrl ?? '')
    } catch {
      setItems([])
    }
  }, [work.id])

  useEffect(() => {
    if (!me.authed || !isCollection) return
    void load()
  }, [load, me.authed, isCollection])

  if (!me.authed || !isCollection || items.length === 0) return null

  const archive = async (item: GameAtlasSuggestion) => {
    setArchiving(item.publicId)
    try {
      const res = await archiveGameAtlasEntry(work.id, item.publicId)
      Toast.success(`已建档「${res.work?.title ?? item.title}」`)
      setItems((prev) => prev.filter((i) => i.publicId !== item.publicId))
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
        <Text strong>GameAtlas 建议建档</Text>
        <Text type="tertiary" size="small">
          「{work.title}」系列在 GameAtlas 里还有 {items.length} 条没建档
        </Text>
      </div>
      <div className="ga-suggest-list">
        {items.map((item) => (
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
                {item.titleAlt ? `${item.titleAlt} · ` : ''}
                {item.releaseDate ? String(item.releaseDate).slice(0, 4) : '日期未知'}
              </Text>
            </span>
            <a className="ga-suggest-link" href={item.url} target="_blank" rel="noreferrer">
              在 GameAtlas 打开
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
