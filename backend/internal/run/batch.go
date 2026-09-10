package run

import (
	"fmt"
	"strings"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/store"
)

const batchMaxSize = 20

// CreateBatch 把一批作品节点排成馆员工单。
//
// 设计取舍：不新增表/不新增 intent —— 一个批次就是共享的
// workspace（batch:<id>）+ 每个作品一个 create_wiki 工单；
// 进度用 GET /api/runs?workspace=batch:<id> 读，暂停=取消队列里的工单，
// 失败可以单条 resume。并发由 Manager 的 worker 池统一限制。
func (m *Manager) CreateBatch(body domain.CreateBatchBody) (*domain.BatchResult, error) {
	if len(body.WorkIDs) == 0 {
		return nil, store.ErrValidation{Message: "workIds is required"}
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
