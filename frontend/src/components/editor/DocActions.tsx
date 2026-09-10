import { useCallback, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Button, Dropdown, Empty, List, Modal, Spin, Tag, Toast, Typography } from '@douyinfe/semi-ui'
import { IconMore } from '@douyinfe/semi-icons'
import type { Revision } from '../../types'
import { deleteWork, getWorkRevisions, restoreWorkRevision } from '../../lib/api'
import { folderPath } from '../../lib/routes'
import { absoluteTime } from '../../lib/tree'

const { Text } = Typography

const AUTHOR_LABEL: Record<string, string> = {
  human: '我',
  llm: 'Altas',
  import: '导入',
}

/**
 * 正文信息栏右侧只保留「…」。飞书母本里这里是克制的：
 * 分享 / 阅读态 / 锁全都不需要，AI 入口在顶栏与正文空态里（见 TopBar 与 MarkdownEditor）。
 */
export default function DocActions({
  workId,
  workTitle,
  onReload,
  onDeleted,
}: {
  workId: string
  workTitle: string
  onReload: () => Promise<void> | void
  onDeleted: () => void
}) {
  const nav = useNavigate()
  const [historyOpen, setHistoryOpen] = useState(false)
  const [revisions, setRevisions] = useState<Revision[]>([])
  const [loading, setLoading] = useState(false)
  const [restoring, setRestoring] = useState<string | null>(null)

  const loadRevisions = useCallback(async () => {
    setLoading(true)
    try {
      const res = await getWorkRevisions(workId, 30)
      setRevisions(res.revisions ?? [])
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '读取版本失败')
      setRevisions([])
    } finally {
      setLoading(false)
    }
  }, [workId])

  const openHistory = () => {
    setHistoryOpen(true)
    void loadRevisions()
  }

  const restore = async (rev: Revision) => {
    setRestoring(rev.id)
    try {
      await restoreWorkRevision(workId, rev.id)
      Toast.success(`已回滚到 v${rev.version}（生成新版本）`)
      setHistoryOpen(false)
      await onReload()
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '回滚失败')
    } finally {
      setRestoring(null)
    }
  }

  const confirmDelete = () => {
    Modal.confirm({
      title: `删除「${workTitle}」？`,
      content: '正文与版本历史一并移除，无法撤销。若存在子节点会被拒绝。',
      okType: 'danger',
      okText: '删除',
      onOk: async () => {
        try {
          await deleteWork(workId)
          Toast.success('已删除')
          onDeleted()
        } catch (e) {
          Toast.error(e instanceof Error ? e.message : '删除失败')
        }
      },
    })
  }

  return (
    <>
      <Dropdown
        trigger="click"
        position="bottomRight"
        render={
          <Dropdown.Menu>
            <Dropdown.Item onClick={() => nav(folderPath(workId))}>打开资料夹</Dropdown.Item>
            <Dropdown.Item onClick={openHistory}>版本历史</Dropdown.Item>
            <Dropdown.Item
              disabled={revisions.length < 2}
              onClick={() => {
                if (revisions.length >= 2) void restore(revisions[1])
              }}
            >
              回滚到上一版
            </Dropdown.Item>
            <Dropdown.Divider />
            <Dropdown.Item type="danger" onClick={confirmDelete}>
              删除
            </Dropdown.Item>
          </Dropdown.Menu>
        }
      >
        <Button
          theme="borderless"
          type="tertiary"
          size="small"
          icon={<IconMore />}
          aria-label="更多操作"
        />
      </Dropdown>

      <Modal
        title="版本历史"
        visible={historyOpen}
        onCancel={() => setHistoryOpen(false)}
        footer={null}
        width={560}
      >
        {loading ? (
          <Spin style={{ display: 'block', margin: '24px auto' }} />
        ) : revisions.length === 0 ? (
          <Empty description="还没有版本记录" style={{ padding: 24 }} />
        ) : (
          <List<Revision>
            dataSource={revisions}
            split={false}
            renderItem={(rev) => (
              <List.Item key={rev.id} className="revision-row">
                <div className="revision-main">
                  <span className="revision-version">v{rev.version}</span>
                  <Text strong>{rev.summary || '无摘要'}</Text>
                  <Text type="tertiary" size="small">
                    {AUTHOR_LABEL[rev.author] ?? rev.author} · {absoluteTime(rev.createdAt)}
                  </Text>
                </div>
                <Tag size="small" color={rev.author === 'llm' ? 'purple' : 'blue'}>
                  {AUTHOR_LABEL[rev.author] ?? rev.author}
                </Tag>
                <Button
                  size="small"
                  theme="borderless"
                  type="tertiary"
                  loading={restoring === rev.id}
                  onClick={() => void restore(rev)}
                >
                  恢复
                </Button>
              </List.Item>
            )}
          />
        )}
      </Modal>
    </>
  )
}
