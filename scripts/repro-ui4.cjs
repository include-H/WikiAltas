#!/usr/bin/env node
/* 第四轮：验证两个修复。
   A. 消息操作条只剩一个"复制"，悬停可点，点击出 toast"已复制"。
   B. 上下文环紧挨发送键（水平间距 ≤12px、竖直居中对齐），且不再居中漂浮。 */
const path = require('path')
const fs = require('fs')
const PW_DIR = '/root/.npm/_npx/e41f203b7505f1fb/node_modules/playwright'
const { chromium } = require(PW_DIR)
const SHOTS = '/root/WikiAltas/.shots'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

async function main() {
  fs.mkdirSync(SHOTS, { recursive: true })
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
    await sleep(1000)
  }
  const float = page.locator('.ai-float-btn')
  if (await float.count()) { await float.click(); await sleep(800) }
  await page.waitForSelector('.ai-panel', { timeout: 10000 })
  await sleep(1500)

  log('══ A. 操作条 ══')
  const containers = page.locator('.ai-panel [class*="ai-chat-dialogue-container"]')
  const cc = await containers.count()
  let totalBtns = 0
  for (let i = 0; i < cc; i++) {
    const c = containers.nth(i)
    await c.hover()
    await sleep(250)
    const btns = c.locator('button[class*="action"]')
    totalBtns += await btns.count()
    const n = await btns.count()
    if (n > 1) problems.push(`消息 #${i} 有 ${n} 个操作按钮（应该 ≤1）`)
    for (let j = 0; j < n; j++) {
      const icon = await btns.nth(j).evaluate((el) => String(el.querySelector('[class*="semi-icon-"]')?.className || '')).catch(() => '')
      if (!icon.includes('copy')) problems.push(`消息 #${i} 按钮[${j}] 图标不是复制：${icon}`)
    }
  }
  log(`  ${cc} 条消息共 ${totalBtns} 个操作按钮`)
  // 点最后一条的复制 → toast 文本
  const last = containers.last()
  await last.hover()
  await sleep(300)
  const copy = last.locator('button[class*="action"]').first()
  if (await copy.count()) {
    await copy.click({ timeout: 3000 }).catch((e) => problems.push('复制按钮点击失败：' + String(e).split('\n')[0]))
    await sleep(700)
    const toastText = await page.locator('.semi-toast-content').first().innerText().catch(() => '')
    log(`  点击后 toast = ${JSON.stringify(toastText)}`)
    if (!toastText.includes('已复制')) problems.push(`复制无 toast 回执（拿到 ${JSON.stringify(toastText)}）`)
  } else {
    problems.push('最后一条消息没有复制按钮')
  }

  log('\n══ B. 输入区布局 ══')
  const geo = await page.evaluate(() => {
    const q = (s) => {
      const el = document.querySelector(s)
      if (!el) return null
      const r = el.getBoundingClientRect()
      return { x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height) }
    }
    return { meter: q('.ctx-meter'), send: q('.semi-aiChatInput-footer-action-send'), input: q('.ai-panel-input') }
  })
  log(`  meter=${JSON.stringify(geo.meter)} send=${JSON.stringify(geo.send)}`)
  if (geo.meter && geo.send) {
    const gap = geo.send.x - (geo.meter.x + geo.meter.w)
    const cy1 = geo.meter.y + geo.meter.h / 2
    const cy2 = geo.send.y + geo.send.h / 2
    log(`  水平间隙=${gap}px 竖直中心差=${Math.abs(cy1 - cy2)}px`)
    if (gap < 0 || gap > 14) problems.push(`环与发送键间距 ${gap}px 不在 0..14`)
    if (Math.abs(cy1 - cy2) > 4) problems.push(`环与发送键竖直没对齐（差 ${Math.abs(cy1 - cy2)}px）`)
    const inputCenter = geo.input.x + geo.input.w / 2
    if (Math.abs(geo.meter.x + geo.meter.w / 2 - inputCenter) < 40) problems.push('环还在整行中间（居中现象未修）')
  } else {
    problems.push('取不到 meter/send 的盒（meter 可能没渲染）')
  }
  const inputBox = geo.input
  if (inputBox) {
    await page.screenshot({ path: path.join(SHOTS, 'repro4-input.png'), clip: { x: inputBox.x - 10, y: inputBox.y - 10, width: inputBox.w + 20, height: inputBox.h + 20 } }).catch(() => {})
    log(`  截图 → ${SHOTS}/repro4-input.png`)
  }

  log('\n══ 结论 ══')
  if (problems.length === 0) log('  全部通过。')
  else for (const p of problems) log('  [问题] ' + p)
  await browser.close()
  process.exit(problems.length ? 1 : 0)
}
main().catch((e) => { console.error(e); process.exit(1) })
