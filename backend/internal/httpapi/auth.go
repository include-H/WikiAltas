package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 简易用户系统（DESIGN_V2 §3.6）：
//   · 单管理员账号，密码哈希存 settings 表
//   · 登录后发 HttpOnly Cookie，值是 HMAC 签名的 "用户名|过期时间戳"
//   · 访客（未登录）默认拒绝，只有白名单里的只读接口可访问，且只返回公开内容

const (
	sessionCookieName = "wa_session"
	sessionTTL        = 30 * 24 * time.Hour
	// 登录失败锁定（对齐 GameManager 的策略：连续失败 → 冷却）
	maxLoginFails = 5
	loginLockTTL  = 5 * time.Minute
	failWindow    = 15 * time.Minute
)

type loginAttempt struct {
	fails       int
	firstFailAt time.Time
	lockedUntil time.Time
}

// loginGuards 记录每个来源的失败次数（进程内即可：单实例、单管理员）。
var loginGuards = struct {
	sync.Mutex
	m map[string]*loginAttempt
}{m: map[string]*loginAttempt{}}

func loginKey(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}

// checkLoginAllowed 返回（是否允许, 剩余次数, 需要等待的秒数）。
func checkLoginAllowed(key string) (bool, int, int64) {
	loginGuards.Lock()
	defer loginGuards.Unlock()
	a, ok := loginGuards.m[key]
	if !ok {
		return true, maxLoginFails, 0
	}
	now := time.Now()
	if now.Before(a.lockedUntil) {
		return false, 0, int64(a.lockedUntil.Sub(now).Seconds()) + 1
	}
	if now.Sub(a.firstFailAt) > failWindow {
		delete(loginGuards.m, key)
		return true, maxLoginFails, 0
	}
	return true, maxLoginFails - a.fails, 0
}

func noteLoginFail(key string) (remaining int, lockedFor int64) {
	loginGuards.Lock()
	defer loginGuards.Unlock()
	now := time.Now()
	a, ok := loginGuards.m[key]
	if !ok || now.Sub(a.firstFailAt) > failWindow {
		a = &loginAttempt{firstFailAt: now}
		loginGuards.m[key] = a
	}
	a.fails++
	if a.fails >= maxLoginFails {
		a.lockedUntil = now.Add(loginLockTTL)
		a.fails = 0
		a.firstFailAt = now
		return 0, int64(loginLockTTL.Seconds())
	}
	return maxLoginFails - a.fails, 0
}

func clearLoginFails(key string) {
	loginGuards.Lock()
	defer loginGuards.Unlock()
	delete(loginGuards.m, key)
}

func (s *Server) signSession(username string, exp int64) string {
	// 指纹 = 当前访问密码哈希的摘要：改密码 → 旧 Cookie 全部失效
	payload := username + "|" + strconv.FormatInt(exp, 10) + "|" + s.store.SessionFingerprint()
	mac := hmac.New(sha256.New, []byte(s.store.SessionSecret()))
	mac.Write([]byte(payload))
	return payload + "|" + hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) verifySession(token string) (string, bool) {
	parts := strings.Split(token, "|")
	if len(parts) != 4 {
		return "", false
	}
	username, expStr, fp, sig := parts[0], parts[1], parts[2], parts[3]
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", false
	}
	if fp != s.store.SessionFingerprint() {
		return "", false
	}
	mac := hmac.New(sha256.New, []byte(s.store.SessionSecret()))
	mac.Write([]byte(username + "|" + expStr + "|" + fp))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return "", false
	}
	if username != s.store.AdminUsername() {
		return "", false
	}
	return username, true
}

// isAuthed 当前请求是否带着有效的管理员会话。
func (s *Server) isAuthed(r *http.Request) bool {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return false
	}
	_, ok := s.verifySession(c.Value)
	return ok
}

// guestAllowed 访客（未登录）唯一可用的接口：只读浏览公开内容。
func (s *Server) guestAllowed(r *http.Request) bool {
	// 登录/登出本身必须在未登录时可用
	if r.Method == http.MethodPost && (r.URL.Path == "/api/auth/login" || r.URL.Path == "/api/auth/logout") {
		return true
	}
	// 首次设置：还没设管理员密码时，允许提交一次设置（用于引导页设置密码），
	// 设好之后这个口子自动关闭。
	if r.Method == http.MethodPut && r.URL.Path == "/api/settings" && !s.store.AdminPasswordConfigured() {
		return true
	}
	if r.Method != http.MethodGet {
		return false
	}
	p := r.URL.Path
	switch {
	case p == "/api/health", p == "/api/auth/me", p == "/api/tree", p == "/api/search":
		return true
	case strings.HasPrefix(p, "/api/works/") && strings.HasSuffix(p, "/docs"):
		return true
	case strings.HasPrefix(p, "/api/works/") && !strings.Contains(strings.TrimPrefix(p, "/api/works/"), "/"):
		return true
	case strings.HasPrefix(p, "/api/docs/") && !strings.Contains(strings.TrimPrefix(p, "/api/docs/"), "/"):
		return true
	}
	return false
}

// withAuth 默认拒绝：非白名单接口一律要求登录。
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.isAuthed(r) || s.guestAllowed(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeErr(w, http.StatusUnauthorized, "unauthorized", "需要登录后才能使用该功能")
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if !s.store.AdminPasswordConfigured() {
		writeErr(w, http.StatusBadRequest, "no_password", "尚未设置管理员密码：请先在设置页设置（首次需已登录状态或在本地直接设置）")
		return
	}
	// 只有"访问密码"，没有账号：用户名可省略，默认就是管理员
	username := strings.TrimSpace(body.Username)
	if username == "" {
		username = s.store.AdminUsername()
	}
	key := loginKey(r)
	if allowed, _, retryAfter := checkLoginAllowed(key); !allowed {
		w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error": map[string]any{
				"code":    "locked",
				"message": fmt.Sprintf("尝试次数过多，请 %d 秒后再试", retryAfter),
			},
			"retryAfterSeconds": retryAfter,
		})
		return
	}
	if !s.store.CheckAdminPassword(username, body.Password) {
		remaining, lockedFor := noteLoginFail(key)
		payload := map[string]any{
			"error":             map[string]any{"code": "bad_credentials", "message": "访问密码不正确"},
			"remainingAttempts": remaining,
		}
		if lockedFor > 0 {
			payload["error"] = map[string]any{
				"code": "locked", "message": fmt.Sprintf("尝试次数过多，请 %d 秒后再试", lockedFor),
			}
			payload["retryAfterSeconds"] = lockedFor
		}
		writeJSON(w, http.StatusUnauthorized, payload)
		return
	}
	clearLoginFails(key)
	exp := time.Now().Add(sessionTTL).Unix()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    s.signSession(username, exp),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": username})
}

func (s *Server) handleLogout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleMe 前端启动时问一次：我是不是登录态、有没有设密码。
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	username, ok := "", false
	if c, err := r.Cookie(sessionCookieName); err == nil {
		username, ok = s.verifySession(c.Value)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authed":                  ok,
		"username":                username,
		"role":                    map[bool]string{true: "admin", false: "guest"}[ok],
		"adminPasswordConfigured": s.store.AdminPasswordConfigured(),
	})
}
