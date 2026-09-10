package run

import (
	"encoding/json"
	"fmt"
	"strings"
)

// 工具详情（artifact）：只给"人愿意读的一小段产物"，不给原始 JSON。
// 上限 40 行 / 约 4KB，超出标注截断——对应飞书"完整内容暂不支持展示"。
const (
	artifactMaxLines = 40
	artifactMaxRunes = 4000
	artifactMaxItems = 10
)

// artifactForTool 把工具返回的 JSON 摘要成 {kind, lines, truncated, totalLines}。
// 返回 nil 表示这个工具没有值得展开的产物（例如写入类，只看摘要）。
func artifactForTool(tool string, out json.RawMessage) map[string]any {
	var probe map[string]any
	if err := json.Unmarshal(out, &probe); err != nil {
		return nil
	}
	switch tool {
	case "read_skill", "read_work", "read_doc", "fetch_url":
		text := firstString(probe, "content", "contentMd", "text")
		if text == "" {
			return nil
		}
		return textArtifact(text)
	case "search_web":
		items, _ := probe["results"].([]any)
		lines := make([]string, 0, len(items))
		for i, item := range items {
			if i >= artifactMaxItems {
				break
			}
			m, _ := item.(map[string]any)
			title, _ := m["title"].(string)
			u, _ := m["url"].(string)
			lines = append(lines, strings.TrimSpace(title+" — "+u))
		}
		return linesArtifact(lines, len(items))
	case "search_works":
		items, _ := probe["hits"].([]any)
		lines := make([]string, 0, len(items))
		for i, item := range items {
			if i >= artifactMaxItems {
				break
			}
			m, _ := item.(map[string]any)
			title, _ := m["title"].(string)
			snippet, _ := m["snippet"].(string)
			lines = append(lines, strings.TrimSpace(title+" — "+snippet))
		}
		return linesArtifact(lines, len(items))
	default:
		return nil
	}
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func textArtifact(text string) map[string]any {
	runes := []rune(text)
	truncated := false
	if len(runes) > artifactMaxRunes {
		runes = runes[:artifactMaxRunes]
		truncated = true
	}
	all := strings.Split(string(runes), "\n")
	total := len(all)
	lines := all
	if len(lines) > artifactMaxLines {
		lines = lines[:artifactMaxLines]
		truncated = true
	}
	if len(lines) == 0 {
		return nil
	}
	return map[string]any{
		"kind":       "text",
		"lines":      lines,
		"truncated":  truncated,
		"totalLines": total,
	}
}

func linesArtifact(lines []string, total int) map[string]any {
	filtered := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		filtered = append(filtered, l)
	}
	if len(filtered) == 0 {
		return nil
	}
	truncated := total > len(filtered)
	return map[string]any{
		"kind":       "list",
		"lines":      filtered,
		"truncated":  truncated,
		"totalLines": total,
	}
}

// artifactSummary 是给 narrative 用的短句（"参考 14 篇"这类）。
func artifactSummary(tool string, art map[string]any) string {
	if art == nil {
		return ""
	}
	n, _ := art["totalLines"].(int)
	switch tool {
	case "search_web":
		return fmt.Sprintf("参考 %d 篇", n)
	case "search_works":
		return fmt.Sprintf("命中 %d 条", n)
	default:
		return ""
	}
}
