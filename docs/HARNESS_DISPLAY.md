# Harness 展示模型：对照 DeepSeek Harness（dsh）

结论先说：**dsh 的"流式"不是把一段文字打出来，而是把一次 turn 拆成
"块 + 增量"，再按"过程 / 结论"分层显示。** 我们缺的是前半段（增量事件），
后半段（过程折叠成芯片）已经做完。

参考：仓库 checkout 在 `deepseek-harness/`（`packages/llm`、`packages/client/ui-chat`、`packages/client/ui-tool`）。

## dsh 的四个关键设计

1. **流的单位 = 块 + 增量**（`packages/llm/llm/src/types.ts:390`）

   ```ts
   type StreamChunk =
     | { type: 'block-start'; index; blockType }
     | { type: 'text-delta'; index; text }
     | { type: 'reasoning-delta'; index; text }
     | { type: 'tool-call-delta'; index; id; name?; argumentsDelta }
     | { type: 'block-end'; index; block }
     | { type: 'usage'; usage } | { type: 'finish'; reason }
   ```

   前端在 `ui-chat/.../assistant.ts#updateChunk` 里把 delta 累积进 `blocks[]`：
   text 追加、tool-call 累加 `argsRaw`、`block-end` 才用权威块替换。
   —— 所以 UI 永远先看到"半成品"，完成时再"纠正"。

2. **一个 turn 拆成"过程 / 结论"**（`ui-chat/.../turn-process*.ts`）

   用 `anchorSeq` 把 assistant-step、工具调用、todo 归入"过程"，
   最终回答单独展开；`hasExternalProcess`/`compactAnswer` 决定要不要
   把过程折成一枚折叠条（默认收起，点开才看每一步）。

3. **工具调用按家族给视图**（`ui-tool/src/client/tool/toolviews/*`）

   `todo-row`（计划：done/total + 当前 in_progress + 并行数）、
   `search-row` / `web-row` / `read-row` / `file-mutation-row`（diff）/
   `bash-sample`（终端）/ `ask-question-row`（反问用户）。
   每行 = 图标 + 标题 + 一行摘要（可省略）+ 展开详情（原始参数/输出），
   行内含状态与耗时。

4. **中间态的纪律**

   - 半截 JSON 必须能显示：`plan-summary.ts` 明确写 "Mid-stream truncation → 回退通用摘要"。
   - 会省略的文本里不塞关键数字：并行进行中的任务数放在**不参与省略号**的相邻 span（`plan-summary.ts` 注释）。
   - `partial.ts` 决定哪些增量值得触发渲染，避免每个 token 重排整棵树。

## WikiAltas 现状 vs 差距

| 维度 | dsh | 我们 |
|---|---|---|
| 模型输出 | 流式（SSE），text/reasoning/tool-call 三类 delta | `internal/llm/client.go` **单次 POST**，只拿整段 |
| 事件粒度 | block-start/delta/block-end | `narrative` / `tool.started|done` / `content.staging|committed` / `plan.updated`（只有"完成态"） |
| 过程 / 结论 | 过程折叠 + 结论展开 | 四段阶段芯片默认收起（`components/run/RunSteps.tsx`），结论即叙事行 |
| 工具展示 | 每类工具有专属卡片 | 一行摘要芯片（`compactDetail` 按工具类型给人话） |
| 中间态 | 半截 JSON 可显示 + 完成时纠正 | 无（没有中间态） |

## 建议的落地方案（最小可用）

1. **`internal/llm` 加流式**：`ChatStream(ctx, messages, tools, onChunk)`，
   走 OpenAI 兼容的 `stream: true`，产出 `text-delta` / `reasoning-delta` /
   `tool-call-delta`；`Chat()` 保留（echo/mock/回退路径不变），用
   `WIKIATLAS_LLM_STREAM=1` 开关。
2. **`internal/run/executor.go` 转发增量**：
   - `narrative.delta`：累积的助手文本，节流 ~150ms 或 ~40 字推一条；
   - `narrative`：一轮结束时推权威全文（前端用它覆盖累积结果）；
   - `tool.delta`：工具参数累积（面板显示"正在写入…"），`tool.started/done` 保持权威。
   落库节流：delta 标 `ephemeral:true`，每 500ms 才落一条快照进 `run_events`，
   避免事件表被 token 撑爆。
3. **前端累积**（`lib/runProjection.ts`）：把同一个 step 的 `narrative.delta`
   累加进**同一条** assistant 文本块（对齐 dsh 的 blocks），进行中显示打字态；
   `tool.delta` 让 RunSteps 的芯片显示"运行中 + 半截参数"。
4. **验收**：真模型跑一次 `continue_wiki`，面板里能看到
   "Altas 边想边打"→ 工具芯片 运行中 → 完成；`run_events` 行数增长可控
   （目标：不含 delta 时 +<5%，delta 快照 ≤ 每 500ms 一条）。

> 迁移顺序：先 1+2（后端，不动 UI 也能用 SSE 观察），再 3（前端累积），
> 最后按 dsh 的 `partial.ts` 思路做渲染节流。
+
+## 落地状态（2026-09-10 23:xx 完成，非"最小可用"）
+
+- [x] `internal/llm`：`StreamingClient.ChatStream` 走 OpenAI 兼容 SSE，
+      产出 `text` / `reasoning` / `tool` 三类 `Delta`，并把增量拼回完整 `ChatResult`
+      （含 `tool_calls` 的 id/name/参数碎片）。provider 拒流时执行器自动回退 `Chat`。
+- [x] `internal/run`：`callModel` 把增量发成 `narrative.delta` / `tool.delta`，
+      **节流 250ms 且不丢字**（攒够一批再 flush），每轮结束补一条 `final` 收尾；
+      `WIKIATLAS_LLM_STREAM=0` 可关。
+- [x] 前端：`runProjection` 累积增量到同一条叙事（带 ▍ 打字光标），
+      权威 `narrative` 到达时收尾（取更长的那份）；`tool.delta` 先画"运行中"芯片，
+      权威 `tool.started` 接管后不重复；面板贴底自动滚动（用户翻历史时不抢）。
+- [x] 实测：真模型 `answer` 工单 10s 出首个 delta、9 条累计 68 字；
+      浏览器里面板文本 106 → 119 字逐步增长、打字态出现、芯片显示"检索资料 1 ⌄"。
+- 单元测试：`internal/llm/stream_test.go`（SSE 拼装 + HTTP 错误回退）。
+
+## 第二轮对照（2026-09-10 深夜）——又揪出 3 个真 bug
+
+1. **SSE 命名事件漏订阅**：`frontend/src/lib/sse.ts` 里 `NAMED_EVENTS` 是白名单，
+   `EventSource` 只派发显式 `addEventListener` 的事件名。新加的 `narrative.delta` /
+   `tool.delta` 没进白名单 → 浏览器里根本收不到增量，面板退化成"整段蹦出来"。
+   （服务器侧一切正常，所以只有真跑浏览器才看得出来。）
+2. **tool.delta 的 args 只发了碎片**：执行器把 `argumentsDelta` 当 `args` 发，
+   `answer` / `narrative` 工具的文本参数于是永远是半截（观测到 `args='》'`）。
+   修成"累计到当前的参数"后，前端才能从中抠出 `text` 做流式打字。
+3. **流式增量把历史窗口挤爆**：`GET /api/runs/{id}` 默认 200 条事件，
+   增量一进来长工单只剩后半段（前面的章节叙事被截掉）。
+   修法：历史与 SSE 回放默认**过滤增量**（`ListRunEventsPlain`），只有 `?deltas=1` 才带；
+   前端拉历史用 1000 条上限。
+
+## 与 dsh 对齐后补的执行器护栏（第二轮）
+
+| 机制 | 我们的实现 | dsh 对应 |
+|---|---|---|
+| 单轮超时 | 20 分钟 ctx + 流式 150s 空转看门狗 | adapter 层的 stream/abort |
+| 重试 | 限流/5xx/网络抖动 3 次（1s→4s→10s），已吐字不重试 | `llm-retry` |
+| 上下文压缩 | > 12 万字符压中间段（整段丢，保消息序列合法） | token-meter + compact |
+| 重复调用拦截 | 同名同参 > 3 次直接回 `ok:false` | 工具层循环防护 |
+| 意图即权限 | `answer`/只读模式不下发写工具 | 工具可见性（tool surface） |
+| 批量上限 | 单请求 ≤ 50 部、`limit` ≤ 200 | ——（我们自己的护栏） |
+
+验证：`go test -race`（run/store/llm）通过；审计脚本 62/62；
+浏览器全站体检 0 报错；kill -9 重启后 running → interrupted → resume → completed；
+面板流式实测 113 → 126 → 150 → 163 → 219 → 259 字逐步长出来。
