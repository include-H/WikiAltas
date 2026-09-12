import { Button, Card, Tag, Typography } from '@douyinfe/semi-ui'
import type { PoolAiSuggestion, PoolEntry } from '../../types'
import { confColor } from '../../lib/mediaTags'

const { Text } = Typography

function actionText(sug: PoolAiSuggestion): string {
  switch (sug.action) {
    case 'link':
      return `建议关联到《${sug.targetTitle}》`
    case 'archive':
      return `建议建档到《${sug.targetTitle}》`
    default:
      return '建议忽略'
  }
}

/** AI 建议卡：只建议、点「确认」才执行。 */
export default function PoolAiCard({
  suggestions,
  busyKey,
  keyOf,
  onConfirm,
}: {
  suggestions: PoolAiSuggestion[]
  busyKey: string | null
  keyOf: (e: PoolEntry) => string
  onConfirm: (sug: PoolAiSuggestion) => void
}) {
  if (suggestions.length === 0) return null
  return (
    <Card
      className="pool-card"
      title={
        <span>
          AI 建议
          <Text type="tertiary" size="small" style={{ marginLeft: 8 }}>
            只建议，点「确认」才执行
          </Text>
        </span>
      }
      headerExtraContent={
        <Text type="tertiary" size="small">
          {suggestions.length} 条
        </Text>
      }
    >
      {suggestions.map((sug) => (
        <div className="ga-suggest-row" key={keyOf(sug.entry)}>
          <span className="ga-suggest-cover">
            {sug.entry.coverImage ? <img src={sug.entry.coverImage} alt="" loading="lazy" /> : null}
          </span>
          <span className="ga-suggest-meta">
            <span className="ga-suggest-title" title={sug.entry.title}>
              {sug.entry.title}
            </span>
            <Text type="tertiary" size="small">
              <Tag size="small" color={confColor(sug.confidence)} style={{ marginRight: 6 }}>
                {Math.round(sug.confidence * 100)}%
              </Tag>
              {actionText(sug)}
              {sug.reason ? ` · ${sug.reason}` : ''}
            </Text>
          </span>
          <a className="ga-suggest-link" href={sug.entry.url} target="_blank" rel="noreferrer">
            打开
          </a>
          <Button
            size="small"
            theme="solid"
            type="primary"
            loading={busyKey === keyOf(sug.entry)}
            onClick={() => onConfirm(sug)}
          >
            确认
          </Button>
        </div>
      ))}
    </Card>
  )
}
