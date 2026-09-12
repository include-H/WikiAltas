package store

import (
	"testing"
	"wikiatlas/backend/internal/domain"
)

func TestSessionLifecycle(t *testing.T) {
	s := newTestStore(t)

	// 页面下的第一个会话直接用 target 当 id；第二个才有 "~" 后缀。
	first, err := s.CreateSession("work:w1", "新对话")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if first.ID != "work:w1" || first.Target != "work:w1" {
		t.Fatalf("first session = %+v, want id=target=work:w1", first)
	}
	second, err := s.CreateSession("work:w1", "")
	if err != nil {
		t.Fatalf("CreateSession 2: %v", err)
	}
	if second.ID == first.ID || second.Target != "work:w1" {
		t.Fatalf("second session = %+v, want distinct id under work:w1", second)
	}

	sessions, err := s.ListSessions("work:w1")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(sessions))
	}

	// 建工单：会话行存在 + 标题用首单目标回填 + runCount 跟着涨。
	if _, err := s.CreateRun("answer", "用一句话介绍这部作品", "m", second.ID); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	got, err := s.GetSession(second.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Title != "用一句话介绍这部作品" {
		t.Fatalf("title = %q, want backfilled from first goal", got.Title)
	}
	if got.RunCount != 1 {
		t.Fatalf("runCount = %d, want 1", got.RunCount)
	}

	// 第二单不改标题。
	if _, err := s.CreateRun("answer", "第二问", "m", second.ID); err != nil {
		t.Fatalf("CreateRun 2: %v", err)
	}
	if got, _ = s.GetSession(second.ID); got.Title != "用一句话介绍这部作品" {
		t.Fatalf("title after 2nd run = %q, want unchanged", got.Title)
	}

	// 改名
	if err := s.RenameSession(second.ID, "作品速览"); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	if got, _ = s.GetSession(second.ID); got.Title != "作品速览" {
		t.Fatalf("renamed title = %q", got.Title)
	}
	if err := s.RenameSession("work:nope", "x"); err == nil {
		t.Fatal("RenameSession on missing session should fail")
	}

	// 删除：会话连同工单、事件一起清掉
	if err := s.DeleteSession(second.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := s.GetSession(second.ID); err == nil {
		t.Fatal("session should be gone")
	}
	runs, err := s.ListRuns("", second.ID, 10)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs of deleted session = %d, want 0", len(runs))
	}
	if err := s.DeleteSession(second.ID); err == nil {
		t.Fatal("second delete should be not found")
	}
}

// TestSessionBackfillOnMigrate：老库（有工单、没有会话行）重开时按 workspace 回填。
func TestSessionBackfillOnMigrate(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateRun("answer", "历史的第一个问题", "m", "work:old~abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRun("answer", "历史的第二个问题", "m", "work:old~abc"); err != nil {
		t.Fatal(err)
	}
	// 模拟"升级前的老库"：把会话行删掉，重新跑迁移。
	if _, err := s.DB.Exec(`DELETE FROM sessions`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	sessions, err := s.ListSessions("work:old")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("backfilled sessions = %d, want 1", len(sessions))
	}
	if sessions[0].ID != "work:old~abc" || sessions[0].RunCount != 2 {
		t.Fatalf("backfilled = %+v, want id=work:old~abc count=2", sessions[0])
	}
}

// 工单列表的数据源：不传 target 必须能列出**全部**会话，并带上"最近说了什么"。
//
// 一个会话 = 一场对话 = 用户看到的一张工单；它下面的 runs 是内部执行记录。
// 这条列表的意义就在这儿：发一句"继续"是在同一场对话里多接一段，
// 不该在列表里多出一行。
func TestListSessionsAllTargetsWithLastGoal(t *testing.T) {
	st := newTestStore(t)
	// 两场对话，各两次执行；另有一场在别的 target
	for _, spec := range []struct{ sess, goal string }{
		{"work:w1", "第一次问"},
		{"work:w1", "接着问"},
		{"home", "首页问一句"},
	} {
		if err := st.EnsureSession(spec.sess, spec.goal); err != nil {
			t.Fatal(err)
		}
		r, err := st.CreateRun("answer", spec.goal, "m", spec.sess)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.CompleteRun(r.ID, map[string]any{}); err != nil {
			t.Fatal(err)
		}
	}

	all, err := st.ListSessions("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("应列出全部 2 场对话，得到 %d", len(all))
	}
	byID := map[string]domain.Session{}
	for _, s := range all {
		byID[s.ID] = s
	}
	w1, ok := byID["work:w1"]
	if !ok {
		t.Fatalf("缺 work:w1：%+v", all)
	}
	// 两次执行 = 同一场对话（两行，不是行内又多一条）
	if w1.RunCount != 2 {
		t.Fatalf("work:w1 应有 2 次执行，得到 %d", w1.RunCount)
	}
	// 副标题取**最近一句**，不是第一句
	if w1.LastGoal != "接着问" {
		t.Fatalf("LastGoal 应为最近一句「接着问」，得到 %q", w1.LastGoal)
	}
	// 按 target 过滤仍然管用
	only, err := st.ListSessions("home")
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != 1 || only[0].ID != "home" {
		t.Fatalf("按 target 过滤不对：%+v", only)
	}
}
