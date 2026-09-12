import { Tag, Typography } from '@douyinfe/semi-ui'
import type { PoolEntry } from '../../types'
import { FORMAT_LABEL, KIND_LABEL } from '../../lib/mediaTags'

const { Text } = Typography

/** 池子里每行的「形态 / 类别 / 来源信息 / 年份 / 新入库」标签行。 */
export function PoolEntryMeta({ entry }: { entry: PoolEntry }) {
  return (
    <Text type="tertiary" size="small">
      {entry.format ? (
        <Tag size="small" color="cyan" style={{ marginRight: 6 }}>
          {FORMAT_LABEL[entry.format] ?? entry.format}
        </Tag>
      ) : null}
      {entry.kind ? (
        <Tag size="small" color="blue" style={{ marginRight: 6 }}>
          {KIND_LABEL[entry.kind] ?? entry.kind}
        </Tag>
      ) : null}
      {entry.extra ? `${entry.extra} · ` : ''}
      {entry.releaseDate ? String(entry.releaseDate).slice(0, 4) : '日期未知'}
      {entry.isNew ? (
        <Tag size="small" color="orange" style={{ marginLeft: 6 }}>
          新
        </Tag>
      ) : null}
    </Text>
  )
}

/** 池子条目的标准行：封面 + 标题/标签 + 「打开」深链 + 调用方给的动作按钮。 */
export default function PoolEntryRow({
  entry,
  actions,
}: {
  entry: PoolEntry
  actions?: React.ReactNode
}) {
  return (
    <div className="ga-suggest-row">
      <span className="ga-suggest-cover">
        {entry.coverImage ? <img src={entry.coverImage} alt="" loading="lazy" /> : null}
      </span>
      <span className="ga-suggest-meta">
        <span className="ga-suggest-title" title={entry.title}>
          {entry.title}
        </span>
        <PoolEntryMeta entry={entry} />
      </span>
      <a className="ga-suggest-link" href={entry.url} target="_blank" rel="noreferrer">
        打开
      </a>
      {actions}
    </div>
  )
}
