import { Button, Card, Typography } from '@douyinfe/semi-ui'
import type { PoolEntry, PoolGroup } from '../../types'
import PoolEntryRow from './PoolEntryRow'

const { Text } = Typography

/** 一个容器（Emby 库 / Komga 系列 / GA 系列）的分组卡：整组忽略 + 逐条动作。 */
export default function PoolGroupCard({
  group,
  items,
  busyKey,
  keyOf,
  onArchive,
  onLink,
  onIgnoreEntry,
  onIgnoreGroup,
}: {
  group: PoolGroup
  items: PoolEntry[]
  busyKey: string | null
  keyOf: (e: PoolEntry) => string
  onArchive: (e: PoolEntry) => void
  onLink: (e: PoolEntry) => void
  onIgnoreEntry: (e: PoolEntry) => void
  onIgnoreGroup: (g: PoolGroup) => void
}) {
  return (
    <Card
      className="pool-card"
      title={
        <span>
          {group.containerTitle}
          <Text type="tertiary" size="small" style={{ marginLeft: 8 }}>
            未挂链 {group.unlinked.length}
            {group.linkedCount > 0 ? ` · 已挂链 ${group.linkedCount}` : ''}
            {group.ignoredCount > 0 ? ` · 已忽略 ${group.ignoredCount}` : ''}
            {group.newCount > 0 ? ` · 新入库 ${group.newCount}` : ''}
          </Text>
        </span>
      }
      headerExtraContent={
        <Button size="small" theme="borderless" onClick={() => onIgnoreGroup(group)}>
          整组忽略
        </Button>
      }
    >
      {items.map((e) => (
        <PoolEntryRow
          key={keyOf(e)}
          entry={e}
          actions={
            <>
              <Button
                size="small"
                theme="solid"
                type="primary"
                loading={busyKey === keyOf(e)}
                onClick={() => onArchive(e)}
              >
                建档
              </Button>
              <Button size="small" loading={busyKey === keyOf(e)} onClick={() => onLink(e)}>
                关联到…
              </Button>
              <Button
                size="small"
                theme="borderless"
                type="tertiary"
                onClick={() => onIgnoreEntry(e)}
              >
                忽略
              </Button>
            </>
          }
        />
      ))}
    </Card>
  )
}
