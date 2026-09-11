package run

import (
	"fmt"
	"strings"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/store"
)

const (
	batchMaxSize = 20
	// batchMaxRequest 是单次请求能提交的节点数上限（防止一口气排几百个真模型工单）
	batchMaxRequest = 50
)

// CreateBatch 把一批作品节点排成 Altas 工单。
//
// 设计取舍：不新增表/不新增 intent —— 一个批次就是共享的
// workspace（batch:<id>）+ 每个作品一个 create_wiki 工单；
// 进度用 GET /api/runs?workspace=batch:<id> 读，暂停=取消队列里的工单，
// 失败可以单条 resume。并发由 Manager 的 worker 池统一限制。
func (m *Manager) CreateBatch(body domain.CreateBatchBody) (*domain.BatchResult, error) {
	if len(body.WorkIDs) == 0 {
		return nil, store.ErrValidation{Message: "workIds is required"}
	}
	// 上限护栏：一次批量最多 50 部（batchSize 是"这批要处理几部"，这里是"一次请求别塞太多"），
	// 否则一个 POST 就能瞬间排几百个真模型工单，既烧额度又拖垮前端。
	if len(body.WorkIDs) > batchMaxRequest {
		return nil, store.ErrValidation{Message: fmt.Sprintf("一次最多 %d 部，收到 %d 个", batchMaxRequest, len(body.WorkIDs))}
	}
	size := body.BatchSize
	if size <= 0 || size > batchMaxSize {
		size = 5
	}
	batchID := store.NewID()
	workspace := "batch:" + batchID
	template := body.GoalTemplate
	if strings.TrimSpace(template) == "" {
		template = "写《{title}》的 Wiki"
	}

	result := &domain.BatchResult{BatchID: batchID, Workspace: workspace, RunIDs: []string{}, SkippedIDs: []string{}}
	for _, workID := range body.WorkIDs {
		if len(result.RunIDs) >= size {
			break
		}
		w, err := m.store.GetWork(workID)
		if err != nil {
			result.SkippedIDs = append(result.SkippedIDs, workID)
			continue
		}
		goal := strings.ReplaceAll(template, "{title}", w.Title)
		workIDCopy := w.ID
		run, err := m.CreateAndStart(domain.CreateRunBody{
			Intent:    domain.RunIntentCreateWiki,
			Goal:      goal,
			Workspace: workspace,
			Context:   &domain.RunContext{WorkID: &workIDCopy, Medium: body.Medium},
		})
		if err != nil {
			result.SkippedIDs = append(result.SkippedIDs, workID)
			continue
		}
		result.RunIDs = append(result.RunIDs, run.ID)
	}
	if len(result.RunIDs) == 0 {
		return nil, fmt.Errorf("没有可建档的节点（%d 个被跳过）", len(result.SkippedIDs))
	}
	return result, nil
}
