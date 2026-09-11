import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Button,
  Empty,
  List,
  Modal,
  Select,
  Spin,
  Tag,
  Toast,
  Tooltip,
  Typography,
} from '@douyinfe/semi-ui'
import { IconAIFilledLevel1, IconFile, IconPlus, IconStar } from '@douyinfe/semi-icons'
import type { Run, WorkSummary } from '../types'
import { createBatchWiki, listRuns } from '../lib/api'
import { useAppStore } from '../lib/store'
import { workPath } from '../lib/routes'
import { STATUS_META, absoluteTime, ancestorPath, relativeTime } from '../lib/tree'
import { CreateNodeModal } from '../components/shell/WorkDialogs'
import type { CreateTarget } from '../components/shell/WorkDialogs'

const { Title, Text } = Typography

export default function Home() {
  const { nodes, treeLoading, treeError, pinnedIds, setActiveBatchId, setAiPanelOpen, me } =
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

  const row = (n: WorkSummary, pinnedRow = false, showStatus = true) => {
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
        {showStatus && n.status !== 'stub' && (
          <Tag size="small" color={status.color} className="doc-row-status">
            {status.label}
          </Tag>
        )}
        <Tooltip content={absoluteTime(n.updatedAt)} position="left">
          <span className="doc-row-time">{relativeTime(n.updatedAt)}</span>
        </Tooltip>
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
        {me.authed && (
          <Button
            theme="solid"
            type="primary"
            icon={<IconPlus />}
            onClick={() => setCreateRoot({ id: null, title: '', kind: null })}
          >
            新建知识库
          </Button>
        )}
      </div>

      {treeLoading && nodes.length === 0 && (
        <Spin style={{ display: 'block', margin: '48px auto' }} />
      )}
      {treeError && (
        <Empty description={treeError} style={{ margin: '32px 0' }} />
      )}
      {!treeLoading && !treeError && nodes.length === 0 && (
        <Empty
          className="home-blank"
          description={
            me.authed
              ? '还没有任何条目。建一个宇宙，Altas 就能按 9 章骨架开始写。'
              : '这个 Wiki 还没有公开内容'
          }
          style={{ margin: '56px 0' }}
        >
          {me.authed && (
            <Button
              theme="solid"
              type="primary"
              icon={<IconPlus />}
              onClick={() => setCreateRoot({ id: null, title: '', kind: null })}
            >
              新建知识库
            </Button>
          )}
        </Empty>
      )}

      {/* 空库时只留一个空态，不再往下铺一堆空 section */}
      {nodes.length > 0 && pinned.length > 0 && section('置顶', pinned, '', true)}
      {nodes.length > 0 && section('最近作品', recent, '暂无作品')}
      {nodes.length > 0 && me.authed && stubs.length > 0 && (
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
            renderItem={(n) => row(n, false, false)}
          />
        </section>
      )}

      {me.authed && running.length > 0 && (
      <section className="home-section">
        <Title heading={6} className="home-section-title">
          进行中工单
        </Title>
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
      </section>
      )}

      {nodes.length > 0 && (
      <div className="home-foot">
        <Text type="tertiary" size="small">
          {me.authed
            ? `${nodes.length} 个节点 · ${stubs.length} 待建档`
            : `${nodes.length} 篇公开文档 · 访客模式（登录后可写作与管理）`}
        </Text>
      </div>
      )}

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
          待建档 {stubs.length} 部。Altas 并发 2 部，按 9 章骨架撰写并落库；
          面板顶部的批次卡可以看进度、暂停或单条继续。
        </div>
      </Modal>
    </div>
  )
}
