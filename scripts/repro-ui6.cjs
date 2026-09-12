#!/usr/bin/env node
/* 第六轮：验证"选中不再自动引用、显式动作仍然可引用"。
   1. 进王者之剑页面 → 在正文里拖选一段 → 面板输入区**不该**出现"已引用选段"芯片。
   2. 悬停让正文工具栏浮出 → 点「问 Altas（@ 选段）」→ 芯片**应该**出现。 */
const PW_DIR = '/root/.npm/_npx/e41f203b7505f1fb/node_modules/playwright'
const { chromium } = require(PW_DIR)
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const WORK = 'http://127.0.0.1:5173/w/01a093ca-0b19-735f-a55f-dced09f970c8'

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
  await page.goto(WORK, { waitUntil: 'domcontentloaded' })
  await sleep(2500)
  // 开面板
  const float = page.locator('.ai-float-btn')
  if (await float.count()) { await float.click(); await sleep(800) }
  await page.waitForSelector('.ai-panel', { timeout: 10000 })

  // 找到正文里的可编辑区域
  const para = page.locator('.work-view [contenteditable="true"] p, .work-view .bn-block-content').first()
  await para.waitFor({ timeout: 10000 })
  const box = await para.boundingBox()
  log(`正文段落盒 = ${JSON.stringify(box)}`)
  if (!box) { log('取不到正文段落'); await browser.close(); process.exit(1) }

  // 1) 拖选一段
  await page.mouse.move(box.x + 6, box.y + 10)
  await page.mouse.down()
  await page.mouse.move(box.x + Math.min(300, box.width - 10), box.y + 10, { steps: 12 })
  await page.mouse.up()
  await sleep(900)
  const chipAfterSelect = await page.locator('.ai-selection-chip').count()
  log(`拖选后 已引用选段芯片 = ${chipAfterSelect}（应为 0）`)
  if (chipAfterSelect !== 0) problems.push('选中仍然自动进聊天框（芯片出现了）')

  // 2) 点工具栏「问 Altas」（用键盘选择：扩展对键盘选区无条件显示）
  const editables = await page.locator('.work-view [contenteditable="true"]').count()
  log(`正文 contenteditable 数 = ${editables}`)
  await para.click().catch(() => {})
  await sleep(400)
  await page.keyboard.press('Control+a')
  await sleep(1000)
  const boldBtn = await page.locator('[aria-label="加粗"]').count()
  log(`工具栏任意按钮（加粗）数 = ${boldBtn}`)
  const askBtn = page.locator('[aria-label="问 Altas（@ 选段）"]')
  const toolbarCount = await askBtn.count()
  log(`工具栏「问 Altas」按钮数量 = ${toolbarCount}`)
  if (toolbarCount === 0) {
    problems.push('选中后工具栏没有浮出（或按钮找不到）')
  } else {
    await askBtn.first().click({ timeout: 3000 }).catch((e) => problems.push('点击问 Altas 失败：' + String(e).split('\n')[0]))
    await sleep(900)
    const chipAfterAsk = await page.locator('.ai-selection-chip').count()
    log(`点「问 Altas」后 芯片 = ${chipAfterAsk}（应为 1）`)
    if (chipAfterAsk !== 1) problems.push('显式路径失效：点「问 Altas」没有出引用芯片')
  }

  log('\n══ 结论 ══')
  if (!problems.length) log('  通过。')
  else for (const p of problems) log('  [问题] ' + p)
  await browser.close()
  process.exit(problems.length ? 1 : 0)
}
main().catch((e) => { console.error(e); process.exit(1) })
