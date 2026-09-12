#!/usr/bin/env node
/* 第五轮：真验证复制——授予剪贴板权限，点击复制，回读剪贴板内容比对。 */
const PW_DIR = '/root/.npm/_npx/e41f203b7505f1fb/node_modules/playwright'
const { chromium } = require(PW_DIR)
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

async function main() {
  const browser = await chromium.launch({ headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] })
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    permissions: ['clipboard-read', 'clipboard-write'],
  })
  const page = await context.newPage()
  const log = console.log
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

  const containers = page.locator('.ai-panel [class*="ai-chat-dialogue-container"]')
  const last = containers.last()
  await last.scrollIntoViewIfNeeded().catch(() => {})
  await last.hover()
  await sleep(400)
  const msgText = (await last.innerText().catch(() => '')).trim()
  const copy = last.locator('button[class*="action"]').first()
  if (!(await copy.count())) {
    log('最后一条消息没有操作按钮'); await browser.close(); process.exit(1)
  }
  await copy.click({ timeout: 3000 })
  await sleep(600)
  const toast = await page.locator('.semi-toast-content').first().innerText().catch(() => '')
  const clip = await page.evaluate(() => navigator.clipboard.readText().catch(() => '<读取失败>'))
  log(`toast = ${JSON.stringify(toast)}`)
  log(`剪贴板长度 = ${clip.length}，消息文本长度 = ${msgText.length}`)
  log(`剪贴板内容前 80 字 = ${JSON.stringify(clip.slice(0, 80))}`)
  const ok = toast.includes('已复制') && clip.length > 10
  log(ok ? '\n  通过：复制进了剪贴板且有回执。' : '\n  [问题] 复制未通过。')
  await browser.close()
  process.exit(ok ? 0 : 1)
}
main().catch((e) => { console.error(e); process.exit(1) })
