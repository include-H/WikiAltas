// 题记排版：对齐 GameManager（Hao/GameManager frontend/src/utils/epigraph.ts +
// components/MarkdownRenderer.vue buildEpigraphHtml）的原始实现——
// 无底色无边框，只有一对对角装饰引号、居中排布、分行排版、署名右对齐。
//
// 核心观感来自萌娘百科「使命召唤：无尽战争」词条 → GameManager：**外文原文小字、中文译文大字**，
// 逐行交替。原文可能是日文、韩文、西里尔……不只拉丁——所以判定依据是**这一行是不是中文**，
// 而不是"是不是拉丁"。凡非中文书写系统一律降级为小字（原文），中文行放大为装饰大字（中译）。

/** display = 装饰大字（中文/中译）；latin = 小字（一切非中文原文）。 */
export type EpigraphLineType = 'display' | 'latin'

/** 拉丁字母（含拉丁扩展 A/B、重音字符）。 */
const LATIN_SCRIPT = /[A-Za-zÀ-ɏḀ-ỿ]/

/** 汉字（含扩展 A、兼容汉字）。这是"中文行"的判据。 */
const HAN_SCRIPT = /[㐀-䶿一-鿿豈-﫿]/

/** 明确的非中文书写系统：假名、韩文、西里尔、希腊、希伯来、阿拉伯、天城文、泰文。
 *  优先级高于汉字——日文行夹汉字，必须先靠假名认成日文，才能降到小字。 */
const NON_CHINESE_SCRIPT =
  /[぀-ヿㇰ-ㇿᄀ-ᇿ㄰-㆏가-힯Ѐ-ӿͰ-Ͽ֐-׿؀-ۿऀ-ॿ฀-๿]/

/** 分行/分段的句读标点（中文、日文、拉丁）。 */
const PUNCTUATION = '，。！？；：、,.!?;:｡､'

/**
 * 每一行的排版分类：
 * - 'display'：中文行（含汉字、中英混排）→ 装饰大字（中译）
 * - 'latin'  ：非中文原文（拉丁、日文、韩文、西里尔…）→ 小字（原文）
 *
 * 两个历史 bug 都源于判据选错：
 * ① 原实现用「ASCII 字母数 > 中文字符数」，把"Sam Fisher 依然更激进了，"这类中英混排误判成小字；
 * ② 曾把"非拉丁书写系统"当大字，日文原文（含假名）被一并放大，与其中译同为大字、字号对照消失。
 */
export function classifyEpigraphLine(line: string): EpigraphLineType {
  if (NON_CHINESE_SCRIPT.test(line)) return 'latin'
  if (HAN_SCRIPT.test(line)) return 'display'
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
