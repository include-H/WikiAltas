#!/usr/bin/env node
/* 取证"滑动时抖动"：滚轮逐格滚正文，每格记录 scrollTop、当前高亮的大纲行、
   大纲各行的盒模型——看高亮是不是在相邻行之间来回闪、有没有行在变形。 */
const PW_DIR = '/root/.npm/_npx/e41f203b7505f1fb/node_modules/playwright'
const { chromium } = require(PW_DIR)
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

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

  // 悬停到正文中间，滚轮从这里滚
  await page.mouse.move(900, 500)

  const snap = async () => page.evaluate(() => {
    const scroller = document.getElementById('content-scroll')
    const items = [...document.querySelectorAll('.outline-list .outline-item')]
    const activeIdx = items.findIndex((el) => el.classList.contains('active'))
    const activeText = activeIdx >= 0 ? [items[activeIdx].querySelector('.outline-number')?.textContent, items[activeIdx].querySelector('.outline-text')?.textContent].join('') : '(无)'
    // 抽查前 6 行的盒子（有没有行在变尺寸/位移）
    const boxes = items.slice(0, 6).map((el) => {
      const r = el.getBoundingClientRect()
      return `${Math.round(r.x)},${Math.round(r.y)},${Math.round(r.width)}x${Math.round(r.height)}`
    })
    return { top: Math.round(scroller?.scrollTop ?? -1), activeIdx, activeText, boxes: boxes.join(' | ') }
  })

  const seq = []
  for (let i = 0; i < 14; i++) {
    await page.mouse.wheel(0, 500)
    await sleep(220)
    const s = await snap()
    seq.push(s)
    console.log(`#${String(i).padStart(2)} top=${String(s.top).padStart(6)} active=[${s.activeIdx}] ${JSON.stringify(s.activeText)}`)
    if (i === 6) console.log(`      boxes[0..5] = ${s.boxes}`)
  }
  // 找"来回跳"：相邻采样里 activeIdx 反向折返
  let bounces = 0
  for (let i = 2; i < seq.length; i++) {
    const a = seq[i - 2].activeIdx, b = seq[i - 1].activeIdx, c = seq[i].activeIdx
    if (a === c && b !== a) bounces++
  }
  console.log(`\n来回跳（A→B→A）次数 = ${bounces}（>0 即高亮在闪）`)
  const uniqueBoxes = new Set(seq.map((s) => s.boxes))
  console.log(`出现的行盒布局种类 = ${uniqueBoxes.size}（>1 说明行在变尺寸/位移）`)
  await browser.close()
}
main().catch((e) => { console.error(e); process.exit(1) })
