import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Button,
  Descriptions,
  Dropdown,
  Empty,
  List,
  Modal,
  Spin,
  Tag,
  Toast,
  Typography,
} from '@douyinfe/semi-ui'
import { IconMore } from '@douyinfe/semi-icons'
import type { LibraryLink, LibrarySource, Relation, Revision, Work } from '../../types'
import { deleteWork, getWorkRelations, getWorkRevisions, pushGameAtlasWiki, pushKomgaSummary, restoreWorkRevision, unlinkEmbyLink, unlinkGameAtlas, unlinkKomgaLink } from '../../lib/api'
import { folderPath, workPath } from '../../lib/routes'
import { absoluteTime } from '../../lib/tree'
import { useAppStore } from '../../lib/store'
import LibraryLinkModal from '../library/LibraryLinkModal'

const { Text } = Typography

const AUTHOR_LABEL: Record<string, string> = {
  human: '我',
  llm: 'Altas',
  import: '导入',
}

const KIND_LABEL: Record<string, string> = {
  universe: '宇宙',
  series: '系列',
  work: '单作',
}

const LINK_SOURCE_LABEL: Record<string, string> = {
  gameatlas: 'GameAtlas',
  emby: 'Emby',
  komga: 'Komga',
}

const STATUS_LABEL: Record<string, string> = {
  stub: '待建档',
  draft: '草稿',
  ready: '已建档',
}

/**
 * 正文信息栏右侧的「…」：飞书母本里这里是克制的收纳位。
 * 版本历史 / 回滚 / 删除 / 节点信息全收进菜单，正文区不再挂开发者元数据。
 */
export default function DocActions({
  work,
  onReload,
  onDeleted,
  libraryLinks,
}: {
  work: Work
  onReload: () => Promise<void> | void
  onDeleted: () => void
  libraryLinks?: LibraryLink[]
}) {
  const nav = useNavigate()
  const [historyOpen, setHistoryOpen] = useState(false)
  const [infoOpen, setInfoOpen] = useState(false)
  const { nodes } = useAppStore()
  const [relations, setRelations] = useState<Relation[]>([])

  // 关系只在打开「更多」时拉：它是低频的结构信息，不值得每次进页面都请求。
  // 存在的意义是**让边看得见**——不然 Altas 建没建、建得对不对，用户在界面上无从核对。
  useEffect(() => {
    if (!infoOpen) return
    let cancelled = false
    void getWorkRelations(work.id)
      .then((r) => { if (!cancelled) setRelations(r.relations ?? []) })
      .catch(() => { if (!cancelled) setRelations([]) })
    return () => { cancelled = true }
  }, [infoOpen, work.id])
  const [revisions, setRevisions] = useState<Revision[]>([])
  const [loading, setLoading] = useState(false)
  const [restoring, setRestoring] = useState<string | null>(null)

  const workId = work.id

  const loadRevisions = useCallback(async () => {
    setLoading(true)
    try {
      const res = await getWorkRevisions(workId, 30)
      setRevisions(res.revisions ?? [])
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '读取版本失败')
      setRevisions([])
    } finally {
      setLoading(false)
    }
  }, [workId])

  const openHistory = () => {
    setHistoryOpen(true)
    void loadRevisions()
  }

  const restore = async (rev: Revision) => {
    setRestoring(rev.id)
    try {
      await restoreWorkRevision(workId, rev.id)
      Toast.success(`已回滚到 v${rev.version}（生成新版本）`)
      setHistoryOpen(false)
      await onReload()
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '回滚失败')
    } finally {
      setRestoring(null)
    }
  }

  const [pushing, setPushing] = useState(false)
  const [linkSource, setLinkSource] = useState<'gameatlas' | 'emby' | 'komga' | null>(null)
  const gaLink = (libraryLinks ?? []).find((l) => l.source === 'gameatlas')
  const komgaLink = (libraryLinks ?? []).find((l) => l.source === 'komga')

  // 反哺：把已保存的正文（服务端自动带简介）写回 GameAtlas 条目
  const pushToGameAtlas = async () => {
    setPushing(true)
    try {
      const res = await pushGameAtlasWiki(workId)
      const intro = res.summary
        ? `（简介：${res.summary.length > 24 ? `${res.summary.slice(0, 24)}…` : res.summary}）`
        : ''
      Toast.success(`已反哺到 GameAtlas${intro}`)
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '反哺失败')
    } finally {
      setPushing(false)
    }
  }

  const removeGaLink = () => {
    Modal.confirm({
      title: '解除 GameAtlas 关联？',
      content: '解除后这个节点不能再反哺；正文与版本历史不受影响。',
      okText: '解除',
      onOk: async () => {
        try {
          await unlinkGameAtlas(workId)
          Toast.success('已解除 GameAtlas 关联')
          await onReload()
        } catch (e) {
          Toast.error(e instanceof Error ? e.message : '解除失败')
        }
      },
    })
  }

  // 反哺到 Komga：把简介 PATCH 到该节点所有 Komga 链的元数据（系列/单册）
  const pushToKomga = async () => {
    setPushing(true)
    try {
      const res = await pushKomgaSummary(workId)
      const parts: string[] = []
      if (res.count) parts.push(`${res.count} 个条目`)
      if (res.summary) {
        parts.push(`简介：${res.summary.length > 24 ? `${res.summary.slice(0, 24)}…` : res.summary}`)
      }
      Toast.success(`已反哺到 Komga${parts.length ? `（${parts.join('；')}）` : ''}`)
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '反哺失败')
    } finally {
      setPushing(false)
    }
  }

  const removeLink = (source: LibrarySource, linkId: string) => {
    Modal.confirm({
      title: `解除这条 ${LINK_SOURCE_LABEL[source] ?? source} 关联？`,
      content: '只解除外链，正文与版本历史不受影响。',
      okText: '解除',
      onOk: async () => {
        try {
          if (source === 'emby') {
            await unlinkEmbyLink(linkId)
          } else {
            await unlinkKomgaLink(linkId)
          }
          Toast.success('已解除关联')
          await onReload()
        } catch (e) {
          Toast.error(e instanceof Error ? e.message : '解除失败')
        }
      },
    })
  }

  const confirmDelete = () => {
    Modal.confirm({
      title: `删除「${work.title}」？`,
      content: '正文与版本历史一并移除，无法撤销。若存在子节点会被拒绝。',
      okType: 'danger',
      okText: '删除',
      onOk: async () => {
        try {
          await deleteWork(workId)
          Toast.success('已删除')
          onDeleted()
        } catch (e) {
          Toast.error(e instanceof Error ? e.message : '删除失败')
        }
      },
    })
  }

  return (
    <>
      <Dropdown
        trigger="click"
        position="bottomRight"
        render={
          <Dropdown.Menu>
            <Dropdown.Item onClick={() => nav(folderPath(workId))}>打开资料夹</Dropdown.Item>
            {gaLink ? (
              <>
                <Dropdown.Item disabled={pushing} onClick={() => void pushToGameAtlas()}>
                  {pushing ? '反哺中…' : '反哺到 GameAtlas'}
                </Dropdown.Item>
                <Dropdown.Item onClick={removeGaLink}>解除 GameAtlas 关联</Dropdown.Item>
              </>
            ) : (
              <Dropdown.Item onClick={() => setLinkSource('gameatlas')}>关联到 GameAtlas…</Dropdown.Item>
            )}
            <Dropdown.Item onClick={() => setLinkSource('emby')}>关联到 Emby…</Dropdown.Item>
            {komgaLink && (
              <Dropdown.Item disabled={pushing} onClick={() => void pushToKomga()}>
                {pushing ? '反哺中…' : '反哺到 Komga'}
              </Dropdown.Item>
            )}
            <Dropdown.Item onClick={() => setLinkSource('komga')}>关联到 Komga…</Dropdown.Item>
            <Dropdown.Item onClick={openHistory}>版本历史</Dropdown.Item>
            <Dropdown.Item
              disabled={revisions.length < 2}
              onClick={() => {
                if (revisions.length >= 2) void restore(revisions[1])
              }}
            >
              回滚到上一版
            </Dropdown.Item>
            <Dropdown.Divider />
            <Dropdown.Item onClick={() => setInfoOpen(true)}>节点信息</Dropdown.Item>
            <Dropdown.Item type="danger" onClick={confirmDelete}>
              删除
            </Dropdown.Item>
          </Dropdown.Menu>
        }
      >
        <Button
          theme="borderless"
          type="tertiary"
          size="small"
          icon={<IconMore />}
          aria-label="更多操作"
        />
      </Dropdown>

      <Modal
        title="版本历史"
        visible={historyOpen}
        onCancel={() => setHistoryOpen(false)}
        footer={null}
        width={560}
      >
        {loading ? (
          <Spin style={{ display: 'block', margin: '24px auto' }} />
        ) : revisions.length === 0 ? (
          <Empty description="还没有版本记录" style={{ padding: 24 }} />
        ) : (
          <List<Revision>
            dataSource={revisions}
            split={false}
            renderItem={(rev) => (
              <List.Item key={rev.id} className="revision-row">
                <div className="revision-main">
                  <span className="revision-version">v{rev.version}</span>
                  <Text strong>{rev.summary || '无摘要'}</Text>
                  <Text type="tertiary" size="small">
                    {AUTHOR_LABEL[rev.author] ?? rev.author} · {absoluteTime(rev.createdAt)}
                  </Text>
                </div>
                <Tag size="small" color={rev.author === 'llm' ? 'purple' : 'blue'}>
                  {AUTHOR_LABEL[rev.author] ?? rev.author}
                </Tag>
                <Button
                  size="small"
                  theme="borderless"
                  type="tertiary"
                  loading={restoring === rev.id}
                  onClick={() => void restore(rev)}
                >
                  恢复
                </Button>
              </List.Item>
            )}
          />
        )}
      </Modal>

      <LibraryLinkModal
        work={work}
        source={linkSource ?? 'gameatlas'}
        visible={linkSource !== null}
        onClose={() => setLinkSource(null)}
        onLinksChanged={() => void onReload()}
      />

      <Modal
        title="节点信息"
        visible={infoOpen}
        onCancel={() => setInfoOpen(false)}
        footer={null}
        width={520}
      >
        <Descriptions
          data={[
            { key: '标题', value: work.title },
            { key: '层级', value: KIND_LABEL[work.kind] ?? work.kind },
            { key: '介质', value: work.medium ?? '—' },
            { key: '状态', value: STATUS_LABEL[work.status] ?? work.status },
            { key: '可见性', value: work.visibility === 'public' ? '公开' : '私有' },
            { key: '版本', value: `v${work.contentVer}` },
            {
              key: '关系',
              value: relations.length ? (
                <span>
                  {relations.map((r, i) => {
                    // 箭头表示方向：本作是 from 就是「本作 → 对方」。
                    // 词表的语义是"衍生作 → 来源作"，方向反了整句就反了，所以必须标出来。
                    const outgoing = r.fromId === work.id
                    const otherId = outgoing ? r.toId : r.fromId
                    const title = nodes.find((n) => n.id === otherId)?.title ?? otherId.slice(0, 8)
                    return (
                      <span key={r.id}>
                        {i > 0 && <br />}
                        {outgoing ? '→ ' : '← '}
                        <a onClick={() => nav(workPath(otherId))}>{title}</a>
                        <Text type="tertiary" size="small">（{r.type}）</Text>
                      </span>
                    )
                  })}
                </span>
              ) : (
                '—'
              ),
            },
            {
              key: '库外链',
              value: (libraryLinks ?? []).length ? (
                <span>
                  {(libraryLinks ?? []).map((l) => (
                    <div key={l.id} className="link-row">
                      {l.url ? (
                        <a href={l.url} target="_blank" rel="noreferrer">
                          {LINK_SOURCE_LABEL[l.source] ?? l.source} · {l.titleHint ?? l.externalId.slice(0, 8)}
                        </a>
                      ) : (
                        <span>
                          {LINK_SOURCE_LABEL[l.source] ?? l.source} · {l.titleHint ?? l.externalId.slice(0, 8)}
                        </span>
                      )}
                      {(l.source === 'emby' || l.source === 'komga') && (
                        <Button
                          size="small"
                          theme="borderless"
                          type="danger"
                          style={{ marginLeft: 6 }}
                          onClick={() => removeLink(l.source, l.id)}
                        >
                          解除
                        </Button>
                      )}
                    </div>
                  ))}
                </span>
              ) : (
                '—'
              ),
            },
            { key: 'ID', value: work.id },
            { key: '创建', value: absoluteTime(work.createdAt) },
            { key: '更新', value: absoluteTime(work.updatedAt) },
          ]}
        />
      </Modal>
    </>
  )
}
