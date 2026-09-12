// Package library 对接外部媒体库（当前接的是孪生项目 GameAtlas）。
package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"
)

// ErrNotConfigured：设置页还没填 GameAtlas 地址或管理员密码。
var ErrNotConfigured = errors.New("gameatlas 未配置")

// GameAtlasEntry 是 /api/games/all 的一条精简投影。
type GameAtlasEntry struct {
	PublicID    string           `json:"public_id"`
	Title       string           `json:"title"`
	TitleAlt    *string          `json:"title_alt"`
	Visibility  string           `json:"visibility"`
	ReleaseDate *string          `json:"release_date"`
	CoverImage  *string          `json:"cover_image"`
	Series      *GameAtlasSeries `json:"series"`
	CreatedAt   string           `json:"created_at"`
	UpdatedAt   string           `json:"updated_at"`
}

type GameAtlasSeries struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Client 是一次操作期的会话客户端：懒登录一次，之后复用 Cookie。
// GameAtlas 只有"管理员密码换会话"这一种认证，所以密码是配置的一部分。
type Client struct {
	baseURL  string
	password string
	http     *http.Client
	loggedIn bool
}

func NewClient(baseURL, password string) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" || strings.TrimSpace(password) == "" {
		return nil, ErrNotConfigured
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &Client{
		baseURL:  base,
		password: password,
		http:     &http.Client{Timeout: 15 * time.Second, Jar: jar},
	}, nil
}

// BaseURL 供宿主拼封面等资源地址。
func (c *Client) BaseURL() string { return c.baseURL }

// GameURL 是条目在 GameAtlas 前端的深链。
func (c *Client) GameURL(publicID string) string {
	return c.baseURL + "/games/" + publicID
}

type envelope struct {
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

func decodeEnvelope(status int, raw []byte) (*envelope, error) {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("gameatlas 响应异常（HTTP %d）", status)
	}
	if !env.Success {
		msg := strings.TrimSpace(env.Error)
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", status)
		}
		return nil, errors.New(msg)
	}
	return &env, nil
}

func (c *Client) login(ctx context.Context) error {
	raw, err := json.Marshal(map[string]string{"password": c.password})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/auth/login", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("gameatlas 连接失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if _, err := decodeEnvelope(resp.StatusCode, body); err != nil {
		return fmt.Errorf("gameatlas 登录失败：%s", err.Error())
	}
	c.loggedIn = true
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	if !c.loggedIn {
		if err := c.login(ctx); err != nil {
			return err
		}
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("gameatlas 连接失败: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if env, err := decodeEnvelope(resp.StatusCode, raw); err != nil {
		return fmt.Errorf("gameatlas: %s", err.Error())
	} else if out != nil && len(env.Data) > 0 {
		return json.Unmarshal(env.Data, out)
	}
	return nil
}

// ListAll 全量精简目录（管理员会话，含私有条目）。
func (c *Client) ListAll(ctx context.Context) ([]GameAtlasEntry, error) {
	var out []GameAtlasEntry
	if err := c.do(ctx, http.MethodGet, "/api/games/all", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// PushWiki 把正文写回 GameAtlas 条目；summary 非 nil 时连同简介一起反哺。
func (c *Client) PushWiki(ctx context.Context, publicID, content string, summary *string) error {
	body := map[string]any{
		"content":        content,
		"change_summary": "反哺自 WikiAltas",
	}
	if summary != nil {
		body["summary"] = *summary
	}
	return c.do(ctx, http.MethodPut, "/api/games/"+publicID+"/wiki", body, nil)
}
