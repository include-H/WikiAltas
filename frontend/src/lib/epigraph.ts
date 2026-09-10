// 题记排版：对齐 GameManager（Hao/GameManager frontend/src/utils/epigraph.ts +
// components/MarkdownRenderer.vue buildEpigraphHtml）的原始实现——
// 无底色无边框，只有一对对角装饰引号、居中排布、分行排版、署名右对齐。
//
// 相对于原实现的语言扩展：题记不只有中英，日文（假名）、韩文、西里尔、
// 希腊、阿拉伯、希伯来、天城文、泰文等非拉丁书写系统一律按"装饰大字"处理；
// 只有纯拉丁字母（含重音扩展）的行才降级为小字。

/** display = 装饰大字（CJK/日文/韩文/西里尔…）；latin = 小字（纯拉丁文本）。 */
export type EpigraphLineType = 'display' | 'latin'

/** 拉丁字母（含拉丁扩展 A/B、重音字符）。 */
const LATIN_SCRIPT = /[A-Za-z\u00c0-\u024f\u1e00-\u1eff]/

/** 非拉丁书写系统：平假名/片假名、CJK、韩文、西里尔、希腊、希伯来、阿拉伯、天城文、泰文、兼容汉字。 */
const NON_LATIN_SCRIPT =
  /[\u3040-\u30ff\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff\uac00-\ud7af\u0400-\u04ff\u0370-\u03ff\u0590-\u05ff\u0600-\u06ff\u0900-\u097f\u0e00-\u0e7f]/

/** 分行/分段的句读标点（中文、日文、拉丁）。 */
const PUNCTUATION = '，。！？；：、,.!?;:｡､'

/**
 * 每一行的排版分类：
 * - 'en'：整行为纯英文（含数字/标点）→ 小字
 * - 'cn'：含中文 / 中英混排 / 无字母 → 装饰性大字
 *
 * 历史 bug（原实现注释）：曾用「ASCII 字母数 > 中文字符数」判定，
 * 导致"Sam Fisher 依然更激进了，"这类中英混排行被误判为纯英文小字。
 */
export function classifyEpigraphLine(line: string): EpigraphLineType {
  if (NON_LATIN_SCRIPT.test(line)) return 'display'
  if (LATIN_SCRIPT.test(line)) return 'latin'
  // 纯数字/符号行按大字处理（与原始实现一致）
  return 'display'
}

/** 中文行按标点切成"不换行小段"，让居中排版在换行处更整齐。 */
export function splitCnSegments(line: string): { text: string; punctuation: string }[] {
  const normalized = line.replace(/\s+/g, '')
  const segments = normalized
    .split(new RegExp(`(?<=[${PUNCTUATION}])`, 'u'))
    .map((s) => s.trim())
    .filter((s) => s.length > 0)
  const parts = segments.length > 0 ? segments : [normalized]
  return parts.map((part) => {
    const m = part.match(new RegExp(`^(.*?)([${PUNCTUATION}]+)?$`, 'u'))
    return { text: m?.[1] ?? part, punctuation: m?.[2] ?? '' }
  })
}

/** 按内容长度选字号/字距（原实现的三档规则）。 */
export function cnTypography(line: string): { fontSize: string; letterSpacing: string } {
  const contentLength = line
    .replace(/[\s，。！？；：、｡､、“”‘’「」『』（）()《》〈〉【】〔〕,.!?;:]/gu, '')
    .length
  if (contentLength >= 42) {
    return { fontSize: 'clamp(0.92rem, 0.72vw + 0.68rem, 1.08rem)', letterSpacing: '0.03em' }
  }
  if (contentLength >= 24) {
    return { fontSize: 'clamp(0.98rem, 0.9vw + 0.66rem, 1.2rem)', letterSpacing: '0.05em' }
  }
  return { fontSize: 'clamp(1.04rem, 1.15vw + 0.68rem, 1.32rem)', letterSpacing: '0.07em' }
}

export interface EpigraphLine {
  type: EpigraphLineType
  text: string
  segments: { text: string; punctuation: string }[]
  style: { fontSize: string; letterSpacing: string }
}

export interface Epigraph {
  lines: EpigraphLine[]
  author: string
}

const AUTHOR_LINE = /^(?:--|---|——)\s*(.+)$/

/** 题记围栏内容 → 排版模型（最后一行以 ——/--/--- 开头时视为署名）。 */
export function parseEpigraph(text: string): Epigraph {
  const lines = text
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean)
  let author = ''
  const last = lines[lines.length - 1]
  if (last) {
    const m = last.match(AUTHOR_LINE)
    if (m) {
      author = m[1].trim()
      lines.pop()
    }
  }
  return {
    author,
    lines: lines.map((line) => {
      const type = classifyEpigraphLine(line)
      return {
        type,
        text: line,
        segments: type === 'display' ? splitCnSegments(line) : [],
        style: cnTypography(line),
      }
    }),
  }
}
