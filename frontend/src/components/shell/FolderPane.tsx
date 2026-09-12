import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Button, Empty, Input, Modal, Spin, Toast, TreeSelect, Typography } from '@douyinfe/semi-ui'
import { IconChevronLeft, IconFile, IconLink, IconPlus } from '@douyinfe/semi-icons'
import type { Doc, Work } from '../../types'
import { createDoc, getWork, getWorkDocs, patchDoc } from '../../lib/api'
import { useAppStore } from '../../lib/store'
import { docPath, workPath } from '../../lib/routes'
import { buildTreeData, subTreeIds } from '../../lib/tree'

const { Text } = Typography

/**
 * 左栏的资料夹模式。两种形态：
 *  · 宇宙 / 系列（树里有「资料夹」那一行）：自己名下的资料，可新建、可关联子树内的条目；
 *  · 单作（入口在节点菜单的「访问资料夹」，树里不占一行）：只读视图，列出祖先资料夹里
 *    关联到这篇的资料——单作不存资料，它是"谁写了我"的那一面。
 * 正文区始终仍打开选中的文档（对齐 DESIGN_V2 §5.3）。
 */
export default function FolderPane({ workId }: { workId: string }) {
  const nav = useNavigate()
  const params = useParams()
  const { nodes, contentStamp } = useAppStore()
  const [work, setWork] = useState<Work | null>(null)
  const [docs, setDocs] = useState<Doc[]>([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [newTitle, setNewTitle] = useState('')
  const [linking, setLinking] = useState<Doc | null>(null)
  const [linkIds, setLinkIds] = useState<string[]>([])
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const w = await getWork(workId)
      setWork(w.work)
      const d = await getWorkDocs(workId)
      setDocs(d.docs ?? [])
    } catch {
      setDocs([])
    } finally {
      setLoading(false)
    }
  }, [workId])

  useEffect(() => {
    void load()
  }, [load, contentStamp])

  // 资料夹是节点自己的抽屉：只能关联本节点子树内的条目
  const linkTreeData = useMemo(() => {
    const inside = subTreeIds(nodes, workId)
    return buildTreeData(nodes.filter((n) => inside.has(n.id)))
  }, [nodes, workId])

  const submitCreate = async () => {
    if (!newTitle.trim()) return
    try {
      const doc = await createDoc(workId, { title: newTitle.trim(), contentMd: '' })
      Toast.success('已创建资料')
      setCreating(false)
      setNewTitle('')
      void load()
      nav(docPath(workId, doc.id))
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '创建失败')
    }
  }

  const saveLinks = async () => {
    if (!linking) return
    setSaving(true)
    try {
      const updated = await patchDoc(linking.id, { links: linkIds })
      setDocs((prev) => prev.map((d) => (d.id === updated.id ? updated : d)))
      Toast.success('已更新关联')
      setLinking(null)
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '更新关联失败')
    } finally {
      setSaving(false)
    }
  }

  const activeDocId = params.docId
  const title = work?.title ?? '资料夹'
  /** 单作资料夹是只读视图：资料存在祖先容器里，这里只负责把关联到它的挑出来。 */
  const readOnly = work?.kind === 'work'

  return (
    <div className="folder-pane">
      <div className="folder-pane-head">
        <Button
          theme="borderless"
          type="tertiary"
          size="small"
          icon={<IconChevronLeft />}
          onClick={() => nav(workPath(workId))}
        >
          返回宇宙树
        </Button>
        {!readOnly && (
          <Button
            theme="borderless"
            type="tertiary"
            size="small"
            icon={<IconPlus />}
            onClick={() => setCreating(true)}
          >
            新建资料
          </Button>
        )}
      </div>
      <div className="folder-pane-title" title={title}>
        {title} · 资料夹
      </div>

      <div className="folder-pane-list">
        {loading && docs.length === 0 ? (
          <Spin style={{ display: 'block', margin: '24px auto' }} />
        ) : docs.length === 0 ? (
          <Empty
            className="folder-pane-empty"
            description={
              <span className="folder-pane-empty-text">
                还没有资料
                <br />
                {readOnly
                  ? '所属系列的资料夹里还没有关联到这篇的资料'
                  : '资料是挂在这个宇宙 / 系列下的长文（设定集、访谈、攻略…）'}
              </span>
            }
            style={{ padding: 16 }}
          >
            {!readOnly && (
              <div className="folder-pane-empty-cta">
                <Button size="small" theme="light" icon={<IconPlus />} onClick={() => setCreating(true)}>
                  新建资料
                </Button>
              </div>
            )}
          </Empty>
        ) : (
          docs.map((d) => (
            <div
              key={d.id}
              role="button"
              tabIndex={0}
              className={`doc-row side-doc-row${activeDocId === d.id ? ' active' : ''}`}
              onClick={() => nav(docPath(workId, d.id))}
              onKeyDown={(e) => {
                if (e.key === 'Enter') nav(docPath(workId, d.id))
              }}
            >
              <IconFile className="doc-row-icon" />
              <div className="doc-row-main">
                <span className="doc-row-title">{d.title}</span>
                <span className="doc-row-path">
                  {d.links.length > 0 ? `关联 ${d.links.length} 条` : `v${d.contentVer}`}
                </span>
              </div>
              {!readOnly && (
                <Button
                  theme="borderless"
                  type="tertiary"
                  size="small"
                  icon={<IconLink />}
                  aria-label="关联条目"
                  onClick={(e) => {
                    e.stopPropagation()
                    setLinking(d)
                    setLinkIds(d.links ?? [])
                  }}
                />
              )}
            </div>
          ))
        )}
      </div>

      <div className="folder-pane-foot">
        <Text type="tertiary" size="small">
          {docs.length} 份资料 ·{' '}
          {readOnly ? '关联到这篇的资料' : '只能关联本节点子树内的条目'}
        </Text>
      </div>

      <Modal
        title="新建资料"
        visible={creating}
        onCancel={() => setCreating(false)}
        onOk={() => void submitCreate()}
        okText="创建"
        width={420}
      >
        <Input
          placeholder="资料标题，如：剧情解析"
          value={newTitle}
          onChange={setNewTitle}
          autoFocus
          onEnterPress={() => void submitCreate()}
        />
      </Modal>

      <Modal
        title={`关联条目：${linking?.title ?? ''}`}
        visible={!!linking}
        onCancel={() => setLinking(null)}
        onOk={() => void saveLinks()}
        okText="保存"
        confirmLoading={saving}
        width={460}
      >
        <div className="dialog-hint" style={{ marginBottom: 12 }}>
          资料夹是当前宇宙 / 系列自己的抽屉：这里的资料只能关联本节点子树内的条目，不能跨出去。
        </div>
        <TreeSelect
          multiple
          value={linkIds}
          onChange={(v) => setLinkIds((Array.isArray(v) ? v : []).map(String))}
          treeData={linkTreeData}
          defaultExpandAll
          dropdownMatchSelectWidth
          style={{ width: '100%' }}
          placeholder="选择本节点子树内的条目"
        />
      </Modal>
    </div>
  )
}
