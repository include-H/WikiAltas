import type { RunIntent } from '../types'

/**
 * 一条消息是不是在让我**产出/改动内容**。
 *
 * 这是唯一的一维判断（"有没有在派活"），不是按话题分档——所以规则只有一条，
 * 而不是"哪些话题算写哪些算聊"的一堆例外。
 *
 * 两个容易划偏的地方，都在这条正则里：
 *   - `写` 后跟「得/的」是**评价**不是派活（"这句写得怎么样"）；
 *   - `补`/`改` 光带宾语就是派活（"补一下""改一句"），不能只认"补充/改写"。
 */
const WRITE_VERBS =
  /(写(?!得|的)|新建|建个|建立|建档|创建|新增|补(?!丁)|扩写|扩充|改写|重写|整理|完善|润色|修订|继续|接着|生成|做一?篇|来一?篇|起稿|落笔|加一?段|加一?章|调整|改)/

export interface IntentContext {
  docMode?: string
  workId?: string | null
  docId?: string | null
}

/**
 * 从上下文推工单意图。**默认是聊天，不是建档。**
 *
 * 旧实现把兜底写成 create_wiki：从设置页（路由里没有 workId）问一句「我是谁」，
 * 会被当成"建一篇新条目"，拉起整套 wiki-writing skill 去联网检索。
 *
 * 两个方向的误判代价并不对称：
 *   - 把"让我写东西"误判成聊天 → 用户重说一句，或者用页面上的显式按钮；
 *   - 把"聊一句"误判成建档 → 一整轮检索 + 写作，还往树里塞了个空条目。
 * 所以默认必须是聊天，"派活"才是例外——反过来才是安全的。
 *
 * 只读模式另说：那是"用户只让我看"，任何写意图都不成立。
 */
export function inferIntent(goal: string, ctx: IntentContext): RunIntent {
  if (ctx.docMode === 'read') return 'answer'
  if (!WRITE_VERBS.test(goal)) return 'answer'
  if (ctx.docId) return 'write_doc'
  if (ctx.workId) return 'continue_wiki'
  return 'create_wiki'
}
