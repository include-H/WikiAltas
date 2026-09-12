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

## WikiAltas 的现状（2026-09-11 迁移后）

流式这件事已经落地，但**形状换成了 Responses**：模型/工具的产出统一由执行器
映射成 OpenAI Responses 规范的流事件（`response.output_text.delta` /
`response.reasoning_summary_text.delta` / `response.function_call_arguments.delta` /
`response.output_item.done` / `response.completed`），前端把它们整条交给 Semi 的
`streamingResponseToMessage` 归约——**我们不自己攒块、不自己投影**。

- 事件表与两条 `sequence_number` 硬约束：`DESIGN_V2.md` §6.2。
- 为什么这么做、删掉了哪一层：`DESIGN_V2.md` §4.10。
- 事件类型的唯一 owner：`backend/internal/run/responses.go`（前端镜像在
  `frontend/src/lib/responses.ts`）。

对照 dsh：它的 `block-start/delta/block-end` 与我们现在的
`output_item.added / *.delta / *.done` 是同一个思路——**UI 先看到半成品，
完成时用权威块替换**；区别是我们把"块与增量"的定义交给了公开规范，
归约器也交给组件库，不再自己维护状态机。

## 与 dsh 对齐的执行器护栏

| 机制 | 我们的实现 | dsh 对应 |
|---|---|---|
| 单轮超时 | 20 分钟 ctx + 流式 150s 空转看门狗 | adapter 层的 stream/abort |
| 重试 | 限流/5xx/网络抖动 3 次（1s→4s→10s），已吐字不重试 | `llm-retry` |
| 上下文压缩 | 到窗口 75% 压中间段（会话日志上的一次替换） | token-meter + compact |
| 重复调用拦截 | 同名同参三级：温和提醒 → 具体提醒 → 硬拦 | 工具层循环防护 |
| 意图即权限 | `answer`/只读模式不下发写工具 | 工具可见性（tool surface） |
| 批量上限 | 单请求 ≤ 50 部、`limit` ≤ 200 | ——（我们自己的护栏） |

## 踩过的坑（都还在，别再踩一次）

1. **SSE 命名事件要显式订阅**：`EventSource` 只派发 `addEventListener` 过的事件名，
   类型清单漏一个，那一类就**静默**收不到（服务端一切正常，只有真跑才看得出来）。
   所以清单收在 `lib/responses.ts` 一处，与后端 `responses.go` 一一对应。
2. **流式参数是累计值才有用**：`function_call_arguments.delta` 给的是碎片，
   归约器负责累加；要从参数里读语义（比如 answer 工具要说的话）必须等累计到位。
3. **历史回放要滤掉增量**：`*.delta` 只走实时流，否则一个长工单的回放会被
   半句话碎片占满窗口（约定式过滤见 `store/runs.go`）。归约器只在**连续**序号上
   推进，所以前端在喂它之前会把权威事件重编号（`lib/responses.ts`）。
