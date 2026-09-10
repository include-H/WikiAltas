import { useCallback, useEffect, useMemo, useState } from 'react'
import { Banner, Button, Empty, List, Modal, Spin, Tag, Toast, Typography } from '@douyinfe/semi-ui'
import type { Revision } from '../../types'
import { getWorkRevisions, restoreWorkRevision } from '../../lib/api'
import { useAppStore } from '../../lib/store'
import { absoluteTime } from '../../lib/tree'

const { Text } = Typography

const HANDLED_KEY = 'wikiatlas.revisions.handled.'
const REVISION_PREFIX = '修订：'

function readHandled(workId: string): string[] {
  try {
    const raw = localStorage.getItem(HANDLED_KEY + workId)
    const parsed: unknown = raw ? JSON.parse(raw) : []
    return Array.isArray(parsed) ? parsed.filter((v): v is string => typeof v === 'string') : []
  } catch {
    return []
  }
}

function writeHandled(workId: string, ids: string[]): void {
  try {
    localStorage.setItem(HANDLED_KEY + workId, JSON.stringify(ids))
  } catch {
    // 存储不可用时降级为会话内状态
  }
}

/**
 * 修订模式产物的"逐条接受 / 拒绝"条。
 *
 * 冻结设计里没有 Proposal 表（写完即 commit，回滚靠 revision），所以这里：
 *   · 接受 = 保留已生效的改动，只在本地归档（后端无需动作）
 *   · 拒绝 = restoreWorkRevision 回到"这一条之前"的版本，等于撤销该条修订
 */
export default function PendingRevisions({
  workId,
  onReload,
}: {
  workId: string
  onReload: () => Promise<void> | void
}) {
  const { contentStamp } = useAppStore()
  const [revisions, setRevisions] = useState<Revision[]>([])
  const [handled, setHandled] = useState<string[]>(() => readHandled(workId))
  const [open, setOpen] = useState(false)
  const [loading, setLoading] = useState(false)
  const [busyId, setBusyId] = useState<string | null>(null)

  useEffect(() => {
    setHandled(readHandled(workId))
  }, [workId])

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const res = await getWorkRevisions(workId, 40)
      setRevisions(res.revisions ?? [])
    } catch {
      setRevisions([])
    } finally {
      setLoading(false)
    }
  }, [workId])

  useEffect(() => {
    void load()
  }, [load, contentStamp])

  // 只挑"馆员在修订模式下写的"版本，且还没处理过
  const pending = useMemo(
    () =>
      revisions.filter(
        (r) => r.author === 'llm' && r.summary.startsWith(REVISION_PREFIX) && !handled.includes(r.id),
      ),
    [revisions, handled],
  )

  const markHandled = (id: string) => {
    setHandled((prev) => {
      const next = [...prev, id]
      writeHandled(workId, next)
      return next
    })
  }

  /** 拒绝：回滚到该修订之前的那一版（列表按 version 倒序，下一条就是"改前"）。 */
  const reject = async (rev: Revision) => {
    const idx = revisions.findIndex((r) => r.id === rev.id)
    const before = revisions[idx + 1]
    if (!before) {
      Toast.info('这是最早的一版，无法回滚')
      return
    }
    setBusyId(rev.id)
    try {
      await restoreWorkRevision(workId, before.id)
      markHandled(rev.id)
      Toast.success(`已拒绝该修订（回到 v${before.version}）`)
      await onReload()
      await load()
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '回滚失败')
    } finally {
      setBusyId(null)
    }
  }

  if (pending.length === 0) return null

  return (
    <>
      <Banner
        type="info"
        closeIcon={null}
        className="pending-revisions"
        description={
          <span className="pending-rev-row">
            <Text strong>馆员提了 {pending.length} 处修订</Text>
            <Text type="tertiary" size="small">
              逐条接受或拒绝（拒绝=回到改前版本）
            </Text>
            <Button size="small" theme="borderless" type="primary" onClick={() => setOpen(true)}>
              逐条查看
            </Button>
          </span>
        }
      />
      <Modal
        title="待确认的修订"
        visible={open}
        onCancel={() => setOpen(false)}
        footer={null}
        width={560}
      >
        {loading ? (
          <Spin style={{ display: 'block', margin: '24px auto' }} />
        ) : pending.length === 0 ? (
          <Empty description="没有待确认的修订" style={{ padding: 24 }} />
        ) : (
          <List<Revision>
            dataSource={pending}
            split={false}
            renderItem={(rev) => (
              <List.Item key={rev.id} className="pending-rev-item">
                <div className="pending-rev-main">
                  <span className="pending-rev-reason">
                    {rev.summary.slice(REVISION_PREFIX.length) || '未说明理由'}
                  </span>
                  <Text type="tertiary" size="small">
                    v{rev.version} · {absoluteTime(rev.createdAt)}
                  </Text>
                </div>
                <Tag size="small" color="orange">
                  待确认
                </Tag>
                <Button
                  size="small"
                  theme="borderless"
                  type="tertiary"
                  loading={busyId === rev.id}
                  onClick={() => void reject(rev)}
                >
                  拒绝
                </Button>
                <Button size="small" type="primary" onClick={() => markHandled(rev.id)}>
                  接受
                </Button>
              </List.Item>
            )}
          />
        )}
      </Modal>
    </>
  )
}
