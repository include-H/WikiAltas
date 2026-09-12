#!/usr/bin/env node
/* 第九轮：run 边界行验证——对话前一行（已加载 skill…）、对话后一行（质量自检…），
   且这两条不再出现在浮层提醒里。 */
const PW_DIR = '/root/.npm/_npx/e41f203b7505f1fb/node_modules/playwright'
const { chromium } = require(PW_DIR)
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

async function main() {
  const browser = await chromium.launch({ headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] })
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
  const log = console.log
  const problems = []
  await page.goto('http://127.0.0.1:5173', { waitUntil: 'domcontentloaded' })
  await sleep(1200)
  const pw = page.locator('input[placeholder="请输入访问密码"]')
  if (!(await pw.count())) { await page.goto('http://127.0.0.1:5173/login', { waitUntil: 'domcontentloaded' }); await sleep(1000) }
  if (await pw.count()) {
    await pw.fill('1234')
    await page.getByRole('button', { name: '进入管理态' }).click()
    await sleep(1800)
  }
  await page.goto('http://127.0.0.1:5173/w/01a090d0-c08d-70c6-945f-b5b62201285e', { waitUntil: 'domcontentloaded' })
  await sleep(2500)
  const float = page.locator('.ai-float-btn')
  if (await float.count()) { await float.click(); await sleep(800) }
  await page.waitForSelector('.ai-panel', { timeout: 10000 })
  await sleep(4000)

  const rows = page.locator('.ai-panel .ai-turn-row')
  const n = await rows.count()
  log(`边界行数 = ${n}（期望 2）`)
  for (let i = 0; i < n; i++) {
    log(`  行[${i}]：${JSON.stringify((await rows.nth(i).innerText()).slice(0, 70))}`)
  }
  if (n !== 2) problems.push(`边界行应为 2 条，实际 ${n}`)
  if (n >= 1) {
    const first = await rows.first().innerText()
    if (!first.includes('已加载')) problems.push('第一条边界行不是"已加载 skill"')
  }
  if (n >= 2) {
    const last = await rows.last().innerText()
    if (!last.includes('质量自检')) problems.push('最后一条边界行不是"质量自检"')
  }
  // 浮层里不该再有这两条
  const dockText = await page.locator('.ai-panel .notice-dock').innerText().catch(() => '')
  log(`浮层 dock 文本 = ${JSON.stringify(dockText.slice(0, 120))}`)
  if (dockText.includes('已加载 wiki-writing') || dockText.includes('质量自检通过')) {
    problems.push('边界事实仍出现在浮层 dock 里（重复显示）')
  }
  // DOM 位置：head 行应排在内容前面
  const order = await page.evaluate(() => {
    const nodes = [...document.querySelectorAll('.ai-panel [class*="dialogue-content"] *')]
    const firstRow = document.querySelector('.ai-panel .ai-turn-row')
    const firstTool = document.querySelector('.ai-panel .tool-card')
    if (!firstRow || !firstTool) return 'missing'
    return firstRow.compareDocumentPosition(firstTool) & Node.DOCUMENT_POSITION_FOLLOWING ? 'row-before-tool' : 'row-after-tool'
  })
  log(`位置关系 = ${order}（期望 row-before-tool）`)
  if (order !== 'row-before-tool') problems.push('边界行没有排在内容之前')

  const shot = await page.locator('.ai-panel').boundingBox()
  if (shot) await page.screenshot({ path: '/root/WikiAltas/.shots/turn-rows.png', clip: shot }).catch(() => {})
  log('截图 → .shots/turn-rows.png')

  log(problems.length ? problems.map((p) => '[问题] ' + p).join('\n') : '全部通过。')
  await browser.close()
  process.exit(problems.length ? 1 : 0)
}
main().catch((e) => { console.error(e); process.exit(1) })
