// Package run manages Atlas Run lifecycle, SSE broadcast, and execution.
package run

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/llm"
	"wikiatlas/backend/internal/skill"
	"wikiatlas/backend/internal/store"
	"wikiatlas/backend/internal/tools"
)

// Manager owns run execution and in-memory SSE subscribers.
type Manager struct {
	store     *store.Store
	registry  *tools.Registry
	client    llm.Client
	skillRoot string

	mu          sync.RWMutex
	subscribers map[string]map[chan domain.RunEvent]struct{}
	cancelers   map[string]context.CancelFunc

	// 工单队列：所有 Run（含批量建档）都排在队列里，由固定数量的 worker
	// 逐个执行，避免一次同步 50 部作品就并发打 50 路 LLM。
	queue         chan func()
	maxConcurrent int
	workersOnce   sync.Once

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewManager creates a manager. Pass nil client to auto-resolve from env/store.
func NewManager(st *store.Store, client llm.Client) *Manager {
	if client == nil {
		if cfg, ok := llm.ConfigFromEnv(); ok {
			client = llm.NewOpenAIClient(cfg)
		} else {
			client = llm.EchoClient{}
		}
	}
	return &Manager{
		store:         st,
		registry:      tools.DefaultRegistry(),
		client:        client,
		subscribers:   map[string]map[chan domain.RunEvent]struct{}{},
		cancelers:     map[string]context.CancelFunc{},
		queue:         make(chan func(), 256),
		maxConcurrent: maxConcurrentRuns(),
		stopCh:        make(chan struct{}),
	}
}

// maxConcurrentRuns 是同时执行的工单数上限（默认 2，可用环境变量覆盖）。
func maxConcurrentRuns() int {
	if v := strings.TrimSpace(os.Getenv("WIKIATLAS_MAX_CONCURRENT_RUNS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 16 {
			return n
		}
	}
	return 2
}

// SetSkillRoot overrides the wiki-writing skill directory.
func (m *Manager) SetSkillRoot(root string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.skillRoot = root
}

// SkillRoot returns the effective skill root.
func (m *Manager) SkillRoot() string {
	m.mu.RLock()
	root := m.skillRoot
	m.mu.RUnlock()
	if root != "" {
		if l := skill.NewLoader(root); l.Exists() {
			return root
		}
	}
	// settings
	if st, err := m.store.GetSettings(); err == nil && st.SkillRoot != "" {
		if l := skill.NewLoader(st.SkillRoot); l.Exists() {
			return st.SkillRoot
		}
	}
	return skill.ResolveRoot(skill.DefaultCandidates()...)
}

func (m *Manager) skillLoader() *skill.Loader {
	root := m.SkillRoot()
	if root == "" {
		return nil
	}
	return skill.NewLoader(root)
}

// activeClient prefers an explicitly injected non-echo client, then env, then store key.
func (m *Manager) activeClient() llm.Client {
	// explicit non-echo injection wins (tests)
	if m.client != nil && !llm.IsEcho(m.client) {
		// still allow env to override only when injected is default OpenAI from env at construct
		// If tests inject a custom client, keep it.
		if _, isOA := m.client.(*llm.OpenAIClient); !isOA {
			return m.client
		}
	}
	if cfg, ok := llm.ConfigFromEnv(); ok {
		return llm.NewOpenAIClient(cfg)
	}
	if key := m.store.GetAPIKey(); key != "" {
		st, err := m.store.GetSettings()
		if err == nil {
			cfg := llm.Config{
				Endpoint:    st.LLM.Endpoint,
				Model:       st.LLM.Model,
				APIKey:      key,
				Temperature: st.LLM.Temperature,
				MaxTokens:   st.LLM.MaxTokens,
			}
			if cfg.Endpoint == "" {
				cfg.Endpoint = "https://api.openai.com/v1"
			}
			if cfg.Model == "" {
				cfg.Model = "gpt-4o-mini"
			}
			return llm.NewOpenAIClient(cfg)
		}
	}
	return m.client
}

// Start launches workers + the expiry loop, and closes out runs orphaned by a restart.
func (m *Manager) Start() {
	m.recoverInterrupted()
	m.ensureWorkers()
	m.wg.Add(1)
	go m.expiryLoop()
}

// ensureWorkers 懒启动 worker 池：任何一次 dispatch 都会把池子拉起来，
// 这样即使调用方忘了 Start()（例如测试里直接建 Manager）工单也不会卡在队列里。
func (m *Manager) ensureWorkers() {
	m.workersOnce.Do(func() {
		for i := 0; i < m.maxConcurrent; i++ {
			m.wg.Add(1)
			go m.worker()
		}
	})
}

// recoverInterrupted 把上次进程残留的 running 标记为 interrupted（checkpoint 保留，
// 用户可以「继续」）——否则重启后这些工单永远停在 running。
func (m *Manager) recoverInterrupted() {
	runs, err := m.store.ListRuns(string(domain.RunStatusRunning), "", 500)
	if err != nil {
		log.Printf("run recovery: %v", err)
		return
	}
	for _, r := range runs {
		if err := m.store.InterruptRun(r.ID); err != nil {
			log.Printf("run recovery %s: %v", r.ID, err)
		}
	}
	if len(runs) > 0 {
		log.Printf("run recovery: marked %d orphaned runs as interrupted", len(runs))
	}
}

func (m *Manager) worker() {
	defer m.wg.Done()
	for {
		select {
		case <-m.stopCh:
			return
		case job := <-m.queue:
			select {
			case <-m.stopCh:
				return
			default:
			}
			job()
		}
	}
}

// Stop cancels all runs and the expiry loop.
func (m *Manager) Stop() {
	close(m.stopCh)
	m.mu.Lock()
	for id, cancel := range m.cancelers {
		cancel()
		delete(m.cancelers, id)
	}
	m.mu.Unlock()
	m.wg.Wait()
}

func (m *Manager) expiryLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	m.sweep()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.sweep()
		}
	}
}

func (m *Manager) sweep() {
	st, err := m.store.GetSettings()
	if err != nil {
		log.Printf("run expiry: settings: %v", err)
		return
	}
	n, err := m.store.ExpireStaleRuns(st.Runs.ExpireDays)
	if err != nil {
		log.Printf("run expiry: %v", err)
	} else if n > 0 {
		log.Printf("run expiry: expired %d runs", n)
	}
	if _, err := m.store.DeleteOldRunEvents(st.Runs.KeepEventsDays); err != nil {
		log.Printf("run event cleanup: %v", err)
	}
}

// CreateAndStart creates a run, persists run.started, and launches the executor.
func (m *Manager) CreateAndStart(body domain.CreateRunBody) (*domain.Run, error) {
	model := m.client.Model()
	r, err := m.store.CreateRun(body.Intent, body.Goal, model, body.Workspace)
	if err != nil {
		return nil, err
	}
	if _, err := m.store.AppendRunEvent(r.ID, "run.started", map[string]any{
		"runId":  r.ID,
		"goal":   body.Goal,
		"intent": string(body.Intent),
	}); err != nil {
		return nil, err
	}
	ctxMap := map[string]any{}
	if body.Context != nil {
		if body.Context.WorkID != nil {
			ctxMap["workId"] = *body.Context.WorkID
		}
		if body.Context.DocID != nil {
			ctxMap["docId"] = *body.Context.DocID
		}
		if body.Context.ParentID != nil {
			ctxMap["parentId"] = *body.Context.ParentID
		}
		if body.Context.Medium != nil {
			ctxMap["medium"] = string(*body.Context.Medium)
		}
		if body.Context.Section != nil {
			ctxMap["section"] = *body.Context.Section
		}
		if body.Context.DocMode != nil && *body.Context.DocMode != "" {
			ctxMap["docMode"] = *body.Context.DocMode
		}
		if body.Context.Selection != nil && *body.Context.Selection != "" {
			ctxMap["selection"] = *body.Context.Selection
		}
		if body.Context.Extra != nil {
			ctxMap["extra"] = body.Context.Extra
		}
	}
	plan := []domain.RunTask{
		{ID: "t1", Title: "理解目标", Status: "in_progress"},
		{ID: "t2", Title: "检索资料", Status: "pending"},
		{ID: "t3", Title: "产出内容", Status: "pending"},
		{ID: "t4", Title: "写入并完成", Status: "pending"},
	}
	_ = m.store.UpdateRunPlan(r.ID, plan)
	_ = m.store.UpdateRunCheckpoint(r.ID, map[string]any{"lastStep": 0, "context": ctxMap}, map[string]any{})

	m.dispatch(r.ID, body.Intent, body.Goal, ctxMap, 0)
	return m.store.GetRun(r.ID)
}

// Resume continues an interrupted/failed run from checkpoint.
func (m *Manager) Resume(runID string) (*domain.Run, error) {
	r, err := m.store.GetRun(runID)
	if err != nil {
		return nil, err
	}
	switch r.Status {
	case domain.RunStatusInterrupted, domain.RunStatusFailed:
	case domain.RunStatusRunning:
		return r, nil
	default:
		return nil, store.ErrValidation{Message: "cannot resume run in status " + string(r.Status)}
	}
	lastStep := 0
	if v, ok := r.Checkpoint["lastStep"].(float64); ok {
		lastStep = int(v)
	}
	ctxMap := map[string]any{}
	if c, ok := r.Checkpoint["context"].(map[string]any); ok {
		ctxMap = c
	}
	_ = m.store.UpdateRunStatus(runID, domain.RunStatusRunning)
	m.emit(runID, "narrative", map[string]any{"text": "继续执行中断的工单。"})

	m.dispatch(runID, r.Intent, r.Goal, ctxMap, lastStep)
	return m.store.GetRun(runID)
}

// dispatch 把工单交给 worker 池（并发上限 maxConcurrent）。
func (m *Manager) dispatch(runID string, intent domain.RunIntent, goal string, ctxMap map[string]any, fromStep int) {
	m.ensureWorkers()
	job := func() {
		// 排队期间可能已被取消/中断，执行前再确认一次状态
		if r, err := m.store.GetRun(runID); err == nil && r.Status != domain.RunStatusRunning {
			return
		}
		m.execute(runID, intent, goal, ctxMap, fromStep)
	}
	select {
	case m.queue <- job:
	default:
		// 队列满（>256）属于异常情况，退回直接执行，避免工单丢失
		go job()
	}
}

// Cancel interrupts a running run.
func (m *Manager) Cancel(runID string) error {
	r, err := m.store.GetRun(runID)
	if err != nil {
		return err
	}
	if r.Status != domain.RunStatusRunning {
		return store.ErrValidation{Message: "run is not running"}
	}
	m.mu.Lock()
	if cancel, ok := m.cancelers[runID]; ok {
		cancel()
	}
	m.mu.Unlock()
	if err := m.store.InterruptRun(runID); err != nil {
		return err
	}
	m.emit(runID, "narrative", map[string]any{"text": "工单已取消，checkpoint 已保留。"})
	return nil
}

// Subscribe registers an SSE channel. Caller must Unsubscribe.
func (m *Manager) Subscribe(runID string) chan domain.RunEvent {
	ch := make(chan domain.RunEvent, 64)
	m.mu.Lock()
	if m.subscribers[runID] == nil {
		m.subscribers[runID] = map[chan domain.RunEvent]struct{}{}
	}
	m.subscribers[runID][ch] = struct{}{}
	m.mu.Unlock()
	return ch
}

// Unsubscribe removes and closes the channel.
func (m *Manager) Unsubscribe(runID string, ch chan domain.RunEvent) {
	m.mu.Lock()
	if set, ok := m.subscribers[runID]; ok {
		delete(set, ch)
		if len(set) == 0 {
			delete(m.subscribers, runID)
		}
	}
	m.mu.Unlock()
	for {
		select {
		case <-ch:
		default:
			close(ch)
			return
		}
	}
}

// emit persists the event and broadcasts to subscribers.
func (m *Manager) emit(runID, eventType string, payload map[string]any) {
	ev, err := m.store.AppendRunEvent(runID, eventType, payload)
	if err != nil {
		log.Printf("emit %s for %s: %v", eventType, runID, err)
		return
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for ch := range m.subscribers[runID] {
		select {
		case ch <- *ev:
		default:
			// drop if slow consumer; reconnect with Last-Event-ID
		}
	}
}

// execute routes to the real LLM loop or the mock path.
func (m *Manager) execute(runID string, intent domain.RunIntent, goal string, ctxMap map[string]any, fromStep int) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.cancelers[runID] = cancel
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		delete(m.cancelers, runID)
		m.mu.Unlock()
	}()

	client := m.activeClient()
	if llm.IsEcho(client) {
		m.executeMock(ctx, runID, intent, goal, ctxMap, fromStep)
		return
	}
	m.executeLLM(ctx, runID, intent, goal, ctxMap, fromStep)
}

// ToolCacheGet is a helper for tool_cache use.
func ToolCacheGet(r *domain.Run, key string) (json.RawMessage, bool) {
	if r == nil || r.ToolCache == nil {
		return nil, false
	}
	v, ok := r.ToolCache[key]
	if !ok {
		return nil, false
	}
	b, _ := json.Marshal(v)
	return b, true
}

// ensure unused imports stay meaningful in tests/tools wiring
var (
	_ = os.Getenv
	_ = strings.TrimSpace
	_ = fmt.Sprintf
)
