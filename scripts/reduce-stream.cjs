#!/usr/bin/env node
/*
 * 把 verify-sse.py 落盘的真实事件流，喂进 Semi 自己的 Responses 归约器。
 *
 * 这是在证伪/证实一句话：「前端不需要任何自己的投影」——如果 Semi 的
 * streamingResponseToMessage 能把后端吐的流折成合理的 message，这句话就成立；
 * 折不出来，就是后端事件形状不合规（而不是要去写第二套投影）。
 *
 * 用法：
 *   scripts/verify-sse.py --dump /tmp/stream.jsonl
 *   node scripts/reduce-stream.cjs /tmp/stream.jsonl
 *
 * 关键点：归约器按 sequence_number 增量处理，所以这里**逐块喂**，模拟前端
 * 一块一块到达；不是一次性把数组丢进去（那测不出增量状态机）。
 */
const fs = require('fs')
const path = require('path')

const reducerPath = path.join(
  __dirname, '..', 'frontend', 'node_modules', '@douyinfe', 'semi-foundation',
  'lib', 'cjs', 'aiChatDialogue', 'dataAdapter', 'streamingResponseToMessage.js',
)
const mod = require(reducerPath)
const reduce = mod.default || mod

function main() {
  const file = process.argv[2]
  if (!file) {
    console.error('用法: node scripts/reduce-stream.cjs <dump.jsonl>')
    process.exit(2)
  }
  const lines = fs.readFileSync(file, 'utf8').split('\n').filter(Boolean)

  const problems = []
  let state
  let msg
  let fed = 0
  let missingType = 0

  for (const line of lines) {
    let rec
    try {
      rec = JSON.parse(line)
    } catch {
      problems.push(`dump 里有非法 JSON 行：${line.slice(0, 80)}`)
      continue
    }
    const { event, data } = rec
    // 只喂 Responses 事件；wikiatlas.* 是"同流不同类"，归约器不认识它们，
    // 由前端另一条通路消费。
    if (!event || !event.startsWith('response.')) continue
    if (!data || typeof data !== 'object') continue

    // 规范要求 payload 自带 type。缺了要报——前端别替后端补。
    if (!data.type) missingType++
    const chunk = data.type ? data : { ...data, type: event }
    fed++
    const r = reduce([chunk], state)
    if (r) {
      msg = r.message
      state = r.nextState
    }
  }

  console.log(`喂入 Responses 事件 ${fed} 条（跳过非 response.* 的）`)
  if (missingType) problems.push(`${missingType} 条事件的 payload 没有 type 字段（规范要求自带）`)

  if (!msg) {
    console.log('\n归约器没折出任何 message —— 流里没有可归约的 Responses 事件。')
    for (const p of problems) console.log(`  [问题] ${p}`)
    process.exit(1)
  }

  const items = Array.isArray(msg.content) ? msg.content : msg.content ? [msg.content] : []

  // message 项的正文在 content[].text 里，不在 it.text 上（Semi 的 Message 形状）。
  // 别按 it.text 判断"这一轮什么都没有"——那是误判。
  const textOf = (it) => {
    const parts = Array.isArray(it?.content) ? it.content : it?.content ? [it.content] : []
    return parts.map((p) => (typeof p?.text === 'string' ? p.text : '')).join('')
  }
  const bodyText = msg.output_text || items.map(textOf).join('')
  const kinds = {}
  for (const it of items) {
    const k = it?.type || '?'
    kinds[k] = (kinds[k] || 0) + 1
  }

  console.log('\n折出的 message：')
  console.log(`  status      = ${msg.status}`)
  console.log(`  model       = ${msg.model}`)
  console.log(`  output_text = ${JSON.stringify((msg.output_text || '').slice(0, 120))}${(msg.output_text || '').length > 120 ? '…' : ''}`)
  console.log(`  content     = ${items.length} 项 ${JSON.stringify(kinds)}`)

  for (const it of items) {
    if (it?.type === 'function_call') {
      console.log(`    · function_call  name=${it.name}  args=${JSON.stringify((it.arguments || '').slice(0, 60))}`)
    } else if (it?.type === 'reasoning') {
      const t = it.text || it.summary || ''
      console.log(`    · reasoning      ${JSON.stringify(String(t).slice(0, 60))}${String(t).length > 60 ? '…' : ''}`)
    } else if (it?.type === 'message') {
      const t = textOf(it)
      console.log(`    · message        ${JSON.stringify(t.slice(0, 60))}${t.length > 60 ? '…' : ''}`)
    }
  }

  // 硬不变量
  if (msg.status !== 'completed') problems.push(`最终 status = ${msg.status}，不是 completed`)
  if (!bodyText.trim() && !items.some((it) => it?.type === 'function_call')) {
    problems.push('既没有正文也没有 function_call —— 这一轮什么都没有')
  }
  for (const it of items) {
    if (it?.type === 'function_call' && !it.name) problems.push('function_call 项没有 name')
  }

  console.log()
  for (const p of problems) console.log(`  [问题] ${p}`)
  if (!problems.length) console.log('  全部通过。')
  process.exit(problems.length ? 1 : 0)
}

main()
