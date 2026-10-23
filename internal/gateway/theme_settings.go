package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/sunqirui1987/xhub/internal/util"
)

var logTraceOnceThemeSettings sync.Once

// uiTheme reads and writes one theme settings object. The save and logo routes share that object with the settings read.
// 参数 w（http.ResponseWriter）：调用方的 HTTP 响应，状态码和正文写在这里；r（*http.Request）：入站 HTTP 请求，用来读路径、头和正文。
// 调用：仅在 theme_settings.go 内使用
// 测试：无直接单测
// 返回：无。状态码和正文写进调用方的响应。
func (s *Server) uiTheme(w http.ResponseWriter, r *http.Request) {
	logTraceOnceThemeSettings.Do(func() { util.Trace("enter gateway.uiTheme") })

	if s.requireManage(w, r) == nil {
		return
	}
	current := s.loadTheme()
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		util.WriteJSON(w, 200, current)
		return
	}
	body := readMap(r)
	if strings.Contains(r.URL.Path, "ui_theme_settings") {
		if color, ok := body["primary_color"]; ok {
			current["primary_color"] = color
		}
	}
	if strings.Contains(r.URL.Path, "/upload/logo") {
		if logo, ok := body["logo_url"]; ok {
			current["logo_url"] = logo
		}
	}
	raw, _ := json.Marshal(map[string]any{
		"primary_color": current["primary_color"],
		"logo_url":      current["logo_url"],
	})
	if err := s.Store.PutKV("ui_theme", "settings", string(raw)); err != nil {
		util.WriteError(w, 500, "internal", err.Error())
		return
	}
	util.WriteJSON(w, 200, s.loadTheme())
}

// loadTheme returns the saved theme, or empty color and logo fields when nothing is stored yet.
// 参数：无。
// 返回 map[string]any（map[string]any）：载入Theme的字段表。缺键表示上游或库里没有这个字段。
// 调用：仅在 theme_settings.go 内使用
// 测试：无直接单测
func (s *Server) loadTheme() map[string]any {
	out := map[string]any{"object": "ui_theme_settings", "primary_color": "", "logo_url": ""}
	saved, err := s.Store.GetKV("ui_theme", "settings")
	if err != nil {
		return out
	}
	if color, ok := saved["primary_color"]; ok {
		out["primary_color"] = color
	}
	if logo, ok := saved["logo_url"]; ok {
		out["logo_url"] = logo
	}
	return out
}
