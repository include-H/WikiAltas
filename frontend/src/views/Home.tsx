import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Button, Empty, List, Modal, Select, Spin, Tag, Toast, Typography } from '@douyinfe/semi-ui'
import { IconAIFilledLevel1, IconFile, IconPlus, IconStar } from '@douyinfe/semi-icons'
import type { Run, WorkSummary } from '../types'
import { createBatchWiki, listRuns } from '../lib/api'
import { useAppStore } from '../lib/store'
import { workPath } from '../lib/routes'
import { STATUS_META, ancestorPath, relativeTime } from '../lib/tree'
import { CreateNodeModal } from '../components/shell/WorkDialogs'
import type { CreateTarget } from '../components/shell/WorkDialogs'

const { Title, Text } = Typography

export default function Home() {
  const { nodes, treeLoading, treeError, pinnedIds, setActiveBatchId, setAiPanelOpen } =
    useAppStore()
  const nav = useNavigate()
  const [createRoot, setCreateRoot] = useState<CreateTarget | null>(null)
  const [runs, setRuns] = useState<Run[]>([])
  const [batchOpen, setBatchOpen] = useState(false)
  const [batchSize, setBatchSize] = useState('5')
  const [batchStarting, setBatchStarting] = useState(false)

  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const res = await listRuns(undefined, 20)
        if (!cancelled) setRuns(res.runs ?? [])
      } catch {
        if (!cancelled) setRuns([])
      }
    })()
    return () => {
      cancelled = true
    }
  }, [])

  const pinned = useMemo(
    () => nodes.filter((n) => pinnedIds.includes(n.id)),
    [nodes, pinnedIds],
  )
  const recent = useMemo(
    () =>
      [...nodes]
        .sort((a, b) => (b.updatedAt ?? '').localeCompare(a.updatedAt ?? ''))
        .slice(0, 12),
    [nodes],
  )
  const stubs = useMemo(() => nodes.filter((n) => n.status === 'stub'), [nodes])
  const running = runs.filter((r) => r.status === 'running' || r.status === 'interrupted')

  const startBatch = async () => {
    setBatchStarting(true)
    try {
      const res = await createBatchWiki({
        workIds: stubs.map((s) => s.id),
        batchSize: Number(batchSize),
      })
      setActiveBatchId(res.batchId)
      setAiPanelOpen(true)
      setBatchOpen(false)
      Toast.success(`已排入 ${res.runIds.length} 个建档工单`)
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '创建批次失败')
    } finally {
      setBatchStarting(false)
    }
  }

  const row = (n: WorkSummary, pinnedRow = false) => {
    const path = ancestorPath(nodes, n.id)
      .slice(0, -1)
      .map((p) => p.title)
      .join(' / ')
    const status = STATUS_META[n.status]
    return (
      <List.Item
        key={n.id}
        className="doc-row"
        onClick={() => nav(workPath(n.id, n.slug))}
      >
        {pinnedRow ? <IconStar size="small" className="doc-row-pin" /> : <IconFile className="doc-row-icon" />}
        <div className="doc-row-main">
          <span className="doc-row-title">{n.title}</span>
          {path && <span className="doc-row-path">{path}</span>}
        </div>
        <Tag size="small" color={status.color} className="doc-row-status">
          {status.text}
        </Tag>
        <span className="doc-row-time">{relativeTime(n.updatedAt)}</span>
      </List.Item>
    )
  }

  const section = (
    title: string,
    items: WorkSummary[],
    emptyText: string,
    pinnedRow = false,
  ) => (
    <section className="home-section">
      <Title heading={6} className="home-section-title">
        {title}
      </Title>
      {items.length === 0 ? (
        <div className="home-empty">{emptyText}</div>
      ) : (
        <List<WorkSummary>
          dataSource={items}
          split={false}
          className="doc-list"
          renderItem={(n) => row(n, pinnedRow)}
        />
      )}
    </section>
  )

  return (
    <div className="home-view">
      <div className="home-head">
        <Title heading={4} style={{ margin: 0 }}>
          首页
        </Title>
        <Button
          theme="solid"
          type="primary"
          icon={<IconPlus />}
          onClick={() => setCreateRoot({ id: null, title: '', kind: null })}
        >
          新建知识库
        </Button>
      </div>

      {treeLoading && nodes.length === 0 && (
        <Spin style={{ display: 'block', margin: '48px auto' }} />
      )}
      {treeError && (
        <Empty description={treeError} style={{ margin: '32px 0' }} />
      )}

      {pinned.length > 0 && section('置顶', pinned, '', true)}
      {section('最近作品', recent, '还没有作品。新建一个宇宙，或直接让右下角馆员建档。')}
      {stubs.length > 0 && (
        <section className="home-section">
          <div className="home-section-head">
            <Title heading={6} className="home-section-title">
              待建档
            </Title>
            <Button
              size="small"
              icon={<IconAIFilledLevel1 />}
              className="ai-coedit-btn"
              theme="solid"
              onClick={() => setBatchOpen(true)}
            >
              一键批量建档
            </Button>
          </div>
          <List<WorkSummary>
            dataSource={stubs}
            split={false}
            className="doc-list"
            renderItem={(n) => row(n)}
          />
        </section>
      )}

      <section className="home-section">
        <Title heading={6} className="home-section-title">
          进行中工单
        </Title>
        {running.length === 0 ? (
          <div className="home-empty">没有进行中的馆员工单。</div>
        ) : (
          <List<Run>
            dataSource={running}
            split={false}
            className="doc-list"
            renderItem={(r) => (
              <List.Item key={r.id} className="doc-row" onClick={() => nav('/runs')}>
                <div className="doc-row-main">
                  <span className="doc-row-title">{r.goal || r.intent}</span>
                  <span className="doc-row-path">{r.intent}</span>
                </div>
                <span className="doc-row-time">{relativeTime(r.startedAt)}</span>
              </List.Item>
            )}
          />
        )}
      </section>

      <div className="home-foot">
        <Text type="tertiary" size="small">
          {nodes.length} 个节点 · {stubs.length} 待建档
        </Text>
      </div>

      <CreateNodeModal target={createRoot} onClose={() => setCreateRoot(null)} />
      <Modal
        title="批量建档"
        visible={batchOpen}
        onCancel={() => setBatchOpen(false)}
        onOk={() => void startBatch()}
        okText="开始建档"
        confirmLoading={batchStarting}
        width={460}
      >
        <div className="dialog-field">
          <span className="dialog-label">本批数量</span>
          <Select<string>
            value={batchSize}
            onChange={(v) => setBatchSize(String(v))}
            optionList={[
              { value: '5', label: '5 部（推荐先试一批）' },
              { value: '10', label: '10 部' },
              { value: '20', label: '20 部' },
            ]}
            style={{ width: '100%' }}
          />
        </div>
        <div className="dialog-hint">
          待建档共 {stubs.length} 部。馆员并发 2 部，逐部按 9 章骨架撰写并落库；
          面板顶部的批次卡可以看进度、暂停或单条继续。
        </div>
      </Modal>
    </div>
  )
}
