#!/usr/bin/env node
/*
 * 真实浏览器渲染验证：登录 → 开 AI 面板 → 真发一句话 → 看它渲染成什么样。
 *
 * 为什么需要它：归约、构建、流形状都验过，但 React 渲染层（prop 形状、
 * chats 结构、roleConfig）只有真跑一次才知道——之前就踩过
 * 「Cannot destructure property 'avatar' of 'role'」这种构建期查不出的崩。
 *
 * 用法：node scripts/verify-ui.cjs [--keep] [--base http://127.0.0.1:5173]
 * 退出码：0 通过；1 有硬问题（页面崩溃 / 没渲染出助手内容）。
 */
const path = require('path')
const fs = require('fs')

const PW_DIR = '/root/.npm/_npx/e41f203b7505f1fb/node_modules/playwright'
const { chromium } = require(PW_DIR)

const args = process.argv.slice(2)
const base = (() => {
  const i = args.indexOf('--base')
  return i >= 0 ? args[i + 1] : 'http://127.0.0.1:5173'
})()
const goal = (() => {
  const i = args.indexOf('--goal')
  return i >= 0 ? args[i + 1] : '用一句话回答：你现在能看到历史会话吗？不要修改任何内容。'
})()
// 多步目标会触发 todo_write → wikiatlas.todo → 任务面板；加了它就断言面板出现。
const expectTodo = args.includes('--expect-todo')
const SHOTS = '/root/WikiAltas/.shots'

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

async function main() {
  fs.mkdirSync(SHOTS, { recursive: true })
  const browser = await chromium.launch({
    headless: true,
    args: ['--no-sandbox', '--disable-dev-shm-usage'],
  })
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })

  const consoleErrors = []
  const pageErrors = []
  const badResponses = []
  page.on('console', (m) => {
    if (m.type() === 'error' || m.type() === 'warning') consoleErrors.push(`[${m.type()}] ${m.text()}`)
  })
  page.on('pageerror', (e) => pageErrors.push(String(e)))
  page.on('response', (r) => {
    if (r.status() >= 400) badResponses.push(`${r.status()} ${r.request().method()} ${r.url()}`)
  })
  page.on('requestfailed', (r) => badResponses.push(`FAILED ${r.method()} ${r.url()} ${r.failure()?.errorText || ''}`))

  const step = (s) => console.log(`  · ${s}`)

  try {
    step(`打开 ${base}`)
    await page.goto(base, { waitUntil: 'domcontentloaded', timeout: 30000 })
    await sleep(1200)

    // 1. 登录：新浏览器上下文是**访客态**，Home 对访客直接渲染，
    //    既没有登录框也没有 AI 悬浮键——所以必须显式去 /login。
    const pw = page.locator('input[placeholder="请输入访问密码"]')
    if (!(await pw.count())) {
      step('访客态 → /login')
      await page.goto(base + '/login', { waitUntil: 'domcontentloaded', timeout: 30000 })
      await sleep(1200)
    }
    if (await pw.count()) {
      step('登录')
      await pw.fill('1234')
      await page.getByRole('button', { name: '进入管理态' }).click()
      await sleep(1800)
      if (!page.url().endsWith('/login')) {
        await page.goto(base, { waitUntil: 'domcontentloaded', timeout: 30000 })
        await sleep(1000)
      }
    } else {
      step('已登录（或未出现登录框）')
    }

    // 2. 打开 AI 面板
    const float = page.locator('.ai-float-btn')
    if (await float.count()) {
      step('点开 AI 面板')
      await float.click()
      await sleep(800)
    }
    await page.waitForSelector('.ai-panel', { timeout: 10000 })
    step('面板已挂载')

    // 3. 发一句话（answer 型，最便宜）
    const editor = page.locator('.ai-panel [contenteditable="true"]').first()
    await editor.waitFor({ timeout: 10000 })
    const question = goal
    step(`输入并发送：${question}`)
    await editor.click()
    await page.keyboard.type(question, { delay: 5 })
    await page.keyboard.press('Enter')

    // 4. 等渲染：用户气泡 → 助手的思考/工具/消息。
    //    判据必须是"相对发送前**增长**"——面板召回的历史里本来就有"回答/已/思考"，
    //    按内容匹配会在发完 2 秒就误判完成（真实踩到：todo 事件还没到就去查 DOM）。
    step('等待助手输出…')
    const beforeLen = (await page.locator('.ai-panel').innerText().catch(() => '')).length
    let grew = false
    for (let i = 0; i < 90; i++) {
      await sleep(2000)
      const txt = await page.locator('.ai-panel').innerText().catch(() => '')
      if (txt.length > beforeLen + 200) grew = true
      const todoNow = expectTodo ? await page.locator('.ai-panel .todo-dock').count() : 0
      if (grew && (!expectTodo || todoNow > 0)) break
    }

    await page.screenshot({ path: path.join(SHOTS, 'verify-ui-panel.png'), fullPage: false })

    const panelText = await page.locator('.ai-panel').innerText().catch(() => '')
    const dialogueExists = await page.locator('.ai-panel [class*="ai-chat-dialogue"]').count()
    const bubbleCount = await page.locator('.ai-panel [class*="user-bubble"], .ai-panel [class*="userBubble"]').count()

    const todoDock = await page.locator('.ai-panel .todo-dock').count()
    const noticeDock = await page.locator('.ai-panel .notice-dock').count()

    console.log('\n── 渲染结果 ──')
    console.log(`  面板文本长度 = ${panelText.length}`)
    console.log(`  AIChatDialogue 节点 = ${dialogueExists}`)
    console.log(`  用户气泡节点 = ${bubbleCount}`)
    console.log(`  任务面板 .todo-dock = ${todoDock}`)
    console.log(`  提醒条 .notice-dock = ${noticeDock}`)
    console.log(`  文本是否增长 = ${grew}`)
    console.log('  ── 面板前 400 字 ──')
    console.log('  ' + panelText.slice(0, 400).replace(/\n/g, '\n  '))

    if (pageErrors.length) {
      console.log('\n  [失败] 页面抛错：')
      for (const e of pageErrors.slice(0, 5)) console.log('    ' + e.slice(0, 500))
    }
    const realConsole = consoleErrors.filter((t) => !/favicon|DevTools|Download the React/i.test(t))
    if (realConsole.length) {
      console.log(`\n  [告警] console（${realConsole.length} 条，去重后）：`)
      const seen = new Set()
      for (const e of realConsole) {
        const key = e.slice(0, 90)
        if (seen.has(key)) continue
        seen.add(key)
        console.log('    ' + e.slice(0, 300))
      }
    }
    if (badResponses.length) {
      console.log('\n  [告警] 非 2xx 请求：')
      for (const b of [...new Set(badResponses)].slice(0, 10)) console.log('    ' + b)
    }
    const dupKeys = consoleErrors.filter((t) => /same key/i.test(t))
    if (dupKeys.length) {
      console.log(`\n  [失败] React 重复 key ${dupKeys.length} 条 —— 会导致子项重复/丢失`)
    }

    const hardFail =
      pageErrors.length > 0 ||
      dialogueExists === 0 ||
      panelText.length < 60 ||
      dupKeys.length > 0 ||
      (expectTodo && todoDock === 0)
    console.log(`\n  截图：${path.join(SHOTS, 'verify-ui-panel.png')}`)
    await browser.close()
    process.exit(hardFail ? 1 : 0)
  } catch (e) {
    console.error('\n  [失败] 脚本异常：', e)
    await page.screenshot({ path: path.join(SHOTS, 'verify-ui-error.png') }).catch(() => {})
    await browser.close()
    process.exit(1)
  }
}

main()
