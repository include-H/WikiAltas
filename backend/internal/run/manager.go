// Package run manages Atlas Run lifecycle, SSE broadcast, and execution.
package run

import (
	"context"
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
)

// Manager owns run execution and in-memory SSE subscribers.
type Manager struct {
	store     *store.Store
	client    llm.Client
	skillRoot string

	mu          sync.RWMutex
	subscribers map[string]map[chan domain.RunEvent]struct{}
	cancelers   map[string]context.CancelFunc

	// 工单队列：所有 Run（含批量建档）都排在队列里，由固定数量的 worker
	// 逐个执行，避免一次同步 50 部作品就并发打 50 路 LLM。
	queue                chan func()
	maxConcurrentDefault int
	inFlight             int
	workersOnce          sync.Once

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewManager creates a manager. Pass nil client to auto-resolve from env/store.
func NewManager(st *store.Store, client llm.Client) *Manager {
	if client == nil {
		if cfg, ok := llm.ConfigFromEnv(); ok {
			c, err := llm.NewClient(cfg)
			if err != nil {
				// 环境变量里配了个不认识的协议：不要退回 echo（那会静默跑 mock），
				// 直接把错误带上——真正的报错点在这条工单第一次调用模型的时候。
				log.Printf("llm env config: %v", err)
				client = llm.ErrorClient{Err: err}
			} else {
				client = c
			}
		} else {
			client = llm.EchoClient{}
		}
	}
	return &Manager{
		store:                st,
		client:               client,
		subscribers:          map[string]map[chan domain.RunEvent]struct{}{},
		cancelers:            map[string]context.CancelFunc{},
		queue:                make(chan func(), 256),
		maxConcurrentDefault: maxConcurrentRuns(),
		stopCh:               make(chan struct{}),
	}
}

// maxConcurrentRuns 是环境变量给出的默认并发上限（设置页会覆盖它）。
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
	return m.activeClientFor("")
}

// activeClientFor 同上，但允许本条工单覆盖思考等级（聊天框里选的档位）。
func (m *Manager) activeClientFor(effort string) llm.Client {
	// 测试注入的自定义客户端优先。构造时按环境变量造出来的那个不算"注入"——
	// 设置页（SQLite）里的配置才是用户的当前意图，要能覆盖它。
	if m.client != nil && !llm.IsEcho(m.client) {
		if _, fromEnv := m.client.(*llm.ResponsesClient); !fromEnv {
			return m.client
		}
	}
	// 设置页（SQLite）优先：用户在 UI 改完模型配置，下一次工单立刻生效（热重载）。
	if key := m.store.GetAPIKey(); key != "" {
		st, err := m.store.GetSettings()
		if err == nil {
			e := st.LLM.ReasoningEffort
			if e == "" {
				e = "medium" // off/低/中/高/超高/max；默认中
			}
			if effort != "" {
				e = effort // 本条工单的覆盖值优先
			}
			cfg := llm.Config{
				Endpoint:        st.LLM.Endpoint,
				Model:           st.LLM.Model,
				APIKey:          key,
				Temperature:     st.LLM.Temperature,
				MaxTokens:       st.LLM.MaxTokens,
				ReasoningEffort: e,
				Protocol:        st.LLM.Protocol,
			}
			if cfg.Endpoint == "" {
				cfg.Endpoint = "https://api.openai.com/v1"
			}
			if cfg.Model == "" {
				cfg.Model = "gpt-4o-mini"
			}
			return clientOrError(cfg)
		}
	}
	// 没有落库配置时退回环境变量
	if cfg, ok := llm.ConfigFromEnv(); ok {
		return clientOrError(cfg)
	}
	return m.client
}

// clientOrError 把"构造失败"变成一个会在调用时报错的客户端。
//
// 不在这里返回 nil、也不退回 echo：设置页里协议填错了，这样才会在工单里
// 明着报出来（"不支持的协议 …"），而不是静默跑起 mock 让人以为一切正常。
func clientOrError(cfg llm.Config) llm.Client {
	c, err := llm.NewClient(cfg)
	if err != nil {
		return llm.ErrorClient{Err: err}
	}
	return c
}

// exaKey 读设置页里的 Exa key（env 兜底）——改完即对下一个工单生效。
func (m *Manager) exaKey() string {
	return m.store.ExaAPIKey()
}

// proxyURL 是设置页里配的出外网代理，只给 fetch_url 用（直连超时时模型带 useProxy 重试）。
func (m *Manager) proxyURL() string {
	if st, err := m.store.GetSettings(); err == nil {
		return st.Search.ProxyURL
	}
	return ""
}

// ActiveClient 供设置页「测试连接」使用（与工单实际用的是同一套解析逻辑）。
func (m *Manager) ActiveClient() llm.Client {
	return m.activeClient()
}

// ActiveCount 正在执行的工单数（并发闸门里占着位子的）。
func (m *Manager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.inFlight
}

// QueuedCount 已建单但还没轮到执行的工单数。
func (m *Manager) QueuedCount() int {
	return len(m.queue)
}

// maxConcurrent 读设置页里的并发上限（默认 2），同样支持运行时修改。
func (m *Manager) maxConcurrent() int {
	if st, err := m.store.GetSettings(); err == nil && st.Runs.MaxConcurrentRuns > 0 {
		return st.Runs.MaxConcurrentRuns
	}
	return m.maxConcurrentDefault
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
		// 固定开一小把 worker（上限），真正的并发闸门在 worker() 里按设置读取，
		// 这样在设置页改并发数不需要重启。
		for i := 0; i < workerCeiling; i++ {
			m.wg.Add(1)
			go m.worker()
		}
	})
}

// workerCeiling 是 worker 数量上限；实际并发由设置页的 maxConcurrentRuns 决定。
const workerCeiling = 8

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
			// 并发闸门：等一个空位（上限实时读设置页）
			for {
				m.mu.Lock()
				limit := m.maxConcurrent()
				ok := m.inFlight < limit
				if ok {
					m.inFlight++
				}
				m.mu.Unlock()
				if ok {
					break
				}
				select {
				case <-m.stopCh:
					return
				case <-time.After(200 * time.Millisecond):
				}
			}
			job()
			m.mu.Lock()
			m.inFlight--
			m.mu.Unlock()
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

// CreateAndStart creates a run and launches the executor.
//
// 建单本身**不发事件**：一条 response 由执行器开口（response.created），
// 排队期间就还没有"响应"可言。以前这里先落一条 run.started，于是前端会看到
// 一个"已经开始了"的事件、随后才是真正的响应——多出来的那一层只是噪声。
func (m *Manager) CreateAndStart(body domain.CreateRunBody) (*domain.Run, error) {
	// 记**解析后**的模型名：m.client 只是冷启动占位（没 env 配置时是 echo），
	// 真正跑的是 activeClient()——设置页优先。用占位名会让每条工单的 model
	// 列都写 "echo"，和流里 response.created 报的模型对不上。
	model := m.activeClient().Model()
	r, err := m.store.CreateRun(body.Intent, body.Goal, model, body.Workspace)
	if err != nil {
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
	// 会话键进上下文：执行器用它做"会话转录"（把前几单的对话喂回模型）。
	if body.Workspace != "" {
		ctxMap["session"] = body.Workspace
	}
	// 聊天框里选的思考等级：只作用于本条工单（auto = 不指定，走设置页的值）
	if body.ReasoningEffort != "" && body.ReasoningEffort != "auto" {
		ctxMap["reasoningEffort"] = body.ReasoningEffort
	}
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
	m.noticeRun(runID, "继续执行中断的工单。", NoticeInfo)

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
	m.noticeRun(runID, "工单已取消，checkpoint 已保留。", NoticeInfo)
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

// ensure unused imports stay meaningful in tests/tools wiring
var (
	_ = os.Getenv
	_ = strings.TrimSpace
	_ = fmt.Sprintf
)
