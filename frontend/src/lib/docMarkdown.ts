// 正文标题（H1）与文档标题（works.title）的唯一真相约定：
//
//   content_md 允许以 "# 标题" 开头（skill / 馆员产出就是这样写的），
//   但编辑器把标题显示在正文上方的文档头里，所以：
//   · 载入编辑器前 strip 掉开头 H1（避免标题出现两次）
//   · 保存时 ensure 回来（保持 content_md 自包含，人和 LLM 都读得懂）
//
// 只处理文档最开头的那个 H1，正文中间的 # 不动。

/** 去掉开头的 H1（如果有）。 */
export function stripLeadingH1(markdown: string): string {
  const lines = markdown.split(/\r?\n/)
  let i = 0
  while (i < lines.length && lines[i].trim() === '') i += 1
  if (i < lines.length && /^#\s+/.test(lines[i])) {
    lines.splice(i, 1)
    while (i < lines.length && lines[i].trim() === '') {
      lines.splice(i, 1)
    }
  }
  return lines.join('\n')
}

/** 确保正文以 "# 标题" 开头，且标题与文档标题一致。 */
export function ensureLeadingH1(markdown: string, title: string): string {
  const clean = title.trim()
  if (!clean) return markdown
  const lines = markdown.split(/\r?\n/)
  let i = 0
  while (i < lines.length && lines[i].trim() === '') i += 1
  if (i < lines.length && /^#\s+/.test(lines[i])) {
    lines[i] = `# ${clean}`
    return lines.join('\n')
  }
  return `# ${clean}\n\n${lines.join('\n')}`
}
