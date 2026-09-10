import { useMemo, useState } from 'react'
import { useLocation, useNavigate, useParams } from 'react-router-dom'
import { Button, Dropdown, Empty, Modal, Spin, Tag, Toast, Tree } from '@douyinfe/semi-ui'
import { IconFile, IconFolder, IconMore, IconStar } from '@douyinfe/semi-icons'
import type { ReactNode } from 'react'
import type { WorkSummary } from '../../types'
import { deleteWork } from '../../lib/api'
import { useAppStore } from '../../lib/store'
import { folderPath, workPath, UNKNOWN_WORK_ID } from '../../lib/routes'
import { STATUS_META, attachFolderRows, folderKey, folderKeyWorkId, isFolderKey } from '../../lib/tree'
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
  } = useAppStore()
  const nav = useNavigate()
  const params = useParams()
  const loc = useLocation()
  const [createTarget, setCreateTarget] = useState<CreateTarget | null>(null)
  const [moveNode, setMoveNode] = useState<WorkSummary | null>(null)

  const routeId = params.id
  const selectedId = routeId && routeId !== UNKNOWN_WORK_ID ? routeId : null

  const treeData = useMemo(() => attachFolderRows(nodes), [nodes])
  const byId = useMemo(() => new Map(nodes.map((n) => [n.id, n])), [nodes])

  const onSelect = (selectedKey: string) => {
    const key = String(selectedKey ?? '')
    if (isFolderKey(key)) {
      nav(folderPath(folderKeyWorkId(key)))
      return
    }
    const node = byId.get(key)
    if (node) nav(workPath(node.id, node.slug))
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
    const status = STATUS_META[w.status]
    const pinned = pinnedIds.includes(w.id)
    return (
      <span className="tree-node" title={w.title}>
        <IconFile size="small" className="tree-node-icon" />
        <span className="tree-node-title">{w.title}</span>
        {pinned && <IconStar size="small" className="tree-node-pin" />}
        <Tag color={status.color} size="small" className="tree-node-status">
          {status.text}
        </Tag>
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
                <Dropdown.Item onClick={() => setMoveNode(w)}>移动到…</Dropdown.Item>
                <Dropdown.Item onClick={() => nav(folderPath(w.id))}>打开资料夹</Dropdown.Item>
                <Dropdown.Item
                  onClick={() => {
                    setAiPanelOpen(true)
                    nav(workPath(w.id, w.slug))
                  }}
                >
                  馆员建档
                </Dropdown.Item>
                <Dropdown.Item onClick={() => togglePin(w.id)}>
                  {pinned ? '取消置顶' : '置顶'}
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
        description="还没有作品。点上方「新建或置顶知识库」建一个宇宙，或让馆员建档。"
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
        onSelect={onSelect}
        renderLabel={renderLabel}
        expandAll
        filterTreeNode={false}
        showLine={false}
        style={{ background: 'transparent' }}
      />
      <CreateNodeModal target={createTarget} onClose={() => setCreateTarget(null)} />
      <MoveNodeModal node={moveNode} onClose={() => setMoveNode(null)} />
    </>
  )
}
