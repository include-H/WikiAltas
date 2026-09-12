package store

import (
	"database/sql"
	"testing"

	"wikiatlas/backend/internal/domain"
)

// 设置里**每一个**可持久化字段都必须存得回来。
//
// 起因：mergeSettings 原来是逐字段手写的，漏了 ContextWindow 和整个 Admin 段——
// 设置页把管理员标识改成「不知名网友Hao」，点保存、刷新，又变回 admin。
// 手写合并迟早会漏，所以这里用一条**与字段列表无关**的不变量兜底：
//
//	存 → 读 → 再存，两次落盘的 JSON 必须一模一样。
//
// 合并漏掉任何字段，第二次就会少一项，测试就红。
func TestSettingsRoundTripKeepsEveryField(t *testing.T) {
	st := newTestStore(t)

	s, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	// 所有字段都改成"一眼能认出"的非默认值
	temp, mt := 0.42, 4096
	embyURL, embyKey := "http://192.168.1.4:8081/emby", "emby-key"
	komgaURL, komgaKey := "http://192.168.1.4:8082/api/v1", "komga-key"
	gaURL, gaKey := "http://192.168.1.4:3000", "ga-key"
	s.LLM.Endpoint = "https://chat.example/v1"
	s.LLM.Model = "test-model"
	s.LLM.Protocol = "responses"
	s.LLM.ReasoningEffort = "xhigh"
	s.LLM.ContextWindow = 123456
	s.LLM.Temperature = &temp
	s.LLM.MaxTokens = &mt
	s.Library.EmbyURL = &embyURL
	s.Library.EmbyAPIKey = &embyKey
	s.Library.KomgaURL = &komgaURL
	s.Library.KomgaAPIKey = &komgaKey
	s.Library.GameAtlasURL = &gaURL
	s.Library.GameAtlasAPIKey = &gaKey
	s.Runs.ExpireDays = 3
	s.Runs.KeepEventsDays = 11
	s.Runs.MaxConcurrentRuns = 5
	s.Search.ProxyURL = "http://192.168.1.253:7890"
	s.Admin.Username = "不知名网友Hao"
	s.Admin.NewNodeVisibility = domain.VisibilityPublic
	s.SkillRoot = "/tmp/skills-under-test"

	if err := st.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	first := rawAppSettings(t, st)

	// 读回来再存一遍：任何"读的时候被丢掉"的字段都会在这时消失
	round, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSettings(round); err != nil {
		t.Fatal(err)
	}
	if second := rawAppSettings(t, st); first != second {
		t.Fatalf("存→读→再存 之后设置变了，说明有字段在读取时被丢掉：\n第一次: %s\n第二次: %s", first, second)
	}

	// 顺带把本次真实踩到的两个点掉
	back, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if back.Admin.Username != "不知名网友Hao" {
		t.Errorf("管理员标识没存回来：%q", back.Admin.Username)
	}
	if back.Admin.NewNodeVisibility != domain.VisibilityPublic {
		t.Errorf("新节点可见性没存回来：%q", back.Admin.NewNodeVisibility)
	}
	if back.LLM.ContextWindow != 123456 {
		t.Errorf("上下文窗口没存回来：%d", back.LLM.ContextWindow)
	}
	if back.Runs.MaxConcurrentRuns != 5 {
		t.Errorf("并发上限没存回来：%d", back.Runs.MaxConcurrentRuns)
	}
	if back.Search.ProxyURL != "http://192.168.1.253:7890" {
		t.Errorf("出外网代理没存回来：%q", back.Search.ProxyURL)
	}
}

// rawAppSettings 直接读 settings 表里的 app 行（不经过默认值合并），
// 用来比对"落盘内容"本身。
func rawAppSettings(t *testing.T, st *Store) string {
	t.Helper()
	var raw sql.NullString
	if err := st.DB.QueryRow(`SELECT value FROM settings WHERE key = 'app'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw.String
}
