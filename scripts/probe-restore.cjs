#!/usr/bin/env node
/* 探针：打开面板走"恢复会话"路径，量文本渲染；检查 Vite 错误浮层与 React 报错。 */
const PW_DIR = '/root/.npm/_npx/e41f203b7505f1fb/node_modules/playwright'
const { chromium } = require(PW_DIR)
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

async function main() {
  const browser = await chromium.launch({ headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] })
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
  const errs = []
  page.on('pageerror', (e) => errs.push(String(e).slice(0, 200)))
  page.on('console', (m) => { if (m.type() === 'error') errs.push('[console] ' + m.text().slice(0, 200)) })
  await page.goto(process.env.PROBE_URL || 'http://127.0.0.1:5173', { waitUntil: 'domcontentloaded' })
  await sleep(1200)
  const pw = page.locator('input[placeholder="请输入访问密码"]')
  if (!(await pw.count())) { await page.goto('http://127.0.0.1:5173/login', { waitUntil: 'domcontentloaded' }); await sleep(1000) }
  if (await pw.count()) {
    await pw.fill('1234')
    await page.getByRole('button', { name: '进入管理态' }).click()
    await sleep(1800)
    await page.goto(process.env.PROBE_URL || 'http://127.0.0.1:5173', { waitUntil: 'domcontentloaded' })
    await sleep(1000)
  }
  const float = page.locator('.ai-float-btn')
  if (await float.count()) { await float.click(); await sleep(800) }
  await page.waitForSelector('.ai-panel', { timeout: 10000 })
  // 等召回（恢复路径会拉 grecent runs + events）
  for (let i = 0; i < 15; i++) {
    await sleep(1000)
    const t = await page.locator('.ai-panel').innerText().catch(() => '')
    if (t.length > 300) break
  }
  const overlay = await page.locator('vite-error-overlay').count()
  const txt = await page.locator('.ai-panel').innerText().catch(() => '')
  // 防"刷新后又把已完成的长消息打字机重打一遍"：面板出文本后再采两次，
  // 两次之间不该有明显增长（历史路径应当一次性到位）。
  const l0 = txt.length
  await sleep(1800)
  const l1 = (await page.locator('.ai-panel').innerText().catch(() => '')).length
  console.log(`历史路径增长检查：1.8s 内 +${l1 - l0} 字（应接近 0；几十以上说明在重放打字机）`)
  const containers = await page.locator('.ai-panel [class*="ai-chat-dialogue-container"]').count()
  console.log(`Vite 错误浮层 = ${overlay}`)
  console.log(`消息容器数 = ${containers}`)
  console.log(`面板文本长度 = ${txt.length}`)
  console.log(`文本尾 160 字 = ${JSON.stringify(txt.slice(-160))}`)
  if (errs.length) { console.log('页面错误：'); for (const e of errs.slice(0, 5)) console.log('  ' + e) }
  console.log(errs.length === 0 && txt.length > 300 ? '探针结论：恢复路径有文本、无报错。' : '探针结论：有问题，见上。')
  await browser.close()
}
main().catch((e) => { console.error(e); process.exit(1) })
