// 媒体库组件共用的标签词表与配色（来源 / 类别 / 形态 / 可信度）。
//
// 配色纪律（VISUAL_SPEC §2）：紫色是 AI 唯一强调色，媒体库这些展示性标签
// 一律不许用紫系（purple / violet / indigo）——kind=blue、format=cyan、
// source=grey（中性，不跟类别色抢视线）、可信度=绿/橙/灰三档。
export const SOURCE_LABEL: Record<string, string> = {
  gameatlas: 'GameAtlas',
  emby: 'Emby',
  komga: 'Komga',
}

/** 分栏顺序：与后端 sourceOrder 一致。 */
export const SOURCE_ORDER = ['gameatlas', 'emby', 'komga'] as const

export const KIND_LABEL: Record<string, string> = {
  game: '游戏',
  tv: '剧集',
  series: '剧集', // Emby 搜索结果的 kind 值
  movie: '电影',
  book: '书籍',
  collection: '合集',
  album: '专辑',
}

export const FORMAT_LABEL: Record<string, string> = {
  comic: '漫画',
  novel: '小说',
}

/** 可信度 → Tag 颜色：≥0.85 绿 / ≥0.6 橙 / 其余灰。 */
export function confColor(confidence: number): 'green' | 'orange' | 'grey' {
  if (confidence >= 0.85) return 'green'
  if (confidence >= 0.6) return 'orange'
  return 'grey'
}
