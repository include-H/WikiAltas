import { Typography } from '@douyinfe/semi-ui'

const { Text } = Typography

export default function NarrativeLine({ text }: { text: string }) {
  return (
    <div className="narrative-line">
      <Text>{text}</Text>
    </div>
  )
}
