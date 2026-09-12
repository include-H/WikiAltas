package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"wikiatlas/backend/internal/domain"
)

func scanDoc(row interface{ Scan(...any) error }) (*domain.Doc, error) {
	var (
		d     domain.Doc
		links string
	)
	err := row.Scan(&d.ID, &d.FolderOf, &d.Title, &d.ContentMd, &d.ContentVer, &links, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(links), &d.Links)
	if d.Links == nil {
		d.Links = []string{}
	}
	return &d, nil
}

const docCols = `id, folder_of, title, content_md, content_ver, links_json, created_at, updated_at`

// CreateDoc inserts a materials document into a node's folder.
//
// 资料夹在界面上只挂给容器节点（宇宙 / 系列）——单作的正文本身就是那个条目，
// 树里不给它挂「资料夹」那一行（见 lib/tree.ts）。这里不做层级校验：
// 存的是"资料挂在哪个节点下"，层级是展示层的选择。
func (s *Store) CreateDoc(workID string, body domain.CreateDocBody) (*domain.Doc, error) {
	if body.Title == "" {
		return nil, ErrValidation{Message: "title is required"}
	}
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM works WHERE id = ?`, workID).Scan(&n); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrNotFound{What: "work"}
	}
	id := NewID()
	now := Now()
	content := ""
	if body.ContentMd != nil {
		content = *body.ContentMd
	}
	links := body.Links
	if links == nil {
		links = []string{}
	}
	if len(links) > 0 {
		if err := s.validateLinksAgainstFolder(workID, links); err != nil {
			return nil, err
		}
	}
	linksJSON, _ := json.Marshal(links)
	_, err := s.DB.Exec(`INSERT INTO docs (id, folder_of, title, content_md, content_ver, links_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, ?, ?, ?)`,
		id, workID, body.Title, content, string(linksJSON), now, now)
	if err != nil {
		return nil, err
	}
	// if initial content, also write revision 1
	if content != "" {
		_, err = s.DB.Exec(`UPDATE docs SET content_ver = 1 WHERE id = ?`, id)
		if err != nil {
			return nil, err
		}
		revID := NewID()
		_, err = s.DB.Exec(`INSERT INTO revisions (id, target_type, target_id, version, content_md, author, run_id, summary, created_at)
			VALUES (?, 'doc', ?, 1, ?, 'human', NULL, 'initial', ?)`,
			revID, id, content, now)
		if err != nil {
			return nil, err
		}
	}
	return s.GetDoc(id)
}

// GetDoc returns one doc by id.
func (s *Store) GetDoc(id string) (*domain.Doc, error) {
	d, err := scanDoc(s.DB.QueryRow(`SELECT `+docCols+` FROM docs WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound{What: "doc"}
	}
	return d, err
}

// ListDocsByWork lists what a node's folder shows.
//   - 宇宙 / 系列：挂在自己名下的资料。
//   - 单作：自己名下不存资料，这里给的是**从祖先资料夹里筛出来的、关联到这篇的资料**——
//     单作资料夹是「谁写了我」的视图，入口在节点菜单的「访问资料夹」，树里不占一行。
func (s *Store) ListDocsByWork(workID string) ([]domain.Doc, error) {
	kind, err := s.workKind(workID)
	if err != nil {
		return nil, err
	}
	if kind == domain.WorkKindWork {
		return s.listDocsLinkingTo(workID)
	}
	rows, err := s.DB.Query(`SELECT `+docCols+` FROM docs WHERE folder_of = ? ORDER BY title`, workID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Doc, 0)
	for rows.Next() {
		d, err := scanDoc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (s *Store) workKind(id string) (domain.WorkKind, error) {
	var kind string
	err := s.DB.QueryRow(`SELECT kind FROM works WHERE id = ?`, id).Scan(&kind)
	if err == sql.ErrNoRows {
		return "", ErrNotFound{What: "work"}
	}
	return domain.WorkKind(kind), err
}

// listDocsLinkingTo 找出祖先容器资料夹里 links 指向该单作的资料。
func (s *Store) listDocsLinkingTo(workID string) ([]domain.Doc, error) {
	ids, err := s.containerAncestors(workID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Doc, 0)
	if len(ids) == 0 {
		return out, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.DB.Query(`SELECT `+docCols+` FROM docs WHERE folder_of IN (`+placeholders+`) ORDER BY title`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		d, err := scanDoc(rows)
		if err != nil {
			return nil, err
		}
		for _, link := range d.Links {
			if link == workID {
				out = append(out, *d)
				break
			}
		}
	}
	return out, rows.Err()
}

// containerAncestors 从该节点往上收集可能持有资料夹的祖先（宇宙 / 系列，不含自己）。
func (s *Store) containerAncestors(id string) ([]string, error) {
	var out []string
	cur := id
	for i := 0; i < 64; i++ {
		var kind string
		var parent sql.NullString
		err := s.DB.QueryRow(`SELECT kind, parent_id FROM works WHERE id = ?`, cur).Scan(&kind, &parent)
		if err == sql.ErrNoRows {
			break
		}
		if err != nil {
			return nil, err
		}
		if cur != id && (kind == string(domain.WorkKindUniverse) || kind == string(domain.WorkKindSeries)) {
			out = append(out, cur)
		}
		if !parent.Valid || parent.String == "" {
			break
		}
		cur = parent.String
	}
	return out, nil
}

// PatchDoc updates title and/or links.
func (s *Store) PatchDoc(id string, body domain.PatchDocBody) (*domain.Doc, error) {
	if _, err := s.GetDoc(id); err != nil {
		return nil, err
	}
	now := Now()
	if body.Title != nil {
		if *body.Title == "" {
			return nil, ErrValidation{Message: "title cannot be empty"}
		}
		if _, err := s.DB.Exec(`UPDATE docs SET title = ?, updated_at = ? WHERE id = ?`, *body.Title, now, id); err != nil {
			return nil, err
		}
	}
	if body.Links != nil {
		if err := s.validateDocLinks(id, *body.Links); err != nil {
			return nil, err
		}
		b, _ := json.Marshal(*body.Links)
		if _, err := s.DB.Exec(`UPDATE docs SET links_json = ?, updated_at = ? WHERE id = ?`, string(b), now, id); err != nil {
			return nil, err
		}
	}
	return s.GetDoc(id)
}

// validateDocLinks 落实「资料只能在本资料夹所属节点的子树内」这条规则：
// 关联目标必须落在**资料夹所属节点的子树内**（含节点自身），不能跨出去。
func (s *Store) validateDocLinks(docID string, links []string) error {
	var folderID string
	err := s.DB.QueryRow(`SELECT folder_of FROM docs WHERE id = ?`, docID).Scan(&folderID)
	if err != nil {
		return err
	}
	return s.validateLinksAgainstFolder(folderID, links)
}

// validateLinksAgainstFolder：关联目标必须在该节点的子树内。
func (s *Store) validateLinksAgainstFolder(folderID string, links []string) error {
	for _, target := range links {
		ok, err := s.isDescendantOf(target, folderID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrValidation{Message: "只能关联本资料夹所属节点子树内的条目，不能跨出去"}
		}
	}
	return nil
}

// isDescendantOf 判断 workID 是否在 ancestorID 的子树里（含直接子节点）。
func (s *Store) isDescendantOf(workID, ancestorID string) (bool, error) {
	cur := workID
	for i := 0; i < 64 && cur != ""; i++ {
		if cur == ancestorID {
			return true, nil
		}
		var parent sql.NullString
		err := s.DB.QueryRow(`SELECT parent_id FROM works WHERE id = ?`, cur).Scan(&parent)
		if err == sql.ErrNoRows {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		cur = ""
		if parent.Valid {
			cur = parent.String
		}
	}
	return false, nil
}

// PutDocContent transactionally commits new doc content with optional version check.
func (s *Store) PutDocContent(id string, body domain.PutContentBody) (*domain.ContentCommitResult, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var contentVer int64
	err = tx.QueryRow(`SELECT content_ver FROM docs WHERE id = ?`, id).Scan(&contentVer)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound{What: "doc"}
	}
	if err != nil {
		return nil, err
	}
	if body.ExpectedVersion != nil && *body.ExpectedVersion != contentVer {
		return nil, ErrConflict{Message: fmt.Sprintf("version conflict: expected %d, current %d", *body.ExpectedVersion, contentVer)}
	}
	switch body.Author {
	case domain.AuthorHuman, domain.AuthorLLM, domain.AuthorImport:
	default:
		return nil, ErrValidation{Message: "author must be human|llm|import"}
	}
	now := Now()
	newVer := contentVer + 1
	summary := ""
	if body.Summary != nil {
		summary = *body.Summary
	}
	if _, err := tx.Exec(`UPDATE docs SET content_md = ?, content_ver = ?, updated_at = ? WHERE id = ?`,
		body.ContentMd, newVer, now, id); err != nil {
		return nil, err
	}
	revID := NewID()
	var runID any
	if body.RunID != nil && *body.RunID != "" {
		runID = *body.RunID
	}
	if _, err := tx.Exec(`INSERT INTO revisions (id, target_type, target_id, version, content_md, author, run_id, summary, created_at)
		VALUES (?, 'doc', ?, ?, ?, ?, ?, ?, ?)`,
		revID, id, newVer, body.ContentMd, string(body.Author), runID, summary, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &domain.ContentCommitResult{ID: id, ContentVer: newVer, RevisionID: revID}, nil
}
