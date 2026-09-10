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
 * 左栏的资料夹模式：点系列节点的「资料夹」后，左栏从宇宙树切成该系列的资料列表
 * （对齐 DESIGN_V2 §5.3：左栏切到该节点资料列表，正文区仍打开文档）。
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

  // 资料夹 = 系列：只能关联本系列子树内的单作
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

  return (
    <div className="folder-pane">
      <div className="folder-pane-head">
        <Button
          theme="borderless"
          type="tertiary"
          size="small"
          icon={<IconChevronLeft />}
          onClick={() => nav(workPath(workId, work?.slug))}
        >
          返回宇宙树
        </Button>
        <Button
          theme="borderless"
          type="tertiary"
          size="small"
          icon={<IconPlus />}
          onClick={() => setCreating(true)}
        >
          新建资料
        </Button>
      </div>
      <div className="folder-pane-title" title={title}>
        {title} · 资料夹
      </div>

      <div className="folder-pane-list">
        {loading && docs.length === 0 ? (
          <Spin style={{ display: 'block', margin: '24px auto' }} />
        ) : docs.length === 0 ? (
          <Empty
            description="暂无资料"
            style={{ padding: 16 }}
          />
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
            </div>
          ))
        )}
      </div>

      <div className="folder-pane-foot">
        <Text type="tertiary" size="small">
          {docs.length} 份资料 · 只能关联本系列单作
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
          资料夹就是一个系列：这里的资料只能关联本系列下的单作，不能跨系列。
        </div>
        <TreeSelect
          multiple
          value={linkIds}
          onChange={(v) => setLinkIds((Array.isArray(v) ? v : []).map(String))}
          treeData={linkTreeData}
          defaultExpandAll
          dropdownMatchSelectWidth
          style={{ width: '100%' }}
          placeholder="选择本系列下的单作"
        />
      </Modal>
    </div>
  )
}
