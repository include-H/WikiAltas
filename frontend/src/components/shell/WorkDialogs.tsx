import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Input, Modal, Select, Toast, TreeSelect } from '@douyinfe/semi-ui'
import type { Medium, WorkKind, WorkSummary } from '../../types'
import { createWork, patchWork } from '../../lib/api'
import { useAppStore } from '../../lib/store'
import { workPath } from '../../lib/routes'
import { buildTreeData, subTreeIds } from '../../lib/tree'
import type { TreeNodeData } from '../../lib/tree'

const KIND_OPTIONS: { value: string; label: string }[] = [
  { value: 'universe', label: '宇宙 universe' },
  { value: 'series', label: '系列 series' },
  { value: 'work', label: '单作 work' },
]

// 介质决定 skill 里的 media-*.md（小说/电影/剧集都用得上）
const MEDIUM_OPTIONS: { value: string; label: string }[] = [
  { value: 'game', label: '游戏 game' },
  { value: 'movie', label: '电影 movie' },
  { value: 'tv', label: '剧集 tv' },
  { value: 'anime', label: '动画 anime' },
  { value: 'manga', label: '漫画 manga' },
  { value: 'novel', label: '小说 novel' },
  { value: 'book', label: '书籍 book' },
  { value: 'other', label: '其他 other' },
]

function defaultChildKind(parentKind?: WorkKind | null): string {
  if (parentKind === 'universe') return 'series'
  if (parentKind === 'series') return 'work'
  return 'universe'
}

function kindOptionsFor(parentKind?: WorkKind | null): { value: string; label: string }[] {
  if (parentKind === 'universe') return KIND_OPTIONS.filter((o) => o.value !== 'universe')
  if (parentKind === 'series') return KIND_OPTIONS.filter((o) => o.value === 'work')
  return KIND_OPTIONS
}

export interface CreateTarget {
  id: string | null
  title: string
  kind: WorkKind | null
}

export function CreateNodeModal({
  target,
  onClose,
}: {
  target: CreateTarget | null
  onClose: () => void
}) {
  const nav = useNavigate()
  const { refreshTree } = useAppStore()
  const [title, setTitle] = useState('')
  const [kind, setKind] = useState<string>('universe')
  const [medium, setMedium] = useState<string>('game')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!target) return
    setTitle('')
    setKind(defaultChildKind(target.kind))
    setMedium('game')
  }, [target])

  const submit = async () => {
    if (!target || !title.trim()) return
    setSaving(true)
    try {
      const work = await createWork({
        parentId: target.id,
        kind: kind as WorkKind,
        title: title.trim(),
        medium: kind === 'work' ? (medium as Medium) : undefined,
      })
      Toast.success(`已创建「${work.title}」`)
      void refreshTree()
      onClose()
      nav(workPath(work.id, work.slug))
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '创建失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      title={target?.id ? `在「${target.title}」下新建` : '新建知识库'}
      visible={!!target}
      onCancel={onClose}
      onOk={() => void submit()}
      okText="创建"
      confirmLoading={saving}
      width={420}
    >
      <div className="dialog-field">
        <span className="dialog-label">层级</span>
        <Select<string>
          value={kind}
          onChange={(v) => setKind(String(v))}
          optionList={kindOptionsFor(target?.kind)}
          style={{ width: '100%' }}
        />
      </div>
      <div className="dialog-field">
        <span className="dialog-label">标题</span>
        <Input
          placeholder="如：猎魔人 / 巫师系列 / 巫师3"
          value={title}
          onChange={setTitle}
          autoFocus
          onEnterPress={() => void submit()}
        />
      </div>
      {kind === 'work' && (
        <div className="dialog-field">
          <span className="dialog-label">介质</span>
          <Select<string>
            value={medium}
            onChange={(v) => setMedium(String(v))}
            optionList={MEDIUM_OPTIONS}
            style={{ width: '100%' }}
          />
        </div>
      )}
      <div className="dialog-hint">
        宇宙 / 系列 / 单作都可以挂资料夹（非标文件、解析稿）；单作建议选对介质，
        Altas 会据此加载写作规范
      </div>
    </Modal>
  )
}

export function MoveNodeModal({
  node,
  onClose,
}: {
  node: WorkSummary | null
  onClose: () => void
}) {
  const { nodes, refreshTree } = useAppStore()
  const [parentId, setParentId] = useState<string>('__root__')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (node) setParentId(node.parentId ?? '__root__')
  }, [node])

  const treeData = useMemo<TreeNodeData[]>(() => {
    if (!node) return []
    const blocked = subTreeIds(nodes, node.id)
    const allowed = nodes.filter((n) => !blocked.has(n.id))
    return [
      { key: '__root__', value: '__root__', label: '根目录（宇宙层）' },
      ...buildTreeData(allowed),
    ]
  }, [node, nodes])

  const submit = async () => {
    if (!node) return
    setSaving(true)
    try {
      // 后端用空字符串表达「移到根」，null 会被当成"未提供该字段"
      await patchWork(node.id, { parentId: parentId === '__root__' ? '' : parentId })
      Toast.success(`已移动「${node.title}」`)
      void refreshTree()
      onClose()
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '移动失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      title={`移动「${node?.title ?? ''}」`}
      visible={!!node}
      onCancel={onClose}
      onOk={() => void submit()}
      okText="移动"
      confirmLoading={saving}
      width={420}
    >
      <div className="dialog-field">
        <span className="dialog-label">新的上级</span>
        <TreeSelect
          treeData={treeData}
          value={parentId}
          onChange={(v) => setParentId(String(v ?? '__root__'))}
          defaultExpandAll
          dropdownMatchSelectWidth
          style={{ width: '100%' }}
          placeholder="选择上级节点"
        />
      </div>
      <div className="dialog-hint">不能移动到自己的子树内。</div>
    </Modal>
  )
}
