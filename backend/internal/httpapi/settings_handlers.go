package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/llm"
)

// settingsPayload 是设置页提交的载荷。
// 密钥字段只在提交时出现：空字符串=不修改，clearXxx=true=清空。
type settingsPayload struct {
	LLM struct {
		Endpoint        string   `json:"endpoint"`
		Model           string   `json:"model"`
		APIKey          string   `json:"apiKey"`
		ClearAPIKey     bool     `json:"clearApiKey"`
		Temperature     *float64 `json:"temperature"`
		MaxTokens       *int     `json:"maxTokens"`
		ReasoningEffort string   `json:"reasoningEffort"`
		Protocol        string   `json:"protocol"`
		ContextWindow   int      `json:"contextWindow"`
	} `json:"llm"`
	Search struct {
		ExaAPIKey      string `json:"exaApiKey"`
		ClearExaAPIKey bool   `json:"clearExaApiKey"`
		// ProxyURL 出外网的 HTTP 代理。nil = 这次没提交这个字段（不动）；
		// 空串 = 明确清掉——文本输入框最自然的语义就是"清空即删除"。
		ProxyURL *string `json:"proxyUrl"`
	} `json:"search"`
	Library struct {
		EmbyURL         *string `json:"embyUrl"`
		EmbyAPIKey      *string `json:"embyApiKey"`
		KomgaURL        *string `json:"komgaUrl"`
		KomgaAPIKey     *string `json:"komgaApiKey"`
		GameAtlasURL    *string `json:"gameatlasUrl"`
		GameAtlasAPIKey *string `json:"gameatlasApiKey"`
		// EmbyLibraryRoles：nil = 这次没提交（不动）；非 nil = 整体替换。
		EmbyLibraryRoles *[]domain.EmbyLibraryRole `json:"embyLibraryRoles"`
		// ScanIntervalMinutes：nil = 没提交；0 = 关闭；其余为分钟数。
		ScanIntervalMinutes *int `json:"scanIntervalMinutes"`
	} `json:"library"`
	Runs struct {
		ExpireDays        int `json:"expireDays"`
		KeepEventsDays    int `json:"keepEventsDays"`
		MaxConcurrentRuns int `json:"maxConcurrentRuns"`
	} `json:"runs"`
	Admin struct {
		Username          string `json:"username"`
		NewPassword       string `json:"newPassword"`
		ClearPassword     bool   `json:"clearPassword"`
		NewNodeVisibility string `json:"newNodeVisibility"`
	} `json:"admin"`
	SkillRoot string `json:"skillRoot"`
}

func nonEmpty(s *string) *string {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil
	}
	return s
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	// 还没设密码时允许一次"初始化提交"（设置管理员密码）；设好后必须登录。
	if !s.isAuthed(r) && s.store.AdminPasswordConfigured() {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "需要登录后才能修改设置")
		return
	}
	var p settingsPayload
	if err := decodeBody(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	// 以当前设置为底再覆盖：表单没出现的字段不能被抹掉
	// （旧实现整体替换，导致媒体库密钥被清空）。
	st, err := s.store.GetSettings()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if p.LLM.Endpoint != "" {
		st.LLM.Endpoint = p.LLM.Endpoint
	}
	if p.LLM.Model != "" {
		st.LLM.Model = p.LLM.Model
	}
	st.LLM.Temperature = p.LLM.Temperature
	st.LLM.MaxTokens = p.LLM.MaxTokens
	if p.LLM.ReasoningEffort != "" {
		st.LLM.ReasoningEffort = p.LLM.ReasoningEffort
	}
	if p.LLM.Protocol != "" {
		st.LLM.Protocol = p.LLM.Protocol
	}
	if p.LLM.ContextWindow > 0 {
		st.LLM.ContextWindow = p.LLM.ContextWindow
	}
	if v := nonEmpty(p.Library.EmbyURL); v != nil {
		st.Library.EmbyURL = v
	}
	if v := nonEmpty(p.Library.EmbyAPIKey); v != nil {
		st.Library.EmbyAPIKey = v
	}
	if v := nonEmpty(p.Library.KomgaURL); v != nil {
		st.Library.KomgaURL = v
	}
	if v := nonEmpty(p.Library.KomgaAPIKey); v != nil {
		st.Library.KomgaAPIKey = v
	}
	if v := nonEmpty(p.Library.GameAtlasURL); v != nil {
		st.Library.GameAtlasURL = v
	}
	if v := nonEmpty(p.Library.GameAtlasAPIKey); v != nil {
		st.Library.GameAtlasAPIKey = v
	}
	if p.Library.EmbyLibraryRoles != nil {
		roles := make([]domain.EmbyLibraryRole, 0, len(*p.Library.EmbyLibraryRoles))
		for _, r := range *p.Library.EmbyLibraryRoles {
			name := strings.TrimSpace(r.Name)
			if name == "" {
				continue
			}
			if r.Role != domain.EmbyLibraryRoleWork && r.Role != domain.EmbyLibraryRoleMixed {
				continue
			}
			roles = append(roles, domain.EmbyLibraryRole{ID: r.ID, Name: name, Role: r.Role})
		}
		st.Library.EmbyLibraryRoles = roles
	}
	if p.Library.ScanIntervalMinutes != nil {
		v := *p.Library.ScanIntervalMinutes
		if v < 0 {
			v = 0
		}
		if v > 10080 {
			v = 10080
		}
		st.Library.ScanIntervalMinutes = &v
	}
	if p.Runs.ExpireDays > 0 {
		st.Runs.ExpireDays = p.Runs.ExpireDays
	}
	if p.Runs.KeepEventsDays > 0 {
		st.Runs.KeepEventsDays = p.Runs.KeepEventsDays
	}
	if p.Runs.MaxConcurrentRuns > 0 && p.Runs.MaxConcurrentRuns <= 16 {
		st.Runs.MaxConcurrentRuns = p.Runs.MaxConcurrentRuns
	}
	if p.SkillRoot != "" {
		st.SkillRoot = p.SkillRoot
	}
	if p.Admin.Username != "" {
		st.Admin.Username = p.Admin.Username
	}
	if p.Admin.NewNodeVisibility == string(domain.VisibilityPublic) ||
		p.Admin.NewNodeVisibility == string(domain.VisibilityPrivate) {
		st.Admin.NewNodeVisibility = domain.Visibility(p.Admin.NewNodeVisibility)
	}

	if p.LLM.ClearAPIKey {
		_ = s.store.SetAPIKey("")
	} else if p.LLM.APIKey != "" {
		_ = s.store.SetAPIKey(p.LLM.APIKey)
	}
	if p.Search.ClearExaAPIKey {
		_ = s.store.SetSecret("exa_api_key", "")
	} else if p.Search.ExaAPIKey != "" {
		_ = s.store.SetSecret("exa_api_key", p.Search.ExaAPIKey)
	}
	// 出外网的代理：nil = 这次没提交（不动），空串 = 清掉。
	if p.Search.ProxyURL != nil {
		st.Search.ProxyURL = strings.TrimSpace(*p.Search.ProxyURL)
	}
	if p.Admin.ClearPassword {
		_ = s.store.SetAdminPassword("")
	} else if p.Admin.NewPassword != "" {
		if err := s.store.SetAdminPassword(p.Admin.NewPassword); err != nil {
			writeStoreErr(w, err)
			return
		}
	}

	if err := s.store.SaveSettings(st); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.settingsForResponse(st))
}

// handleTestLLM：设置页的「测试连接」——用当前配置发一次极小的请求。
func (s *Server) handleTestLLM(w http.ResponseWriter, r *http.Request) {
	client := s.runs.ActiveClient()
	if client == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "未配置模型"})
		return
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := client.Chat(ctx, []llm.Message{{Role: "user", Content: "只回复两个字：正常"}}, nil)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "model": client.Model(), "latencyMs": latency, "error": err.Error()})
		return
	}
	reply := strings.TrimSpace(res.Content)
	if len([]rune(reply)) > 60 {
		reply = string([]rune(reply)[:60])
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "model": client.Model(), "latencyMs": latency, "reply": reply})
}

// handleRuntime：设置页「关于/运行时」区块的数据。
func (s *Server) handleRuntime(w http.ResponseWriter, _ *http.Request) {
	st, _ := s.store.GetSettings()
	stats, _ := s.store.RuntimeStats()
	// 上下文窗口：优先用设置里的值，没设时给个保守默认（面板右下角的用量环用它做分母）
	ctxWindow := st.LLM.ContextWindow
	if ctxWindow <= 0 {
		ctxWindow = 262144
	}
	info := map[string]any{
		"model":             s.runs.ActiveClient().Model(),
		"contextWindow":     ctxWindow,
		"maxConcurrentRuns": st.Runs.MaxConcurrentRuns,
		"activeRuns":        s.runs.ActiveCount(),
		"queuedRuns":        s.runs.QueuedCount(),
		"skillRoot":         s.runs.SkillRoot(),
		"stats":             stats,
		"features": []string{
			"阅读：宇宙树 / 大纲 / 正文（题记·说明块·九章）",
			"写作：编辑态 Markdown（BlockNote）+ 飞书三态（编辑 / 修订 / 只读）",
			"Altas：Run + 工具面（站内检索 / 联网核实 / 按章写入）+ 折叠叙事流",
			"资料夹：宇宙 / 系列各自的非标资料列表，长文可按节写入并关联本节点子树内的条目",
			"批次：一键批量建档（worker 池并发执行）",
			"版本：每次写入成 revision，可回滚；修订可逐条接受/拒绝",
		},
	}
	writeJSON(w, http.StatusOK, info)
}
