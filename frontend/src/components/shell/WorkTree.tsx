import { useEffect, useMemo, useState } from 'react'
import { useLocation, useNavigate, useParams } from 'react-router-dom'
import { Button, Dropdown, Empty, Modal, Spin, Toast, Tree } from '@douyinfe/semi-ui'
import { IconFile, IconFolder, IconLock, IconMore, IconStar } from '@douyinfe/semi-icons'
import type { ReactNode } from 'react'
import type { WorkSummary } from '../../types'
import { deleteWork, patchWork } from '../../lib/api'
import { useAppStore } from '../../lib/store'
import { folderPath, workPath, UNKNOWN_WORK_ID } from '../../lib/routes'
import { MAX_PINS } from '../../lib/pins'
import { attachFolderRows, folderKey, folderKeyWorkId, isFolderKey } from '../../lib/tree'
import type { TreeNodeData } from '../../lib/tree'
import { CreateNodeModal, MoveNodeModal } from './WorkDialogs'
import type { CreateTarget } from './WorkDialogs'

export default function WorkTree() {
  const {
    nodes,
    treeLoading,
    treeError,
    refreshTree,
    pinnedIds,
    togglePin,
    setAiPanelOpen,
    me,
  } = useAppStore()
  const nav = useNavigate()
  const params = useParams()
  const loc = useLocation()
  const [createTarget, setCreateTarget] = useState<CreateTarget | null>(null)
  const [moveNode, setMoveNode] = useState<WorkSummary | null>(null)
  // 目录树默认折叠：只露根节点，点谁展开谁（以前是 expandAll，整棵树全摊开）。
  const [expandedKeys, setExpandedKeys] = useState<string[]>([])

  const routeId = params.id
  const selectedId = routeId && routeId !== UNKNOWN_WORK_ID ? routeId : null

  const treeData = useMemo(() => attachFolderRows(nodes), [nodes])
  const byId = useMemo(() => new Map(nodes.map((n) => [n.id, n])), [nodes])

  // 当前选中的节点要看得见：把它一路上级展开（其余分支保持折叠）。
  // 刷新后直达深层页面、或从别处导航过来时，树都会跟到你的位置。
  useEffect(() => {
    if (!selectedId) return
    const chain: string[] = []
    let cur = byId.get(selectedId)
    while (cur?.parentId) {
      chain.push(cur.parentId)
      cur = byId.get(cur.parentId)
    }
    if (chain.length) {
      setExpandedKeys((prev) => Array.from(new Set([...prev, ...chain])))
    }
  }, [selectedId, byId])

  const onSelect = (selectedKey: string) => {
    const key = String(selectedKey ?? '')
    if (isFolderKey(key)) {
      nav(folderPath(folderKeyWorkId(key)))
      return
    }
    // 点击的同时展开它（点击的主语义 = 去这一层 + 看到下一层）
    setExpandedKeys((prev) => (prev.includes(key) ? prev : [...prev, key]))
    const node = byId.get(key)
    if (node) nav(workPath(node.id))
  }

  const removeNode = (w: WorkSummary) => {
    Modal.confirm({
      title: `删除「${w.title}」？`,
      content: '若存在子节点会被拒绝。删除后正文与版本一并移除，无法撤销。',
      okType: 'danger',
      okText: '删除',
      onOk: async () => {
        try {
          await deleteWork(w.id)
          Toast.success('已删除')
          void refreshTree()
          if (selectedId === w.id) nav('/')
        } catch (e) {
          Toast.error(e instanceof Error ? e.message : '删除失败')
        }
      },
    })
  }

  /** 切换节点可见性（public 才能被访客看到，且要求祖先也公开）。 */
  const setVisibility = async (id: string, makePublic: boolean) => {
    try {
      await patchWork(id, { visibility: makePublic ? 'public' : 'private' })
      Toast.success(makePublic ? '已设为公开' : '已设为私有')
      void refreshTree()
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '修改失败')
    }
  }

  const renderLabel = (_label: ReactNode, treeNode?: TreeNodeData | { key?: string }) => {
    const key = String((treeNode as TreeNodeData | undefined)?.key ?? '')
    if (isFolderKey(key)) {
      return (
        <span className="tree-node tree-node-folder">
          <IconFolder size="small" className="tree-node-icon" />
          <span className="tree-node-title">资料夹</span>
        </span>
      )
    }
    const w = byId.get(key)
    if (!w) return _label
    const pinned = pinnedIds.includes(w.id)
    const isPublic = w.visibility === 'public'
    // 树上不再挂状态徽标（stub/draft/ready 逐行都是噪音）：
    // 没有正文的节点只把标题压暗，其余状态在作品页与首页看。
    const isEmptyStub = w.status === 'stub' && !w.hasContent
    return (
      <span className="tree-node" title={w.title}>
        {isPublic ? (
          <IconFile size="small" className="tree-node-icon" />
        ) : (
          <IconLock size="small" className="tree-node-icon" />
        )}
        <span className={`tree-node-title${isEmptyStub ? ' is-empty' : ''}`}>{w.title}</span>
        {pinned && <IconStar size="small" className="tree-node-pin" />}
        {me.authed && (
        <span className="tree-node-actions">
          <Dropdown
            trigger="click"
            position="bottomRight"
            render={
              <Dropdown.Menu>
                <Dropdown.Item
                  onClick={() =>
                    setCreateTarget({ id: w.id, title: w.title, kind: w.kind })
                  }
                  disabled={w.kind === 'work'}
                >
                  新建子节点
                </Dropdown.Item>
                <Dropdown.Item
                  onClick={() =>
                    setCreateTarget({
                      id: w.id,
                      title: w.title,
                      kind: w.kind,
                      // 文章 = work 节点。层级就是**你点的那一层**，不必再选；
                      // 下面的「新建子节点」则保留"选层级"的能力（系列下还能再挂系列）。
                      presetKind: 'work',
                    })
                  }
                  disabled={w.kind === 'work'}
                >
                  新增文章
                </Dropdown.Item>
                <Dropdown.Item onClick={() => setMoveNode(w)}>移动到…</Dropdown.Item>
                <Dropdown.Item onClick={() => nav(folderPath(w.id))}>访问资料夹</Dropdown.Item>
                <Dropdown.Item
                  onClick={() => {
                    setAiPanelOpen(true)
                    nav(workPath(w.id))
                  }}
                >
                  Altas 建档
                </Dropdown.Item>
                <Dropdown.Item
                  onClick={() => togglePin(w.id)}
                  // 满了就禁掉并说明原因（点不动又没有解释，比不给点更让人困惑）
                  disabled={!pinned && pinnedIds.length >= MAX_PINS}
                >
                  {pinned ? '取消置顶' : pinnedIds.length >= MAX_PINS ? `置顶（已满 ${MAX_PINS}）` : '置顶'}
                </Dropdown.Item>
                <Dropdown.Item
                  onClick={() => void setVisibility(w.id, w.visibility !== 'public')}
                >
                  {isPublic ? '设为私有' : '设为公开'}
                </Dropdown.Item>
                <Dropdown.Divider />
                <Dropdown.Item type="danger" onClick={() => removeNode(w)}>
                  删除
                </Dropdown.Item>
              </Dropdown.Menu>
            }
          >
            <Button
              theme="borderless"
              type="tertiary"
              size="small"
              className="tree-more"
              icon={<IconMore />}
              aria-label="节点操作"
              onClick={(e) => e.stopPropagation()}
            />
          </Dropdown>
        </span>
        )}
      </span>
    )
  }

  if (treeLoading && nodes.length === 0) {
    return <Spin style={{ display: 'block', margin: '24px auto' }} />
  }
  if (treeError) {
    return <div className="tree-error">{treeError}</div>
  }
  if (nodes.length === 0) {
    return (
      <Empty
        description="暂无作品"
        style={{ padding: 16 }}
      />
    )
  }

  return (
    <>
      <Tree
        className="side-tree"
        treeData={treeData}
        value={
          loc.pathname.endsWith('/folder') && selectedId
            ? [folderKey(selectedId)]
            : selectedId
              ? [selectedId]
              : []
        }
        expandedKeys={expandedKeys}
        onExpand={(keys) => setExpandedKeys(keys as string[])}
        onSelect={onSelect}
        renderLabel={renderLabel}
        filterTreeNode={false}
        showLine={false}
        style={{ background: 'transparent' }}
      />
      <CreateNodeModal target={createTarget} onClose={() => setCreateTarget(null)} />
      <MoveNodeModal node={moveNode} onClose={() => setMoveNode(null)} />
    </>
  )
}
