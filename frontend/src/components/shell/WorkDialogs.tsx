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

// 介质决定 skill 里的 media-*.md（电影/剧集 → video，漫画/书籍 → book）
const MEDIUM_OPTIONS: { value: string; label: string }[] = [
  { value: 'game', label: '游戏 game' },
  { value: 'movie', label: '电影 movie' },
  { value: 'tv', label: '剧集 tv' },
  { value: 'manga', label: '漫画 manga' },
  { value: 'book', label: '书籍 book' },
  { value: 'other', label: '其他 other' },
]

function defaultChildKind(parentKind?: WorkKind | null): string {
  if (parentKind === 'universe') return 'series'
  if (parentKind === 'series') return 'work'
  return 'universe'
}

// 下级可选层级：只有宇宙不能塞到别人下面；系列下可以再挂系列
// （新水晶神话 ▸ 最终幻想15 这种），单作永远是最底层。
function kindOptionsFor(parentKind?: WorkKind | null): { value: string; label: string }[] {
  if (parentKind) return KIND_OPTIONS.filter((o) => o.value !== 'universe')
  return KIND_OPTIONS
}

export interface CreateTarget {
  id: string | null
  title: string
  kind: WorkKind | null
  /** presetKind 固定层级（「新增文章」用：直接是 work，不给选） */
  presetKind?: WorkKind
}

export function CreateNodeModal({
  target,
  onClose,
}: {
  target: CreateTarget | null
  onClose: () => void
}) {
  const nav = useNavigate()
  const { refreshTree, nodes } = useAppStore()
  const [title, setTitle] = useState('')
  const [kind, setKind] = useState<string>('universe')
  const [medium, setMedium] = useState<string>('game')
  const [parentId, setParentId] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!target) return
    setTitle('')
    setKind(target.presetKind ?? defaultChildKind(target.kind))
    setMedium('game')
    // 归属**继承点击的那一层**：右键某个系列再让人重选一遍，等于把刚说过的话再问一次。
    // 首页那个"新建知识库"没有挂点（id 为空），才从"不归属"开始。
    setParentId(target.id)
  }, [target])

  // 首页开的新建框没有挂点：系列要选所属宇宙（也可以再嵌一层系列）、单作要选所属系列/宇宙。
  //
  // 但**游离叶子是允许的**——设计上支持直接挂在根上的独立单作。所以这里强制的是
  // "必须显式做一次决定"，不是"必须选一个父节点"：列表末尾给一个「不归属」，
  // 既挡掉手滑建出的孤儿，也不把有意为之的游离条目拦在门外。
  const NO_PARENT = '__none__'
  const parentOptions = useMemo(() => {
    const none = [{ value: NO_PARENT, label: '不归属（游离条目，直接挂在根上）' }]
    if (kind === 'series') {
      const unis = nodes.filter((n) => n.kind === 'universe').map((n) => ({ value: n.id, label: `${n.title}（宇宙）` }))
      const series = nodes.filter((n) => n.kind === 'series').map((n) => ({ value: n.id, label: `${n.title}（系列）` }))
      return [...unis, ...series, ...none]
    }
    if (kind === 'work') {
      const series = nodes.filter((n) => n.kind === 'series').map((n) => ({ value: n.id, label: `${n.title}（系列）` }))
      const unis = nodes.filter((n) => n.kind === 'universe').map((n) => ({ value: n.id, label: `${n.title}（宇宙）` }))
      return [...series, ...unis, ...none]
    }
    return []
  }, [nodes, kind])
  const needParentPick = !target?.id && (kind === 'series' || kind === 'work') && parentOptions.length > 0
  const parentLabel = kind === 'series' ? '所属宇宙 / 系列' : '所属系列 / 宇宙'

  const submit = async () => {
    if (!target || !title.trim()) return
    if (needParentPick && !parentId) {
      Toast.warning(`请选择${parentLabel}`)
      return
    }
    setSaving(true)
    try {
      // 「不归属」= 真正建一个游离条目（parentId 传 null），不是空字符串
      const resolvedParent = parentId === NO_PARENT ? null : parentId
      const work = await createWork({
        parentId: target.id ?? (needParentPick ? resolvedParent : null),
        kind: kind as WorkKind,
        title: title.trim(),
        medium: kind === 'work' ? (medium as Medium) : undefined,
      })
      Toast.success(`已创建「${work.title}」`)
      void refreshTree()
      onClose()
      nav(workPath(work.id))
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
      {needParentPick && (
        <div className="dialog-field">
          <span className="dialog-label">{parentLabel}</span>
          <Select<string>
            value={parentId ?? undefined}
            onChange={(v) => setParentId(String(v))}
            optionList={parentOptions}
            placeholder={
              kind === 'series' ? '归属的宇宙，或再上一层的系列' : '选择归属的系列（也可直接挂宇宙）'
            }
            style={{ width: '100%' }}
          />
        </div>
      )}
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
        资料夹挂在宇宙 / 系列上（单作的资料夹是只读视图，走节点菜单的「访问资料夹」）；
        系列也可以嵌在系列下。单作建议选对介质，Altas 会据此加载写作规范
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
