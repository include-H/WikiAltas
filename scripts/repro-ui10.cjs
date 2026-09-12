#!/usr/bin/env node
/* 取证"点击大纲时的抖动/像是容器放大"：点一条大纲，~1.2s 内每 120ms 记录
   关键容器的盒模型（大纲栏、大纲列表、滚动容器、正文首标题），并截 3 张图。 */
const PW_DIR = '/root/.npm/_npx/e41f203b7505f1fb/node_modules/playwright'
const { chromium } = require(PW_DIR)
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const SHOTS = '/root/WikiAltas/.shots'

async function main() {
  const browser = await chromium.launch({ headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] })
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
  await page.goto('http://127.0.0.1:5173', { waitUntil: 'domcontentloaded' })
  await sleep(1100)
  const pw = page.locator('input[placeholder="请输入访问密码"]')
  if (!(await pw.count())) { await page.goto('http://127.0.0.1:5173/login', { waitUntil: 'domcontentloaded' }); await sleep(900) }
  if (await pw.count()) {
    await pw.fill('1234')
    await page.getByRole('button', { name: '进入管理态' }).click()
    await sleep(1700)
  }
  await page.goto('http://127.0.0.1:5173/w/01a090d0-c08d-70c6-945f-b5b62201285e', { waitUntil: 'domcontentloaded' })
  await sleep(2500)

  const rows = await page.locator('.outline-list .outline-item').count()
  console.log(`初始大纲行数 = ${rows}（默认折叠应只露顶层）`)
  if (rows === 0) { console.log('没有大纲行'); await browser.close(); return }

  const sample = async (label) => {
    const boxes = await page.evaluate(() => {
      const q = (s) => {
        const el = document.querySelector(s)
        if (!el) return null
        const r = el.getBoundingClientRect()
        return `${Math.round(r.x)},${Math.round(r.y)} ${Math.round(r.width)}x${Math.round(r.height)}`
      }
      return {
        pane: q('.outline-pane'),
        list: q('.outline-list'),
        scroll: q('#content-scroll'),
        content: q('.work-content'),
        editor: q('.block-editor-inner, .bn-editor, .ProseMirror'),
        firstHeading: q('#content-scroll h1, #content-scroll h2'),
      }
    })
    console.log(`[${label}] pane=${boxes.pane} | list=${boxes.list}`)
    console.log(`         scroll=${boxes.scroll} | content=${boxes.content}`)
    console.log(`         editor=${boxes.editor} | firstHeading=${boxes.firstHeading}`)
  }

  await sample('点击前')
  const target = page.locator('.outline-list .outline-item').nth(3)
  const ttxt = await target.innerText().catch(() => '')
  console.log(`点击第 4 行：${JSON.stringify(ttxt.slice(0, 30))}`)
  await target.click()
  await sleep(150); await sample('+150ms')
  await page.screenshot({ path: `${SHOTS}/jitter-150.png`, clip: { x: 0, y: 60, width: 720, height: 800 } }).catch(() => {})
  await sleep(250); await sample('+400ms')
  await page.screenshot({ path: `${SHOTS}/jitter-400.png`, clip: { x: 0, y: 60, width: 720, height: 800 } }).catch(() => {})
  await sleep(500); await sample('+900ms')
  await page.screenshot({ path: `${SHOTS}/jitter-900.png`, clip: { x: 0, y: 60, width: 720, height: 800 } }).catch(() => {})
  const rows2 = await page.locator('.outline-list .outline-item').count()
  console.log(`点击后大纲行数 = ${rows2}（应因展开变多）`)
  await browser.close()
}
main().catch((e) => { console.error(e); process.exit(1) })
