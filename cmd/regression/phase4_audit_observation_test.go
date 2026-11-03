//go:build phase4audit

package regression

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestPhase4AuditObservation 仅收集隔离HTTP/数据库观察，不把200或探测结束当成功能验收。
// 前置本地harness，逐条替换模板参数并记录状态、KV变化和上游次数；不包含真实凭据，schema由harness清理。
func TestPhase4AuditObservation(t *testing.T) {
	raw, err := os.ReadFile("../../internal/gateway/testdata/route-ownership.json")
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Catalog map[string][]string `json:"catalog"`
	}
	if err = json.Unmarshal(raw, &inventory); err != nil {
		t.Fatal(err)
	}
	var patterns []string
	for owner, rows := range inventory.Catalog {
		if strings.HasSuffix(owner, ":manage") {
			patterns = append(patterns, rows...)
		}
	}
	sort.Strings(patterns)
	h := newHarness(t)
	admin := h.adminSession()
	user := h.openScope(t, admin, "audit-observer")
	records := []map[string]any{}
	parameter := regexp.MustCompile(`\{[^}]+\}`)
	for _, pattern := range patterns {
		method, path, _ := strings.Cut(pattern, " ")
		path = parameter.ReplaceAllString(path, "audit-fixture")
		before, err := h.store.Engine.QueryString("SELECT kind,id,body FROM kv ORDER BY kind,id")
		if err != nil {
			t.Fatal(err)
		}
		beforeJSON, _ := json.Marshal(before)
		a := h.do(method, path, "", map[string]any{"id": "audit-fixture", "note": "audit-observation"})
		u := h.do(method, path, user.session, map[string]any{"id": "audit-fixture", "note": "audit-observation"})
		r := h.do(method, path, admin, map[string]any{"id": "audit-fixture", "note": "audit-observation"})
		after, err := h.store.Engine.QueryString("SELECT kind,id,body FROM kv ORDER BY kind,id")
		if err != nil {
			t.Fatal(err)
		}
		afterJSON, _ := json.Marshal(after)
		records = append(records, map[string]any{"route": pattern, "anonymous": a.status, "user": u.status, "admin": r.status, "kv_changed": string(beforeJSON) != string(afterJSON), "upstream_calls": len(h.upstreamCalls())})
	}
	if len(records) != 303 {
		t.Fatalf("观察入口缺失: %d", len(records))
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	output := os.Getenv("XHUB_PHASE4_AUDIT_OUTPUT")
	if output == "" {
		output = filepath.Join(t.TempDir(), "observations.json")
	}
	if err = os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(output, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
