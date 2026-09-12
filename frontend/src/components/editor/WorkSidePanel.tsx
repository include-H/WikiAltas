import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Button, Tag, Typography } from '@douyinfe/semi-ui'
import { IconChevronLeft, IconChevronRight } from '@douyinfe/semi-icons'
import type { LibraryLink, Relation, Work } from '../../types'
import { getWorkRelations } from '../../lib/api'
import { useAppStore } from '../../lib/store'
import { SOURCE_LABEL } from '../../lib/mediaTags'
import { workPath } from '../../lib/routes'
import LibraryDiscovery from '../library/LibraryDiscovery'
import LibrarySuggest from '../library/LibrarySuggest'
import LibraryRelated from '../library/LibraryRelated'

const { Text } = Typography

const REL_LABEL: Record<string, string> = {
  adaptation_of: '改编',
  sequel_to: '续作',
  spin_off_of: '衍生',
  remake_of: '重制',
  expansion_of: '扩展',
  references: '引用',
  same_series: '同系列',
}

const SIDE_KEY = 'wikiatlas.workSide'

/**
 * 作品页右侧栏「关联与媒体」：把结构信息（作品关系 / 库外链）和媒体建议
 * （媒体库发现 / 建议建档 / 相关候选）收在一栏里——正文列保持干净。
 * 收起状态记本地；收起后右缘留一个竖向拉手。
 */
export default function WorkSidePanel({
  work,
  links,
  onLinked,
}: {
  work: Work
  links: LibraryLink[]
  onLinked: () => void
}) {
  const nav = useNavigate()
  const { nodes } = useAppStore()
  const [open, setOpen] = useState(() => localStorage.getItem(SIDE_KEY) !== '0')
  const [relations, setRelations] = useState<Relation[]>([])

  const toggle = () => {
    setOpen((v) => {
      localStorage.setItem(SIDE_KEY, v ? '0' : '1')
      return !v
    })
  }

  const loadRelations = useCallback(() => {
    void getWorkRelations(work.id)
      .then((r) => setRelations(r.relations ?? []))
      .catch(() => setRelations([]))
  }, [work.id])

  useEffect(() => {
    if (open) loadRelations()
  }, [open, loadRelations])

  if (!open) {
    return (
      <button className="work-side-tab" onClick={toggle} title="展开「关联与媒体」">
        <IconChevronLeft size="small" />
        关联
      </button>
    )
  }

  return (
    <aside className="work-side">
      <div className="work-side-head">
        <Text strong>关联与媒体</Text>
        <Button
          size="small"
          theme="borderless"
          type="tertiary"
          icon={<IconChevronRight />}
          onClick={toggle}
          aria-label="收起侧栏"
        />
      </div>
      <div className="work-side-body">
        <div className="work-side-section">
          <Text type="tertiary" size="small" className="work-side-label">
            作品关系
          </Text>
          {relations.length === 0 ? (
            <Text type="tertiary" size="small">
              还没有与其他作品建立关系
            </Text>
          ) : (
            relations.map((r) => {
              // 箭头表示方向：本作是 from 就是「本作 → 对方」（与节点信息的约定一致）
              const outgoing = r.fromId === work.id
              const otherId = outgoing ? r.toId : r.fromId
              const title = nodes.find((n) => n.id === otherId)?.title ?? otherId.slice(0, 8)
              return (
                <div className="work-side-row" key={r.id}>
                  <a className="work-side-link" onClick={() => nav(workPath(otherId))} title={title}>
                    <span className="work-side-arrow">{outgoing ? '→' : '←'}</span>
                    {title}
                  </a>
                  <Tag size="small" style={{ flex: 'none' }}>
                    {REL_LABEL[r.type] ?? r.type}
                  </Tag>
                </div>
              )
            })
          )}
        </div>

        <div className="work-side-section">
          <Text type="tertiary" size="small" className="work-side-label">
            库外链
          </Text>
          {links.length === 0 ? (
            <Text type="tertiary" size="small">
              还没有关联的库条目
            </Text>
          ) : (
            links.map((l) => (
              <div className="work-side-row" key={l.id}>
                {l.coverImage ? (
                  <span className="work-side-cover">
                    <img src={l.coverImage} alt="" loading="lazy" />
                  </span>
                ) : null}
                <a
                  className="work-side-link"
                  href={l.url ?? undefined}
                  target="_blank"
                  rel="noreferrer"
                  title={l.titleHint ?? l.externalId}
                >
                  {l.titleHint ?? l.externalId.slice(0, 12)}
                </a>
                <Tag size="small" style={{ flex: 'none' }}>
                  {SOURCE_LABEL[l.source] ?? l.source}
                </Tag>
              </div>
            ))
          )}
        </div>

        <LibraryDiscovery work={work} onLinked={onLinked} />
        <LibrarySuggest work={work} source="gameatlas" />
        <LibrarySuggest work={work} source="emby" />
        <LibrarySuggest work={work} source="komga" />
        <LibraryRelated work={work} />
      </div>
    </aside>
  )
}
