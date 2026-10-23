package gateway

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sunqirui1987/xhub/internal/util"
)

const searchProbeQuery = "OpenAI API documentation"
const searchProbeTimeout = 10 * time.Second
const searchProbeResponseLimit = 1 << 20

// searchProbeInput 是管理连接测试输入；已保存ID与临时参数互斥，query省略时使用固定英文探测语句。
type searchProbeInput struct {
	ID     string         `json:"search_tool_id"`
	Params map[string]any `json:"litellm_params"`
	Query  *string        `json:"query"`
}

// searchProbeResult 只返回搜索展示必需字段，不透出上游任意扩展字段或凭据。
type searchProbeResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// searchProbeFailure 描述连接测试可公开的失败；message由稳定code映射，绝不拼接网络错误、请求URL或供应商正文。
type searchProbeFailure struct {
	Status   int
	Code     string
	Upstream int
}

// searchProbeConfig 保存校验后的执行参数；Endpoint是完整URL，Key仅用于出站认证，禁止进入响应或日志。
type searchProbeConfig struct{ Provider, Endpoint, Key, Query string }

// searchToolTest 以管理员权限执行一次真实搜索探测；w/r是HTTP边界，无成功缓存或数据库写入。
// 已保存配置从服务端读取，临时配置只用于本次请求；授权、配置、网络和结果校验全部通过才返回成功。
func (s *Server) searchToolTest(w http.ResponseWriter, r *http.Request) {
	if s.requireManage(w, r) == nil {
		return
	}
	input, err := readSearchProbeInput(r.Body)
	if err != nil {
		writeSearchProbeFailure(w, searchProbeFailure{Status: 400, Code: "invalid_request"})
		return
	}
	params := input.Params
	if input.ID != "" {
		if s.Store == nil {
			writeSearchProbeFailure(w, searchProbeFailure{Status: 503, Code: "store_unavailable"})
			return
		}
		row, readErr := s.Store.GetKV("search_tools", input.ID)
		if readErr != nil {
			failure := searchProbeFailure{Status: 500, Code: "store_unavailable"}
			if errors.Is(readErr, sql.ErrNoRows) {
				failure = searchProbeFailure{Status: 404, Code: "not_found"}
			}
			writeSearchProbeFailure(w, failure)
			return
		}
		params, _ = row["litellm_params"].(map[string]any)
	}
	cfg, failure := makeSearchProbeConfig(params, input.Query)
	if failure != nil {
		writeSearchProbeFailure(w, *failure)
		return
	}
	results, upstream, failure := runSearchProbe(r.Context(), s.Client, cfg)
	if failure != nil {
		writeSearchProbeFailure(w, *failure)
		return
	}
	util.WriteJSON(w, 200, map[string]any{"status": "success", "search_tool_id": input.ID, "search_provider": cfg.Provider, "test_query": cfg.Query, "results_count": len(results), "results": results, "upstream_status": upstream})
}

// readSearchProbeInput 严格解析单个JSON对象；参数reader是入站正文，返回输入或解析错误，不忽略截断与尾随内容。
// 调用场景为管理探测；ID与参数必须二选一，避免测试输入与保存配置混用而造成误判。
func readSearchProbeInput(reader io.Reader) (searchProbeInput, error) {
	var input searchProbeInput
	if reader == nil {
		return input, errors.New("missing body")
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		return input, err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return input, errors.New("object required")
	}
	if err = json.Unmarshal(trimmed, &input); err != nil {
		return input, err
	}
	if (input.ID == "") == (input.Params == nil) {
		return input, errors.New("one configuration source required")
	}
	return input, nil
}

// makeSearchProbeConfig 校验既有目录支持的三种供应商以及完整HTTP端点；参数为保存或临时配置和可选原始查询。
// 返回执行配置或可公开错误；缺少、掩码和未解析环境引用不是有效凭据，不从进程环境擅自装载秘密。
// 协议依据为Tavily/Perplexity官方/search参考和Serper官方playground，来源及核对日期记录在本PR验证文档。
func makeSearchProbeConfig(params map[string]any, query *string) (searchProbeConfig, *searchProbeFailure) {
	cfg := searchProbeConfig{Provider: strings.ToLower(strings.TrimSpace(str(params["search_provider"]))), Query: searchProbeQuery}
	switch cfg.Provider {
	case "tavily":
		cfg.Endpoint = "https://api.tavily.com/search"
	case "perplexity":
		cfg.Endpoint = "https://api.perplexity.ai/search"
	case "serper":
		cfg.Endpoint = "https://google.serper.dev/search"
	default:
		return cfg, &searchProbeFailure{Status: 400, Code: "unsupported_provider"}
	}
	if query != nil {
		cfg.Query = *query
	}
	if strings.TrimSpace(cfg.Query) == "" {
		return cfg, &searchProbeFailure{Status: 400, Code: "invalid_request"}
	}
	cfg.Key = strings.TrimSpace(str(params["api_key"]))
	if cfg.Key == "" || strings.Contains(cfg.Key, "**") || strings.ContainsAny(cfg.Key, "\r\n") || strings.HasPrefix(cfg.Key, "os.environ/") || strings.HasPrefix(cfg.Key, "${") || strings.HasPrefix(cfg.Key, "env/") {
		return cfg, &searchProbeFailure{Status: 400, Code: "missing_credential"}
	}
	if raw, exists := params["api_base"]; exists && raw != nil {
		base, ok := raw.(string)
		if !ok {
			return cfg, &searchProbeFailure{Status: 400, Code: "invalid_api_base"}
		}
		if strings.TrimSpace(base) != "" {
			cfg.Endpoint = strings.TrimSpace(base)
		}
	}
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Opaque != "" {
		return cfg, &searchProbeFailure{Status: 400, Code: "invalid_api_base"}
	}
	return cfg, nil
}

// runSearchProbe 执行有限时长、有限响应体的一次搜索；ctx继承入站取消，base只复用传输和较短超时，cfg决定真实请求。
// 返回规范化结果、上游状态或安全失败；完整endpoint允许企业自建服务，重定向一律拒绝，避免认证信息被带到其他主机。
func runSearchProbe(ctx context.Context, base *http.Client, cfg searchProbeConfig) ([]searchProbeResult, int, *searchProbeFailure) {
	ctx, cancel := context.WithTimeout(ctx, searchProbeTimeout)
	defer cancel()
	payload := map[string]any{"query": cfg.Query, "max_results": 3}
	header, value := "Authorization", "Bearer "+cfg.Key
	if cfg.Provider == "serper" {
		payload = map[string]any{"q": cfg.Query, "num": 3}
		header, value = "X-API-KEY", cfg.Key
	}
	if cfg.Provider == "tavily" {
		payload["search_depth"] = "basic"
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, 0, &searchProbeFailure{Status: 400, Code: "invalid_api_base"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(header, value)
	client := http.Client{Timeout: searchProbeTimeout}
	if base != nil {
		client.Transport = base.Transport
		if base.Timeout > 0 && base.Timeout < client.Timeout {
			client.Timeout = base.Timeout
		}
	}
	client.CheckRedirect = rejectSearchProbeRedirect
	response, err := client.Do(req)
	if err != nil {
		failure := searchProbeRequestFailure(ctx, err)
		return nil, 0, &failure
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		status := response.StatusCode
		// 3xx不能被测试页面视为导航成功，保留真实上游状态供排查。
		if status < 400 {
			status = 502
		}
		return nil, response.StatusCode, &searchProbeFailure{Status: status, Code: "upstream_http_error", Upstream: response.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, searchProbeResponseLimit+1))
	if err != nil {
		failure := searchProbeRequestFailure(ctx, err)
		failure.Upstream = response.StatusCode
		return nil, response.StatusCode, &failure
	}
	if ctx.Err() != nil {
		failure := searchProbeRequestFailure(ctx, ctx.Err())
		return nil, response.StatusCode, &failure
	}
	results, err := parseSearchProbeResults(cfg.Provider, body, cfg.Key)
	if err != nil || len(body) > searchProbeResponseLimit {
		return nil, response.StatusCode, &searchProbeFailure{Status: 502, Code: "invalid_search_response", Upstream: response.StatusCode}
	}
	return results, response.StatusCode, nil
}

// rejectSearchProbeRedirect 阻止任何自动重定向；参数由http.Client传入，返回ErrUseLastResponse以保留原状态且不发送第二次请求。
func rejectSearchProbeRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// searchProbeRequestFailure 将超时、入站取消与普通网络故障归类；不公开err字符串，因为其中可能含端点或凭据。
func searchProbeRequestFailure(ctx context.Context, err error) searchProbeFailure {
	if errors.Is(ctx.Err(), context.Canceled) {
		return searchProbeFailure{Status: 408, Code: "request_cancelled"}
	}
	var timeout interface{ Timeout() bool }
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
		return searchProbeFailure{Status: 504, Code: "request_timeout"}
	}
	return searchProbeFailure{Status: 502, Code: "request_failed"}
}

// parseSearchProbeResults 按官方搜索响应形状解析真实结果；provider决定results或organic，raw为上游正文，secret用于最后一道回显脱敏。
// 空数组是成功搜索，缺字段、null、错误对象或错误条目不是；返回规范化展示字段，绝不返回原始供应商对象。
func parseSearchProbeResults(provider string, raw []byte, secret string) ([]searchProbeResult, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	field := "results"
	if provider == "serper" {
		field = "organic"
	}
	encoded, exists := obj[field]
	if !exists || bytes.Equal(bytes.TrimSpace(encoded), []byte("null")) {
		return nil, errors.New("missing results")
	}
	if _, exists := obj["error"]; exists {
		return nil, errors.New("upstream error envelope")
	}
	var rows []map[string]any
	if err := json.Unmarshal(encoded, &rows); err != nil {
		return nil, err
	}
	results := make([]searchProbeResult, 0, len(rows))
	for _, row := range rows {
		title, ok := row["title"].(string)
		if !ok {
			return nil, errors.New("invalid title")
		}
		urlField, snippetField := "url", "snippet"
		if provider == "serper" {
			urlField = "link"
		}
		if provider == "tavily" {
			snippetField = "content"
		}
		link, ok := row[urlField].(string)
		if !ok {
			return nil, errors.New("invalid URL")
		}
		parsed, err := url.Parse(link)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
			return nil, errors.New("invalid result URL")
		}
		snippet, ok := row[snippetField].(string)
		if !ok {
			return nil, errors.New("invalid snippet")
		}
		if secret != "" {
			title = strings.ReplaceAll(title, secret, "[REDACTED]")
			link = strings.ReplaceAll(link, secret, "[REDACTED]")
			snippet = strings.ReplaceAll(snippet, secret, "[REDACTED]")
		}
		results = append(results, searchProbeResult{Title: title, URL: link, Snippet: snippet})
	}
	return results, nil
}

// writeSearchProbeFailure 输出稳定可识别的失败契约；参数w为响应，failure只含安全代码与状态，不含供应商原始内容。
// 前端按error.code国际化，不把测试失败误认为成功；upstream_status仅在已经收到上游响应时存在。
func writeSearchProbeFailure(w http.ResponseWriter, failure searchProbeFailure) {
	messages := map[string]string{"invalid_request": "invalid search probe request", "unsupported_provider": "search provider is not supported", "missing_credential": "a resolved search API key is required", "invalid_api_base": "search API base must be a complete HTTP endpoint without credentials, query or fragment", "not_found": "search tool not found", "store_unavailable": "search tool configuration could not be read", "upstream_http_error": "search provider returned an unsuccessful HTTP status", "invalid_search_response": "search provider returned an invalid search response", "request_timeout": "search provider request timed out", "request_cancelled": "search provider request was cancelled", "request_failed": "search provider request failed"}
	body := map[string]any{"status": "error", "error_type": failure.Code, "error": map[string]any{"code": failure.Code, "type": failure.Code, "message": messages[failure.Code]}}
	if failure.Upstream != 0 {
		body["upstream_status"] = failure.Upstream
	}
	util.WriteJSON(w, failure.Status, body)
}
