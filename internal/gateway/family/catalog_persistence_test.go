package family

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/store"
	"xorm.io/xorm"
)

// catalogPersistenceHost 为通用目录单测提供管理身份与存储；只实现被测路径实际调用的方法，不发起推理。
type catalogPersistenceHost struct {
	Host
	st *store.Store
}

// RecordStore 返回测试指定的存储；无参数、无额外副作用，由目录处理器调用。
func (h catalogPersistenceHost) RecordStore() *store.Store { return h.st }

// RequireManage 为解析单测提供已授权管理员；参数为测试响应和请求，返回本地身份且不访问真实账号。
func (h catalogPersistenceHost) RequireManage(http.ResponseWriter, *http.Request) *auth.Principal {
	return &auth.Principal{UserID: "catalog-test-admin"}
}

// closedCatalogHost 构造已关闭的 SQL 引擎，让真实 Store 方法确定性失败；不建立网络连接，测试结束再次关闭清理资源。
func closedCatalogHost(t *testing.T) catalogPersistenceHost {
	t.Helper()
	engine, err := xorm.NewEngine("pgx", "postgres://catalog_test@127.0.0.1:1/catalog_test?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return catalogPersistenceHost{st: &store.Store{Engine: engine}}
}

// TestCatalogPersistenceClosedStoreRejectsSuccess 验证真实 Store 在 SQL 引擎关闭时不能伪造列表、详情或写入成功；仅本地关闭引擎，无数据库数据。
func TestCatalogPersistenceClosedStoreRejectsSuccess(t *testing.T) {
	h := closedCatalogHost(t)
	for _, tc := range []struct{ name, method, path string }{
		{"list", "GET", "/search_tools/list"},
		{"info", "GET", "/search_tools/tool-1"},
		{"create", "POST", "/search_tools"},
		{"update", "PUT", "/search_tools/tool-1"},
		{"delete", "DELETE", "/search_tools/tool-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeCatalogPersist(h, w, httptest.NewRequest(tc.method, tc.path, nil), map[string]any{"name": "test-tool"})
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("存储不可用时 %s 不得成功：status=%d body=%s", tc.name, w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "database is closed") || strings.Contains(w.Body.String(), "catalog_test") {
				t.Fatalf("存储内部细节不应出现在响应中：%s", w.Body.String())
			}
		})
	}
}

// TestServeMgmtRejectsInvalidJSON 验证通用管理写入拒绝非对象、尾部内容、截断及读取失败；授权夹具无线上身份，关闭引擎确保不能写入。
func TestServeMgmtRejectsInvalidJSON(t *testing.T) {
	h := closedCatalogHost(t)
	for _, tc := range []struct {
		name string
		body io.Reader
	}{
		{"truncated", strings.NewReader(`{"name":`)},
		{"null", strings.NewReader(`null`)},
		{"array", strings.NewReader(`[]`)},
		{"scalar", strings.NewReader(`true`)},
		{"multiple_objects", strings.NewReader(`{} {}`)},
		{"trailing_garbage", strings.NewReader(`{} false`)},
		{"reader_failure", iotest.ErrReader(errors.New("private reader error"))},
		{"trailing_reader_failure", io.MultiReader(strings.NewReader(`{}`), iotest.ErrReader(errors.New("private reader error")))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			ServeMgmt(h, w, httptest.NewRequest("POST", "/search_tools", tc.body))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("无效JSON必须拒绝且不能写入：status=%d body=%s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "private reader error") {
				t.Fatalf("正文读取错误不能泄漏内部信息：%s", w.Body.String())
			}
		})
	}
}

// catalogMemoryStore 仅用于确定性单测，模拟每次读取独立对象及按操作注入故障；不替代真实 PostgreSQL regression。
type catalogMemoryStore struct {
	rows      map[string]map[string]any
	getErr    error
	listErr   error
	putErr    error
	deleteErr map[string]error
	putCalls  int
	deleteIDs []string
}

// ListKV 返回单测内存记录副本或注入的列表故障；kind 保留接口形状，无外部副作用，失败不能伪报空列表。
func (s *catalogMemoryStore) ListKV(string) ([]map[string]any, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	rows := make([]map[string]any, 0, len(s.rows))
	for _, row := range s.rows {
		rows = append(rows, cloneCatalogTestObject(row))
	}
	return rows, nil
}

// GetKV 读取 id 对应的独立副本；优先返回注入的读取故障，否则不存在时返回 sql.ErrNoRows，不改变已有记录。
func (s *catalogMemoryStore) GetKV(_ string, id string) (map[string]any, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if row := s.rows[id]; row != nil {
		return cloneCatalogTestObject(row), nil
	}
	return nil, sql.ErrNoRows
}

// PutKV 记录保存尝试，仅无注入故障且 JSON 合法时更新 id；返回错误供被测处理器验证，状态仅存在本测试内存。
func (s *catalogMemoryStore) PutKV(_ string, id, body string) error {
	s.putCalls++
	if s.putErr != nil {
		return s.putErr
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(body), &row); err != nil {
		return err
	}
	if s.rows == nil {
		s.rows = map[string]map[string]any{}
	}
	s.rows[id] = row
	return nil
}

// DeleteKV 记录删除顺序并在指定 id 上注入错误；只删除已存在的本地对象，缺失返回 sql.ErrNoRows，无外部清理。
func (s *catalogMemoryStore) DeleteKV(_ string, id string) error {
	s.deleteIDs = append(s.deleteIDs, id)
	if err := s.deleteErr[id]; err != nil {
		return err
	}
	if _, ok := s.rows[id]; !ok {
		return sql.ErrNoRows
	}
	delete(s.rows, id)
	return nil
}

// cloneCatalogTestObject 用 JSON 深复制合法测试记录，模拟真实 Store 解码得到的新对象；非法夹具直接 panic，避免忽略夹具错误。
func cloneCatalogTestObject(row map[string]any) map[string]any {
	raw, err := json.Marshal(row)
	if err != nil {
		panic(err)
	}
	var copied map[string]any
	if err := json.Unmarshal(raw, &copied); err != nil {
		panic(err)
	}
	return copied
}

// callCatalogStore 调用生产目录处理器并返回实际记录器；参数为测试存储、HTTP 方法/路径及对象，不做身份或网络模拟声明。
func callCatalogStore(st catalogRecordStore, method, path string, body map[string]any) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	writeCatalogStore(st, w, httptest.NewRequest(method, path, nil), body)
	return w
}

// TestCatalogPersistenceKeepsCRUDAndUpsert 验证合法创建、详情、列表、更新及删除均对应存储变化，并保留缺失时 upsert；纯内存记录随用例释放。
func TestCatalogPersistenceKeepsCRUDAndUpsert(t *testing.T) {
	st := &catalogMemoryStore{}
	for _, method := range []string{"POST", "PUT"} {
		path := "/search_tools"
		if method == "PUT" {
			path += "/tool-1"
		}
		w := callCatalogStore(st, method, path, map[string]any{"search_tool_id": "tool-1", "name": method, "password": "not-stored"})
		if w.Code != 200 || st.rows["tool-1"]["name"] != method || st.rows["tool-1"]["password"] != nil {
			t.Fatalf("合法 %s 必须真实保存且不存密码：status=%d body=%s rows=%v", method, w.Code, w.Body.String(), st.rows)
		}
	}
	for _, path := range []string{"/search_tools/list", "/search_tools/tool-1"} {
		w := callCatalogStore(st, "GET", path, nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"PUT"`) {
			t.Fatalf("读取必须反映已保存数据 %s：status=%d body=%s", path, w.Code, w.Body.String())
		}
	}
	missing := callCatalogStore(st, "GET", "/search_tools/not-created", nil)
	if missing.Code != 404 || strings.Contains(missing.Body.String(), `"object":"search_tool"`) {
		t.Fatalf("缺失详情不能构造虚假对象：status=%d body=%s", missing.Code, missing.Body.String())
	}
	upsert := callCatalogStore(st, "PUT", "/search_tools/new-through-update", map[string]any{"name": "created by update"})
	if upsert.Code != 200 || st.rows["new-through-update"]["name"] != "created by update" {
		t.Fatalf("保留更新缺失记录的 upsert 契约：status=%d body=%s", upsert.Code, upsert.Body.String())
	}
	for _, wantDeleted := range []int{1, 0} {
		w := callCatalogStore(st, "DELETE", "/search_tools/tool-1", nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), fmt.Sprintf(`"deleted":%d`, wantDeleted)) || st.rows["tool-1"] != nil {
			t.Fatalf("删除必须按实际结果计数，重复删除保持幂等：status=%d body=%s", w.Code, w.Body.String())
		}
	}
}

// TestCatalogPersistenceWriteFailures 验证更新读取失败不覆盖、创建/更新写入失败不成功、编码失败不调用存储；故障和原始记录仅存在内存。
func TestCatalogPersistenceWriteFailures(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		readErr, writeErr  error
		invalidBody        bool
		wantPutCalls       int
	}{
		{"create_write", "POST", "/search_tools", nil, errors.New("private SQL or DSN"), false, 1},
		{"update_write", "PUT", "/search_tools/tool-1", nil, errors.New("private SQL or DSN"), false, 1},
		{"update_read", "PUT", "/search_tools/tool-1", errors.New("private SQL or DSN"), nil, false, 0},
		{"create_encode", "POST", "/search_tools", nil, nil, true, 0},
		{"update_encode", "PUT", "/search_tools/tool-1", nil, nil, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &catalogMemoryStore{rows: map[string]map[string]any{"tool-1": {"id": "tool-1", "name": "original"}}, getErr: tc.readErr, putErr: tc.writeErr}
			body := map[string]any{"search_tool_id": "tool-1", "name": "changed"}
			if tc.invalidBody {
				body["non_json"] = make(chan bool)
			}
			w := callCatalogStore(st, tc.method, tc.path, body)
			if w.Code != 500 || st.putCalls != tc.wantPutCalls || st.rows["tool-1"]["name"] != "original" {
				t.Fatalf("失败不得成功或覆盖原记录：status=%d put_calls=%d body=%s", w.Code, st.putCalls, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "changed") {
				t.Fatalf("失败响应不得泄漏内部详情或返回未保存对象：%s", w.Body.String())
			}
		})
	}
}

// TestCatalogPersistenceDeleteStopsOnFailure 验证批次前项成功、后项数据库失败时整体返回错误并停止；已完成删除不伪称回滚，夹具仅内存。
func TestCatalogPersistenceDeleteStopsOnFailure(t *testing.T) {
	st := &catalogMemoryStore{
		rows:      map[string]map[string]any{"first": {"id": "first"}, "failed": {"id": "failed"}, "untouched": {"id": "untouched"}},
		deleteErr: map[string]error{"failed": errors.New("private deletion failure")},
	}
	w := callCatalogStore(st, "POST", "/tag/delete", map[string]any{"tags": []any{"first", "failed", "untouched"}})
	if w.Code != 500 || len(st.deleteIDs) != 2 || st.rows["first"] != nil || st.rows["failed"] == nil || st.rows["untouched"] == nil {
		t.Fatalf("批量删除中途故障必须报错且停止：status=%d attempted=%v remaining=%v body=%s", w.Code, st.deleteIDs, st.rows, w.Body.String())
	}
}

// TestCatalogPersistenceCredentialMaskSurvivesUpdate 验证凭据创建不回显、读取掩码及掩码更新保留原密钥；测试密钥为固定非真实值，记录随测试释放。
func TestCatalogPersistenceCredentialMaskSurvivesUpdate(t *testing.T) {
	st := &catalogMemoryStore{}
	created := callCatalogStore(st, "POST", "/credentials", map[string]any{"credential_name": "example", "credential_values": map[string]any{"api_key": "test-only-secret", "base_url": "https://example.invalid"}})
	if created.Code != 200 || strings.Contains(created.Body.String(), "test-only-secret") || strings.Contains(created.Body.String(), "credential_values") {
		t.Fatalf("创建成功也不能回显凭据：status=%d body=%s", created.Code, created.Body.String())
	}
	read := callCatalogStore(st, "GET", "/credentials/by_name/example", nil)
	if read.Code != 200 || strings.Contains(read.Body.String(), "test-only-secret") || !strings.Contains(read.Body.String(), "**") {
		t.Fatalf("凭据详情必须打码：status=%d body=%s", read.Code, read.Body.String())
	}
	updated := callCatalogStore(st, "PATCH", "/credentials/example", map[string]any{"credential_values": map[string]any{"api_key": "****", "base_url": "https://updated.invalid"}})
	values := st.rows["example"]["credential_values"].(map[string]any)
	if updated.Code != 200 || values["api_key"] != "test-only-secret" || values["base_url"] != "https://updated.invalid" || strings.Contains(updated.Body.String(), "test-only-secret") {
		t.Fatalf("掩码更新必须保留已有密钥并保存普通字段：status=%d body=%s", updated.Code, updated.Body.String())
	}
}

// TestCatalogPersistenceMissingStore 验证没有配置存储时各类目录操作稳定返回 503，避免空列表假成功或 panic；无任何外部资源。
func TestCatalogPersistenceMissingStore(t *testing.T) {
	for _, tc := range []struct{ method, path string }{{"GET", "/search_tools/list"}, {"GET", "/search_tools/missing"}, {"POST", "/search_tools"}, {"PUT", "/search_tools/tool"}, {"DELETE", "/search_tools/tool"}} {
		w := httptest.NewRecorder()
		writeCatalogPersist(catalogPersistenceHost{}, w, httptest.NewRequest(tc.method, tc.path, nil), nil)
		if w.Code != 503 {
			t.Fatalf("缺少存储必须503 %s %s：status=%d body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

// TestReadCatalogBodyBoundary 验证空正文兼容、合法字段不改写，超过 1 MiB 的合法对象或空白仍沿用既有读取语义；纯内存 reader 无持久化数据需清理。
func TestReadCatalogBodyBoundary(t *testing.T) {
	for _, reader := range []io.Reader{nil, strings.NewReader(""), strings.NewReader(" \r\n\t"), strings.NewReader("{}")} {
		body, err := readCatalogBody(reader)
		if err != nil || body == nil || len(body) != 0 {
			t.Fatalf("空正文和空对象必须保持合法：body=%v err=%v", body, err)
		}
	}
	body, err := readCatalogBody(strings.NewReader(`{"zero":0,"empty":null,"text":"用户原始内容"}`))
	if err != nil || body["zero"] != float64(0) || body["empty"] != nil || body["text"] != "用户原始内容" {
		t.Fatalf("解析不得修改零/null/用户正文：body=%v err=%v", body, err)
	}
	largeWhitespace, err := readCatalogBody(strings.NewReader(strings.Repeat(" ", (1<<20)+1)))
	if err != nil || largeWhitespace == nil || len(largeWhitespace) != 0 {
		t.Fatalf("超过 1 MiB 的空白正文必须保持原有空对象语义：body=%v err=%v", largeWhitespace, err)
	}
	largeText := strings.Repeat("a", (1<<20)+1)
	largeObject, err := readCatalogBody(strings.NewReader(`{"text":"` + largeText + `"}`))
	if err != nil || largeObject["text"] != largeText {
		t.Fatalf("超过 1 MiB 的完整对象必须原样读取：err=%v", err)
	}
}
