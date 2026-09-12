import { Modal, Select, TreeSelect } from '@douyinfe/semi-ui'
import type { PoolEntry } from '../../types'
import type { TreeNodeData } from '../../lib/tree'

/** 「建档 / 关联到…」的目标节点选择弹窗。 */
export function PoolPickModal({
  pick,
  treeData,
  pickParent,
  onPickParentChange,
  onCancel,
  onOk,
}: {
  pick: { entry: PoolEntry; mode: 'link' | 'archive' } | null
  treeData: TreeNodeData[]
  pickParent: string | undefined
  onPickParentChange: (v: string | undefined) => void
  onCancel: () => void
  onOk: () => void
}) {
  return (
    <Modal
      title={
        pick?.mode === 'archive'
          ? `把「${pick?.entry.title}」建档到哪个节点下？`
          : pick
            ? `把「${pick.entry.title}」关联到哪个节点？`
            : ''
      }
      visible={!!pick}
      onCancel={onCancel}
      onOk={onOk}
      okText={pick?.mode === 'archive' ? '建档' : '关联'}
    >
      <TreeSelect
        treeData={treeData}
        value={pickParent}
        onChange={(v) => onPickParentChange(v == null ? undefined : String(Array.isArray(v) ? v[0] : v))}
        placeholder="选一个节点（集合或单作都行）"
        style={{ width: '100%' }}
      />
    </Modal>
  )
}

/** 批量建档弹窗（本批数量 + 说明）。 */
export function PoolBatchModal({
  open,
  stubCount,
  batchSize,
  onBatchSizeChange,
  starting,
  onCancel,
  onOk,
}: {
  open: boolean
  stubCount: number
  batchSize: string
  onBatchSizeChange: (v: string) => void
  starting: boolean
  onCancel: () => void
  onOk: () => void
}) {
  return (
    <Modal
      title="批量建档"
      visible={open}
      onCancel={onCancel}
      onOk={onOk}
      okText="开始建档"
      confirmLoading={starting}
      width={460}
    >
      <div className="dialog-field">
        <span className="dialog-label">本批数量</span>
        <Select<string>
          value={batchSize}
          onChange={(v) => onBatchSizeChange(String(v))}
          optionList={[
            { value: '5', label: '5 部（推荐先试一批）' },
            { value: '10', label: '10 部' },
            { value: '20', label: '20 部' },
          ]}
          style={{ width: '100%' }}
        />
      </div>
      <div className="dialog-hint">
        待建档 {stubCount} 部。Altas 并发 2 部，按 9 章骨架撰写并落库；
        面板顶部的批次卡可以看进度、暂停或单条继续。
      </div>
    </Modal>
  )
}
