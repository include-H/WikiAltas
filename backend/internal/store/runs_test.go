package store

import (
	"strings"
	"sync"
	"testing"
)

// 历史回放必须滤掉**所有**流式增量——按命名约定 *.delta，而不是硬编码清单。
//
// 真实事故：清单里写的是 ('narrative.delta','tool.delta')，后来加了
// reasoning.delta 没人补，于是一个工单的历史回放 530 条里有 478 条
// 是思考的逐字碎片（而且思考文本在库里还存了两份）。
func TestListRunEventsPlainDropsEveryDeltaType(t *testing.T) {
	st := newTestStore(t)
	r, err := st.CreateRun("answer", "问一句", "m", "work:w1")
	if err != nil {
		t.Fatal(err)
	}
	types := []string{
		"response.output_item.added", "response.output_text.delta",
		"wikiatlas.notice", "response.reasoning_summary_text.delta",
		"response.function_call_arguments.done", "response.function_call_arguments.delta",
		"future.thing.delta", // 以后新增的增量也自动算增量
	}
	for _, typ := range types {
		if _, err := st.AppendRunEvent(r.ID, typ, map[string]any{"t": typ}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.ListRunEventsPlain(r.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range got {
		if strings.HasSuffix(e.Type, ".delta") {
			t.Fatalf("增量事件漏进了历史回放：%s", e.Type)
		}
	}
	if len(got) != 3 { // 非 .delta 的那 3 条（含 wikiatlas.* 旁路）
		t.Fatalf("应保留 3 条权威事件，得到 %d", len(got))
	}
	// 不过滤的那条路（实时回放）必须还能拿到增量，否则 SSE 断线续传就废了
	all, err := st.ListRunEvents(r.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(types) {
		t.Fatalf("实时路径应拿到全部 %d 条，得到 %d", len(types), len(all))
	}
}

// sequence_number 是 Responses 流事件专属的：从 0 起、连续 +1，被 wikiatlas.*
// 旁路事件插在中间也不受影响（它们不占号）。前端的归约器只在连续序号上推进，
// 跳号要攒到 gap > 10 才敢跳——所以这条不变量一旦破了，界面就会丢块。
func TestSequenceNumberOnlyOnResponseEvents(t *testing.T) {
	st := newTestStore(t)
	r, err := st.CreateRun("answer", "问一句", "m", "work:w1")
	if err != nil {
		t.Fatal(err)
	}
	order := []struct {
		typ     string
		wantSeq int // -1 = 不该带号
	}{
		{"wikiatlas.meta", -1},
		{"response.created", 0},
		{"response.in_progress", 1},
		{"wikiatlas.notice", -1},
		{"response.output_item.added", 2},
		{"response.output_text.delta", 3},
		{"wikiatlas.usage", -1},
		{"response.output_text.done", 4},
		{"response.output_item.done", 5},
		{"response.completed", 6},
	}
	for _, step := range order {
		ev, err := st.AppendRunEvent(r.ID, step.typ, map[string]any{"k": step.typ})
		if err != nil {
			t.Fatal(err)
		}
		got, has := ev.Payload["sequence_number"]
		if step.wantSeq < 0 {
			if has {
				t.Fatalf("%s 不该带 sequence_number（旁路事件不占号），得到 %v", step.typ, got)
			}
			continue
		}
		n, ok := got.(int)
		if !ok || n != step.wantSeq {
			t.Fatalf("%s 的 sequence_number = %v(%T)，want %d", step.typ, got, got, step.wantSeq)
		}
	}
}

// 发号必须原子：取消/续跑的生命周期播报走 HTTP goroutine，与执行器 goroutine
// 并发写同一个 run。若 MAX(seq)+1 / COUNT(*) 与 INSERT 分成两条语句，两个
// goroutine 会算出同一个号——(run_id, seq) 有唯一约束，后到的那条**直接写不进去**，
// 事件被丢掉：轻则少一条播报，重则少一个 response.* 流事件，前端序号出现空洞，
// 归约器要攒到 gap > 10 才肯跳过。
func TestAppendRunEventNumberingIsAtomicUnderConcurrency(t *testing.T) {
	st := newTestStore(t)
	r, err := st.CreateRun("answer", "并发发号", "m", "work:w1")
	if err != nil {
		t.Fatal(err)
	}
	const goroutines, per = 8, 20
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < per; i++ {
				if _, err := st.AppendRunEvent(r.ID, "response.output_text.delta", map[string]any{"delta": "x"}); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("并发 append 失败: %v", err)
	}

	events, err := st.ListRunEvents(r.ID, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != goroutines*per {
		t.Fatalf("事件数 = %d，want %d", len(events), goroutines*per)
	}
	seqs := map[int64]bool{}
	nums := map[int]bool{}
	for _, ev := range events {
		if seqs[ev.Seq] {
			t.Fatalf("seq 重复: %d", ev.Seq)
		}
		seqs[ev.Seq] = true
		raw, has := ev.Payload["sequence_number"]
		if !has {
			t.Fatalf("Responses 事件缺 sequence_number: %s", ev.Type)
		}
		// 注意：ListRunEvents 的 payload 是 JSON 反序列化来的，数字是 float64。
		f, ok := raw.(float64)
		if !ok {
			t.Fatalf("sequence_number 类型 = %T", raw)
		}
		n := int(f)
		if nums[n] {
			t.Fatalf("sequence_number 重复: %d", n)
		}
		nums[n] = true
	}
	for i := 0; i < goroutines*per; i++ {
		if !nums[i] || !seqs[int64(i+1)] {
			t.Fatalf("编号不连续：缺 seq=%d 或 sequence_number=%d", i+1, i)
		}
	}
}
