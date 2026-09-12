#!/usr/bin/env node
/* 第七轮：平滑的回归测试（会真的发一个小工单——answer 型，便宜）。
   1. 流式期间采样最后一条消息的文本长度：应有多次小步增长（打字机感）。
   2. 工单结束后等排空，记下文本；刷新页面（走历史回放、无平滑）再读一遍：
      两者必须一致——防"水位线卡住导致正文永久截断"这个最危险的失效。 */
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
    await page.goto('http://127.0.0.1:5173', { waitUntil: 'domcontentloaded' })
    await sleep(1200)
  }
  const float = page.locator('.ai-float-btn')
  if (await float.count()) { await float.click(); await sleep(800) }
  await page.waitForSelector('.ai-panel', { timeout: 10000 })
  await sleep(1500)

  const containers = page.locator('.ai-panel [class*="ai-chat-dialogue-container"]')
  const before = await containers.count()
  const editor = page.locator('.ai-panel [contenteditable="true"]').first()
  await editor.click()
  await page.keyboard.type('用三句话介绍你自己', { delay: 8 })
  await page.keyboard.press('Enter')
  log('已发送，开始采样最后一条消息的文本增长…')

  const samples = []
  let lastLen = -1
  let stableAt = Date.now()
  const t0 = Date.now()
  while (Date.now() - t0 < 180000) {
    await sleep(200)
    const n = await containers.count()
    if (n <= before) continue
    const txt = await containers.nth(n - 1).innerText().catch(() => '')
    const len = txt.trim().length
    if (len !== lastLen) {
      samples.push(len)
      lastLen = len
      stableAt = Date.now()
    } else if (len > 40 && Date.now() - stableAt > 8000) {
      break // 有实质内容且稳定 8 秒 → 视为本轮结束并排空
    }
  }
  // 再等 4 秒确保平滑层排空
  await sleep(4000)
  const n = await containers.count()
  const finalTxt = (await containers.nth(n - 1).innerText().catch(() => '')).trim()
  const steps = samples.filter((v, i) => i > 0 && v > samples[i - 1]).length
  log(`采样到 ${samples.length} 个不同的长度、其中增长步 ${steps} 次`)
  log(`长度序列（尾 12 个）= ${samples.slice(-12).join(', ')}`)
  log(`完成后文本尾 40 字 = ${JSON.stringify(finalTxt.slice(-40))}`)
  if (steps < 4) problems.push(`增长步只有 ${steps} 次——像是整段砸下来，不是打字机`)

  // 刷新 → 历史回放路径（无平滑）→ 对照
  await page.reload({ waitUntil: 'domcontentloaded' })
  await sleep(2500)
  const float2 = page.locator('.ai-float-btn')
  if (await float2.count()) { await float2.click(); await sleep(800) }
  await page.waitForSelector('.ai-panel', { timeout: 10000 })
  await sleep(3500)
  const containers2 = page.locator('.ai-panel [class*="ai-chat-dialogue-container"]')
  const n2 = await containers2.count()
  const histTxt = (await containers2.nth(n2 - 1).innerText().catch(() => '')).trim()
  log(`刷新后（历史路径）文本尾 40 字 = ${JSON.stringify(histTxt.slice(-40))}`)
  if (!histTxt.endsWith(finalTxt.slice(-24))) {
    problems.push('平滑结束后与历史回放不一致——可能有永久截断！')
  }

  log('\n══ 结论 ══')
  if (!problems.length) log('  通过。')
  else for (const p of problems) log('  [问题] ' + p)
  await browser.close()
  process.exit(problems.length ? 1 : 0)
}
main().catch((e) => { console.error(e); process.exit(1) })
