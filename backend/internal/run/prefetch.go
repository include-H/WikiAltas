package run

import (
	"database/sql"
	"fmt"
	"strings"

	"wikiatlas/backend/internal/store"
)

// 预取（pre-fetch）：发起工单时由系统直接把「目标节点的情况」放进首条消息，
// 而不是等模型自己想起来调 read_work。
//
// 为什么需要它：工具检索的链路是"模型决定查什么"，模型完全可能跳过 read_work
// 直接联网，或者对着一个空节点瞎猜。预取把这几件事变成确定的：
//
//	· 目标节点是什么（kind/介质/状态/别名）
//	· 它的正文（开头 + 章节大纲，避免整篇塞爆上下文）
//	· 它的子节点版图（系列 -> 单作）、同级单作、**与它们的关系**（改编/续作/衍生…）
//	· 它的资料夹里有什么（非标文件清单）
//
// 这和 RAG 的"提前召回"是同一个思路：确定性的部分系统做，需要判断的部分交给模型。
const (
	prefetchBodyRunes   = 1200
	prefetchMaxHeadings = 24
	prefetchMaxChildren = 30
	prefetchMaxDocs     = 20
	// 关系比子节点少得多，给 12 条足够（一个节点不该挂几十条边）
	prefetchMaxRelations = 12
)

func buildContextBrief(st *store.Store, workID, docID string) string {
	if workID == "" && docID == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## 系统预取：目标对象现状（已核实，不必再查这一步）\n")

	if workID != "" {
		w, err := st.GetWork(workID)
		if err != nil {
			return ""
		}
		medium := "-"
		if w.Medium != nil {
			medium = string(*w.Medium)
		}
		content := ""
		if w.ContentMd != nil {
			content = *w.ContentMd
		}
		fmt.Fprintf(&b, "- 节点：%s（%s · 介质 %s · 状态 %s）\n", w.Title, w.Kind, medium, w.Status)
		// 所属层级：同名作品极多（如《白狼崛起》既是猎魔人短篇集、也是起点同人小说），
		// 必须把"它挂在哪个宇宙/系列下"写清楚，否则模型会联网搜到别的同名作品。
		if chain := ancestorChain(st, w.ID); len(chain) > 0 {
			fmt.Fprintf(&b, "- 所属层级：%s\n", strings.Join(chain, " › "))
		}
		if len(w.Aliases) > 0 {
			fmt.Fprintf(&b, "- 别名：%s\n", strings.Join(w.Aliases, "、"))
		}
		fmt.Fprintf(&b, "- 正文：%d 字", len([]rune(content)))
		if strings.TrimSpace(content) == "" {
			b.WriteString("（**空节点**：需要建档，别假装它已经有内容）\n")
		} else {
			b.WriteString("\n")
		}

		if heads := headingList(content, prefetchMaxHeadings); len(heads) > 0 {
			fmt.Fprintf(&b, "- 现有章节：%s\n", strings.Join(heads, " / "))
		}
		if head := firstRunes(content, prefetchBodyRunes); head != "" {
			b.WriteString("- 正文开头：\n\n> ")
			b.WriteString(strings.ReplaceAll(head, "\n", "\n> "))
			b.WriteString("\n\n（要看全文用 read_work；只看某章用 read_work 的 section 参数。）\n")
		}

		if kids := childBriefs(st, workID, prefetchMaxChildren); len(kids) > 0 {
			fmt.Fprintf(&b, "- 下级节点（%d）：%s\n", len(kids), strings.Join(kids, "、"))
		}
		if sibs := siblingBriefs(st, w.ParentID, workID, prefetchMaxChildren); len(sibs) > 0 {
			fmt.Fprintf(&b, "- 同级单作（%d）：%s\n", len(sibs), strings.Join(sibs, "、"))
		}
		// 关系：**光有名字不够**。只列"同级单作"时，模型知道旁边有《未来黎明》，
		// 却无从判断它是"同一故事的另一结局"还是"同系列里不相干的一部"——于是
		// 回答剧情问题时不会连带它。关系本来就在库里，摆到模型面前是确定性的事。
		if rels, total := relationBriefs(st, workID, prefetchMaxRelations); len(rels) > 0 {
			line := strings.Join(rels, "；")
			// 截断必须说话：只给前 N 条而不提"还有"，模型会以为这就是全部边。
			if total > len(rels) {
				line += fmt.Sprintf("（还有 %d 条未列出，用 get_tree 看全）", total-len(rels))
			}
			fmt.Fprintf(&b, "- 关系：%s\n", line)
		}
		if docs := docBriefs(st, workID, prefetchMaxDocs); len(docs) > 0 {
			fmt.Fprintf(&b, "- 资料夹（%d 份非标资料）：%s\n", len(docs), strings.Join(docs, "、"))
		}
	}

	if docID != "" {
		d, err := st.GetDoc(docID)
		if err == nil {
			body := strings.TrimSpace(d.ContentMd)
			fmt.Fprintf(&b, "- 资料：%s（%d 字）\n", d.Title, len([]rune(body)))
			if head := firstRunes(body, prefetchBodyRunes); head != "" {
				b.WriteString("- 资料开头：\n\n> ")
				b.WriteString(strings.ReplaceAll(head, "\n", "\n> "))
				b.WriteString("\n\n")
			}
		}
	}

	b.WriteString("\n上面每一条都带 id：**要读别家条目直接 read_work(id)，不必先 get_tree 换 id**。")
	b.WriteString("关系那一行说明了谁和本作讲的是同一段故事——回答跨作品问题（某角色的结局、某个设定）时，")
	b.WriteString("把相关的那几条读进来一起看，别只按当前条目回答。\n")
	return b.String()
}

// ancestorChain 返回从根到当前节点的名称链（不含自身）。
func ancestorChain(st *store.Store, workID string) []string {
	chain := []string{}
	cur := workID
	for i := 0; i < 32; i++ {
		var parent sql.NullString
		err := st.DB.QueryRow(`SELECT parent_id FROM works WHERE id = ?`, cur).Scan(&parent)
		if err != nil || !parent.Valid {
			break
		}
		var title, kind string
		if err := st.DB.QueryRow(`SELECT title, kind FROM works WHERE id = ?`, parent.String).Scan(&title, &kind); err != nil {
			break
		}
		chain = append([]string{fmt.Sprintf("%s(%s)", title, kind)}, chain...)
		cur = parent.String
	}
	return chain
}

// siblingBriefs 列出同一父节点下的其它单作（帮助模型确认"这是哪一部"）。
//
// **带上 id**：只给标题的话，模型想读它还得先调一次 get_tree/search_works 去换 id，
// 多一跳就常常不跳——"能连读别家条目"于是停在纸面上。
func siblingBriefs(st *store.Store, parentID *string, selfID string, limit int) []string {
	if parentID == nil || *parentID == "" {
		return nil
	}
	rows, err := st.DB.Query(
		`SELECT id, title, COALESCE(medium,'') FROM works WHERE parent_id = ? AND id != ? ORDER BY sort_order, title LIMIT ?`,
		*parentID, selfID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]string, 0, limit)
	for rows.Next() {
		var id, title, medium string
		if err := rows.Scan(&id, &title, &medium); err != nil {
			return out
		}
		if medium != "" {
			title += "/" + medium
		}
		out = append(out, fmt.Sprintf("《%s》(%s)", title, id))
	}
	return out
}

func childBriefs(st *store.Store, parentID string, limit int) []string {
	rows, err := st.DB.Query(
		`SELECT id, title, kind, COALESCE(medium, ''), status FROM works WHERE parent_id = ? ORDER BY sort_order, title LIMIT ?`,
		parentID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]string, 0, limit)
	for rows.Next() {
		var id, title, kind, medium, status string
		if err := rows.Scan(&id, &title, &kind, &medium, &status); err != nil {
			return out
		}
		label := fmt.Sprintf("《%s》(%s", title, kind)
		if medium != "" {
			label += "/" + medium
		}
		label += "," + status + ") " + id
		out = append(out, label)
	}
	return out
}

func docBriefs(st *store.Store, workID string, limit int) []string {
	rows, err := st.DB.Query(`SELECT id, title FROM docs WHERE folder_of = ? ORDER BY created_at LIMIT ?`, workID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]string, 0, limit)
	for rows.Next() {
		var id, title string
		if err := rows.Scan(&id, &title); err != nil {
			return out
		}
		out = append(out, fmt.Sprintf("《%s》(%s)", title, id))
	}
	return out
}

// headingList 抽 ## / ### 标题（去掉 markdown 记号），用于告诉模型现有骨架。
func headingList(md string, limit int) []string {
	if strings.TrimSpace(md) == "" {
		return nil
	}
	out := make([]string, 0, limit)
	for _, line := range strings.Split(md, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "## ") && !strings.HasPrefix(t, "### ") {
			continue
		}
		out = append(out, strings.TrimSpace(strings.TrimLeft(t, "# ")))
		if len(out) >= limit {
			break
		}
	}
	return out
}

// firstRunes 按 rune 截断（避免把多字节汉字切成半个）。
func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(string(r[:n])) + "…（已截断）"
}

// relationBriefs 把关系写成带方向的三元组，并返回**总条数**。
//
// 词表的语义是"衍生作 → 来源作"（如 `spin_off_of`），所以方向必须写清楚：
// 同一行关系，对两端节点读起来是相反的。这里从**本节点**出发写成
// `本作 --type--> 《X》`，模型不必反推。
//
// 总数要单独返回：调用方据此在截断时补一句"还有 N 条"——不给总数，
// 模型会把"列出来的这几条"当成全部。
func relationBriefs(st *store.Store, workID string, limit int) ([]string, int) {
	var total int
	if err := st.DB.QueryRow(
		`SELECT COUNT(*) FROM relations WHERE from_id = ? OR to_id = ?`,
		workID, workID).Scan(&total); err != nil {
		return nil, 0
	}
	if total == 0 {
		return nil, 0
	}
	rows, err := st.DB.Query(`
		SELECT r.type, r.from_id, w.id, w.title, COALESCE(w.medium, '')
		FROM relations r
		JOIN works w ON w.id = CASE WHEN r.from_id = ? THEN r.to_id ELSE r.from_id END
		WHERE r.from_id = ? OR r.to_id = ?
		ORDER BY r.created_at LIMIT ?`, workID, workID, workID, limit)
	if err != nil {
		return nil, total
	}
	defer rows.Close()
	out := make([]string, 0, limit)
	for rows.Next() {
		var typ, fromID, otherID, title, medium string
		if err := rows.Scan(&typ, &fromID, &otherID, &title, &medium); err != nil {
			return out, total
		}
		if medium != "" {
			title += "/" + medium
		}
		if fromID == workID {
			out = append(out, fmt.Sprintf("本作 --%s--> 《%s》(%s)", typ, title, otherID))
		} else {
			out = append(out, fmt.Sprintf("《%s》(%s) --%s--> 本作", title, otherID, typ))
		}
	}
	return out, total
}
