#!/usr/bin/env node
/*
 * 逐屏截图，用来做视觉打磨：登录后把主要页面都拍一遍，落到 .shots/polish-*.png。
 * 用法: node scripts/shoot.cjs [--base http://127.0.0.1:5173]
 */
const path = require('path')
const fs = require('fs')
const PW = '/root/.npm/_npx/e41f203b7505f1fb/node_modules/playwright'
const { chromium } = require(PW)

const args = process.argv.slice(2)
const i = args.indexOf('--base')
const base = i >= 0 ? args[i + 1] : 'http://127.0.0.1:5173'
const SHOTS = '/root/WikiAltas/.shots'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

async function main() {
  fs.mkdirSync(SHOTS, { recursive: true })
  const browser = await chromium.launch({ headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] })
  const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } })

  // 登录
  await page.goto(base + '/login', { waitUntil: 'domcontentloaded' })
  await sleep(1200)
  const pw = page.locator('input[placeholder="请输入访问密码"]')
  if (await pw.count()) {
    await pw.fill('1234')
    await page.getByRole('button', { name: '进入管理态' }).click()
    await sleep(1800)
  }

  // 找一个有正文的作品
  const tree = await page.evaluate(async (b) => {
    const r = await fetch(b + '/api/tree')
    return r.ok ? r.json() : null
  }, base)
  const nodes = tree?.nodes ?? []
  const work = nodes.find((n) => n.kind === 'work' && n.hasContent) || nodes.find((n) => n.kind === 'work')
  const series = nodes.find((n) => n.kind === 'series' || n.kind === 'universe')

  const shots = [
    ['home', '/'],
    ['runs', '/runs'],
    ['settings', '/settings'],
    ['login', '/login'],
  ]
  if (work) shots.push(['work', `/w/${work.id}`])
  if (series) shots.push(['series', `/w/${series.id}`])

  // 资料夹页（空态也要看一眼）
  const folderHost = series || work
  if (folderHost) shots.push(['folder', `/w/${folderHost.id}/folder`])

  for (const [name, route] of shots) {
    await page.goto(base + route, { waitUntil: 'domcontentloaded' })
    await sleep(1800)
    await page.screenshot({ path: path.join(SHOTS, `polish-${name}.png`) })
    console.log(`  拍了 ${name}  ${route}`)
  }

  // AI 面板（空态 + 打开）
  await page.goto(base + '/', { waitUntil: 'domcontentloaded' })
  await sleep(1200)
  const float = page.locator('.ai-float-btn')
  if (await float.count()) {
    await float.click()
    await sleep(1200)
    await page.screenshot({ path: path.join(SHOTS, 'polish-ai-panel.png') })
    console.log('  拍了 ai-panel')
  }

  console.log(`\n截图目录: ${SHOTS}`)
  await browser.close()
}
main()
