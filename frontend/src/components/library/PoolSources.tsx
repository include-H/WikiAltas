import { Empty, Select, Tabs, Typography } from '@douyinfe/semi-ui'
import type { PoolEntry, PoolGroup } from '../../types'
import { SOURCE_LABEL, SOURCE_ORDER } from '../../lib/mediaTags'
import PoolGroupCard from './PoolGroupCard'

const { Text } = Typography

/** 来源分栏：平级的来源页签（点击切换）+ 页签右侧的筛选；下方是当前来源的分组卡。 */
export default function PoolSources({
  groups,
  onlyNew,
  onOnlyNewChange,
  activeSource,
  onActiveSourceChange,
  busyKey,
  keyOf,
  onArchive,
  onLink,
  onIgnoreEntry,
  onIgnoreGroup,
}: {
  groups: PoolGroup[]
  onlyNew: boolean
  onOnlyNewChange: (v: boolean) => void
  activeSource: string
  onActiveSourceChange: (k: string) => void
  busyKey: string | null
  keyOf: (e: PoolEntry) => string
  onArchive: (e: PoolEntry) => void
  onLink: (e: PoolEntry) => void
  onIgnoreEntry: (e: PoolEntry) => void
  onIgnoreGroup: (g: PoolGroup) => void
}) {
  const visibleUnlinked = (g: PoolGroup) => (onlyNew ? g.unlinked.filter((e) => e.isNew) : g.unlinked)
  const srcGroups = (src: string) =>
    groups.filter((g) => g.source === src && visibleUnlinked(g).length > 0)
  const srcUnlinked = (src: string) =>
    srcGroups(src).reduce((n, g) => n + (onlyNew ? g.newCount : g.unlinked.length), 0)
  const active = srcGroups(activeSource)

  return (
    <>
      <Tabs
        type="card"
        activeKey={activeSource}
        onChange={(k) => onActiveSourceChange(String(k))}
        tabList={SOURCE_ORDER.map((src) => ({
          itemKey: src,
          tab: (
            <span>
              {SOURCE_LABEL[src] ?? src}
              <Text type="tertiary" size="small" style={{ marginLeft: 6 }}>
                {srcUnlinked(src)}
              </Text>
            </span>
          ),
        }))}
        tabBarExtraContent={
          <Select<string>
            size="small"
            value={onlyNew ? 'new' : 'all'}
            onChange={(v) => onOnlyNewChange(String(v) === 'new')}
            optionList={[
              { value: 'all', label: '全部未挂链' },
              { value: 'new', label: '只看新入库' },
            ]}
            style={{ width: 140 }}
          />
        }
      />
      <div className="pool-tab-body">
        {active.length === 0 && <Empty style={{ margin: 32 }} description="这个来源没有待处理的条目" />}
        {active.map((g) => (
          <PoolGroupCard
            key={`${g.source}|${g.containerKey}`}
            group={g}
            items={visibleUnlinked(g)}
            busyKey={busyKey}
            keyOf={keyOf}
            onArchive={onArchive}
            onLink={onLink}
            onIgnoreEntry={onIgnoreEntry}
            onIgnoreGroup={onIgnoreGroup}
          />
        ))}
      </div>
    </>
  )
}
