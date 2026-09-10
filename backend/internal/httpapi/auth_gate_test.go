package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"testing"
)

const anotherPassword = "another-password-456"

func loginClient(t *testing.T, url, password string) (*http.Client, int) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	body, _ := json.Marshal(map[string]string{"password": password})
	req, _ := http.NewRequest("POST", url+"/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	return client, resp.StatusCode
}

func authed(t *testing.T, url string, client *http.Client) bool {
	t.Helper()
	resp, err := client.Get(url + "/api/auth/me")
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Authed bool `json:"authed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	return out.Authed
}

// 改访问密码 = 所有旧会话立即失效（硬门槛：密码是唯一入口，不该留后门）。
func TestChangingPasswordInvalidatesSessions(t *testing.T) {
	ts, st := newTestServer(t)

	client, code := loginClient(t, ts.URL, testAdminPassword)
	if code != http.StatusOK || !authed(t, ts.URL, client) {
		t.Fatalf("初始登录失败: code=%d authed=%v", code, authed(t, ts.URL, client))
	}

	if err := st.SetAdminPassword(anotherPassword); err != nil {
		t.Fatalf("改密码: %v", err)
	}
	if authed(t, ts.URL, client) {
		t.Fatal("改密码后旧会话仍然有效")
	}
	if _, code := loginClient(t, ts.URL, testAdminPassword); code != http.StatusUnauthorized {
		t.Fatalf("旧密码登录 = %d, want 401", code)
	}
	fresh, code := loginClient(t, ts.URL, anotherPassword)
	if code != http.StatusOK || !authed(t, ts.URL, fresh) {
		t.Fatalf("新密码登录失败: code=%d", code)
	}
}

// 未登录绝对看不到私有内容：树里不出现、直取 404、写接口 401。
func TestGuestCannotSeePrivateContent(t *testing.T) {
	ts, _ := newTestServer(t)
	admin := testClients[ts.URL]
	if admin == nil {
		t.Fatal("缺少已登录客户端")
	}

	created := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{
		"kind": "work", "title": "硬门槛私有单作",
	}, http.StatusCreated)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("建节点失败: %v", created)
	}

	guest := &http.Client{}
	resp, err := guest.Get(ts.URL + "/api/works/" + id)
	if err != nil {
		t.Fatalf("guest get work: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("访客直取私有节点 = %d, want 404", resp.StatusCode)
	}

	resp, err = guest.Get(ts.URL + "/api/tree")
	if err != nil {
		t.Fatalf("guest tree: %v", err)
	}
	defer resp.Body.Close()
	var tree struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tree); err != nil {
		t.Fatalf("decode tree: %v", err)
	}
	for _, n := range tree.Nodes {
		if n.ID == id {
			t.Fatal("访客树里出现了私有节点")
		}
	}

	write, _ := http.NewRequest("POST", ts.URL+"/api/works", bytes.NewReader([]byte(`{"kind":"work","title":"访客偷建"}`)))
	write.Header.Set("Content-Type", "application/json")
	wresp, err := guest.Do(write)
	if err != nil {
		t.Fatalf("guest write: %v", err)
	}
	wresp.Body.Close()
	if wresp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("访客写接口 = %d, want 401", wresp.StatusCode)
	}
}
