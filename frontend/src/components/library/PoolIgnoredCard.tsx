import { useState } from 'react'
import { Button, Card, Tag, Typography } from '@douyinfe/semi-ui'
import type { PoolEntry, PoolIgnoredGroup } from '../../types'
import { SOURCE_LABEL } from '../../lib/mediaTags'
import PoolEntryRow from './PoolEntryRow'

const { Text } = Typography

/** 已忽略卡：默认收起；展开后按「整组 / 单条」恢复。 */
export default function PoolIgnoredCard({
  entries,
  groups,
  onRestoreEntry,
  onRestoreGroup,
}: {
  entries: PoolEntry[]
  groups: PoolIgnoredGroup[]
  onRestoreEntry: (e: PoolEntry) => void
  onRestoreGroup: (source: string, containerKey: string) => void
}) {
  const [showIgnored, setShowIgnored] = useState(false)
  if (entries.length === 0 && groups.length === 0) return null
  return (
    <Card
      className="pool-card"
      title={
        <span>
          已忽略
          <Text type="tertiary" size="small" style={{ marginLeft: 8 }}>
            {entries.length} 条 · {groups.length} 组
          </Text>
        </span>
      }
      headerExtraContent={
        <Button size="small" theme="borderless" onClick={() => setShowIgnored((v) => !v)}>
          {showIgnored ? '收起' : '展开'}
        </Button>
      }
    >
      {showIgnored && (
        <>
          {groups.map((g) => (
            <div className="ga-suggest-row" key={`${g.source}|${g.containerKey}`}>
              <span className="ga-suggest-meta">
                <span className="ga-suggest-title">
                  {g.containerTitle}
                  <Tag size="small" style={{ marginLeft: 8 }}>
                    {SOURCE_LABEL[g.source] ?? g.source}
                  </Tag>
                </span>
                <Text type="tertiary" size="small">
                  整组已忽略
                </Text>
              </span>
              <Button
                size="small"
                theme="borderless"
                type="primary"
                onClick={() => onRestoreGroup(g.source, g.containerKey)}
              >
                恢复整组
              </Button>
            </div>
          ))}
          {entries.map((e) => (
            <PoolEntryRow
              key={`${e.source}:${e.publicId}`}
              entry={e}
              actions={
                <Button size="small" theme="borderless" type="primary" onClick={() => onRestoreEntry(e)}>
                  恢复
                </Button>
              }
            />
          ))}
        </>
      )}
    </Card>
  )
}
