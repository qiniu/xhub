package iam

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"xorm.io/xorm"
)

// ModelMonitorRow 表示同一供应商实际模型的汇总或日趋势；nil 性能指标表示无实测样本。
type ModelMonitorRow struct {
	Kind             string   `json:"kind"`
	Supplier         string   `json:"supplier"`
	Model            string   `json:"model"`
	Day              string   `json:"day"`
	Requests         int64    `json:"requests"`
	FailedRequests   int64    `json:"failed_requests"`
	PromptTokens     int64    `json:"prompt_tokens"`
	CompletionTokens int64    `json:"completion_tokens"`
	TotalTokens      int64    `json:"total_tokens"`
	Cost             float64  `json:"cost"`
	AvgTPS           *float64 `xorm:"avg_tps" json:"avg_tps"`
	AvgTTFTMs        *float64 `xorm:"avg_ttft_ms" json:"avg_ttft_ms"`
	AvgLatencyMs     *float64 `xorm:"avg_latency_ms" json:"avg_latency_ms"`
	TPSSamples       int64    `xorm:"tps_samples" json:"tps_samples"`
	TTFTSamples      int64    `xorm:"ttft_samples" json:"ttft_samples"`
}

// ModelMonitorQuery 提供可由桶表达的授权和半开日期筛选；Supplier 为连接身份。
type ModelMonitorQuery struct {
	UsageQuery
	Supplier string
}

// MonitorDeployment 是当前启用且授权的部署身份；CrossSafe 表示该连接及上游型号没有无权或停用的共享部署。
// 普通用户监控查询使用；直接请求匹配别名，只有 CrossSafe 为真时才允许匹配路由组或回退来源。
type MonitorDeployment struct {
	Alias     string `json:"alias"`
	Supplier  string `json:"supplier"`
	Model     string `json:"model"`
	CrossSafe bool   `json:"cross_safe"`
}

// ModelMonitorCurrent 按当前授权部署与来源别名筛选原始使用记录并聚合，不改变账单。
// ctx/q/tzMinutes 控制已鉴权的范围、时间及用户筛选，deployments 是当前有效部署，allowed 是来源授权（nil 不限）。
// 返回汇总、模型和日期趋势；空部署返回空汇总，存储或授权条件异常返回错误，不退化成全量数据。
// 模型监控接口调用；必须回查 usage_events，因为历史预聚合桶未保存公开别名、无法安全区分撤销授权的同上游请求。
func (db *DB) ModelMonitorCurrent(ctx context.Context, q ModelMonitorQuery, tzMinutes int, deployments []MonitorDeployment, allowed []string) ([]ModelMonitorRow, error) {
	if q.Status != "" || q.SessionID != "" || q.RequestID != "" || q.Search != "" || q.Cond == nil || q.From.IsZero() || q.To.IsZero() || !q.From.Before(q.To) || q.To.Sub(q.From) > 90*24*time.Hour || tzMinutes < -840 || tzMinutes > 840 || tzMinutes%15 != 0 || !q.From.Equal(q.From.Truncate(15*time.Minute)) || !q.To.Equal(q.To.Truncate(15*time.Minute)) {
		return nil, fmt.Errorf("invalid model monitor scope or window")
	}
	if len(deployments) == 0 {
		return []ModelMonitorRow{}, nil
	}
	raw, err := json.Marshal(deployments)
	if err != nil {
		return nil, err
	}
	// nil 代表来源不限；非 nil 空集合代表全部拒绝，不能被 JSON null 当成不限。
	unrestricted := allowed == nil
	if allowed == nil {
		allowed = []string{}
	}
	aliases, err := json.Marshal(allowed)
	if err != nil {
		return nil, err
	}
	// 明细查询不能依赖聚合桶，需要限制查询时间；超时向调用方返回错误，不能伪装成空统计。
	queryCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	s := db.session(queryCtx).Table("usage_events").Where(q.Cond).And("ts >= ? AND ts < ?", q.From.UTC(), q.To.UTC())
	defer s.Close()
	// JSON 仅包含服务端配置与授权结果，不拼接请求输入；EXISTS 避免重复部署对同一账单重复计数。
	// 请求别名与实际身份均匹配时按该身份展示；组和回退的跨别名匹配还须来源仍授权且共享身份无歧义。
	// 历史事件没有实际成员 ID，直接匹配不证明具体部署；无权访问的身份不能通过组名绕过。
	s = s.And(`EXISTS (SELECT 1 FROM jsonb_to_recordset(?::jsonb) AS d(alias text, supplier text, model text, cross_safe boolean) WHERE d.supplier = usage_events.supplier AND d.model = usage_events.upstream_model AND (d.alias = usage_events.model OR (d.cross_safe AND (? OR usage_events.model IN (SELECT jsonb_array_elements_text(?::jsonb))))))`, string(raw), unrestricted, string(aliases)).And("status NOT IN (?, ?)", "executing", "polling")
	for _, f := range []struct{ column, value string }{{"user_id", q.UserID}, {"team_id", q.TeamID}, {"project_id", q.ProjectID}, {"key_id", q.KeyID}, {"upstream_model", q.Model}, {"supplier", q.Supplier}} {
		if f.value != "" {
			s = s.And(f.column+" = ?", f.value)
		}
	}
	s = q.applyLists(s)
	day := "to_char((ts AT TIME ZONE 'UTC') + INTERVAL '" + strconv.Itoa(tzMinutes) + " minutes', 'YYYY-MM-DD')"
	// 计时样本条件与 accumulateMonitor/OutputTPS 共用文本操作表和边界，避免改变 TPS 口径。
	calls := "'" + strings.Join(textTimingCalls, "','") + "'"
	success := "status IN ('','success','completed') AND http_status < 400"
	timing := success + " AND NOT cache_hit AND ttft_ms >= 0 AND duration_ms >= ttft_ms AND call_type IN (" + calls + ")"
	tpSample := timing + " AND streamed AND completion_tokens > 0 AND duration_ms > ttft_ms"
	latency := success + " AND duration_ms > 0"
	sel := "CASE WHEN GROUPING(supplier)=1 THEN 'summary' WHEN GROUPING(" + day + ")=1 THEN 'model' ELSE 'trend' END AS kind, COALESCE(supplier,'') AS supplier, COALESCE(upstream_model,'') AS model, COALESCE(" + day + ",'') AS day, COUNT(*) AS requests, COUNT(*) FILTER (WHERE NOT (" + success + ")) AS failed_requests, COALESCE(SUM(prompt_tokens),0) AS prompt_tokens, COALESCE(SUM(completion_tokens),0) AS completion_tokens, COALESCE(SUM(prompt_tokens+completion_tokens),0) AS total_tokens, COALESCE(SUM(cost),0) AS cost, AVG(CASE WHEN " + tpSample + " THEN completion_tokens::double precision * 1000 / NULLIF(duration_ms-ttft_ms,0) END) AS avg_tps, AVG(CASE WHEN " + timing + " THEN ttft_ms END) AS avg_ttft_ms, AVG(CASE WHEN " + latency + " THEN duration_ms END) AS avg_latency_ms, COUNT(*) FILTER (WHERE " + tpSample + ") AS tps_samples, COUNT(*) FILTER (WHERE " + timing + ") AS ttft_samples"
	rows := []ModelMonitorRow{}
	err = s.Select(sel).GroupBy("GROUPING SETS ((), (supplier,upstream_model), (supplier,upstream_model," + day + "))").OrderBy("kind,supplier,model,day").Find(&rows)
	return rows, mapErr(err)
}

// monitorKey 保留完整授权归属；同时间桶同维度的请求合并，高基数归属仍会增加桶数。
type monitorKey struct {
	Bucket                                                time.Time
	Org, Team, Project, User, Key, Owner, Supplier, Model string
}

// monitorDelta 是可合并与反向修正的统计状态，平均值由累加和/样本数获得。
type monitorDelta struct {
	Requests, Failed, Prompt, Completion    int64
	Cost, TPS, TTFT, Latency                float64
	TPSSamples, TTFTSamples, LatencySamples int64
}

// accumulateMonitor 把终态记录 r 按 sign 方向加入批次 out；账单及任务结算调用。
// 无返回值；进行中不计数，失败和缓存不产生性能样本；TTFT 保留首字节口径，TPS 只使用有效流式请求的输出 Token。
func accumulateMonitor(out map[monitorKey]monitorDelta, r UsageRecord, sign int64) {
	if r.Status == "executing" || r.Status == "polling" {
		return
	}
	k := monitorKey{stamp(r.TS).Truncate(15 * time.Minute), r.OrganizationID, r.TeamID, r.ProjectID, r.UserID, r.KeyID, ownerType(r), r.Supplier, r.UpstreamModel}
	d := out[k]
	d.Requests += sign
	d.Prompt += sign * int64(r.PromptTokens)
	d.Completion += sign * int64(r.CompletionTokens)
	d.Cost += float64(sign) * r.Cost
	success := r.HTTPStatus < 400 && (status(r) == "success" || status(r) == "completed")
	if !success {
		d.Failed += sign
	}
	if success && r.DurationMS > 0 {
		d.Latency += float64(sign) * float64(r.DurationMS)
		d.LatencySamples += sign
	}
	// 同步首字节可用于响应延迟，但 TPS 必须排除同步读尾时间，并与请求日志共用输出公式。
	if textTimingSample(r) {
		d.TTFT += float64(sign) * float64(*r.TTFTMs)
		d.TTFTSamples += sign
		if tps := OutputTPS(r); tps != nil {
			d.TPS += float64(sign) * *tps
			d.TPSSamples += sign
		}
	}
	out[k] = d
}

// flushMonitor 在调用方事务 s 内每桶一次 UPSERT，values 已按新事件去重合并。
// 返回存储错误，调用方整体回滚；键排序统一锁行顺序，减少并发热点和死锁。
// 输出 TPS 写入独立指标列，兼容旧版本继续写旧 TPS 列时不会混合统计口径。
func flushMonitor(s *xorm.Session, values map[monitorKey]monitorDelta) error {
	keys := make([]monitorKey, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if !a.Bucket.Equal(b.Bucket) {
			return a.Bucket.Before(b.Bucket)
		}
		aa := []string{a.Org, a.Team, a.Project, a.User, a.Key, a.Owner, a.Supplier, a.Model}
		bb := []string{b.Org, b.Team, b.Project, b.User, b.Key, b.Owner, b.Supplier, b.Model}
		for n := range aa {
			if aa[n] != bb[n] {
				return aa[n] < bb[n]
			}
		}
		return false
	})
	for _, k := range keys {
		d := values[k]
		_, err := s.Exec(`INSERT INTO model_monitor_buckets
 (bucket_start,organization_id,team_id,project_id,user_id,key_id,owner_type,supplier,model,requests,failed_requests,prompt_tokens,completion_tokens,cost,output_tps_sum,output_tps_samples,ttft_sum,ttft_samples,latency_sum,latency_samples)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT (bucket_start,organization_id,team_id,project_id,user_id,key_id,owner_type,supplier,model)
 DO UPDATE SET requests=model_monitor_buckets.requests+EXCLUDED.requests,
 failed_requests=model_monitor_buckets.failed_requests+EXCLUDED.failed_requests,
 prompt_tokens=model_monitor_buckets.prompt_tokens+EXCLUDED.prompt_tokens,
 completion_tokens=model_monitor_buckets.completion_tokens+EXCLUDED.completion_tokens,
 cost=model_monitor_buckets.cost+EXCLUDED.cost,
 output_tps_sum=model_monitor_buckets.output_tps_sum+EXCLUDED.output_tps_sum,output_tps_samples=model_monitor_buckets.output_tps_samples+EXCLUDED.output_tps_samples,
 ttft_sum=model_monitor_buckets.ttft_sum+EXCLUDED.ttft_sum,ttft_samples=model_monitor_buckets.ttft_samples+EXCLUDED.ttft_samples,
 latency_sum=model_monitor_buckets.latency_sum+EXCLUDED.latency_sum,latency_samples=model_monitor_buckets.latency_samples+EXCLUDED.latency_samples`,
			k.Bucket, k.Org, k.Team, k.Project, k.User, k.Key, k.Owner, k.Supplier, k.Model, d.Requests, d.Failed, d.Prompt, d.Completion, d.Cost, d.TPS, d.TPSSamples, d.TTFT, d.TTFTSamples, d.Latency, d.LatencySamples)
		if err != nil {
			return mapErr(err)
		}
	}
	return nil
}

// ModelMonitor 读取预聚合桶；ctx 控制取消，q 提供授权/半开窗口，tzMinutes 为东向时区。
// 返回总计、模型及日趋势和存储错误；监控接口调用，无写入。缺失权限或未对齐窗口拒绝。
// 单条 SQL 保持三种结果同一快照；裸时间列使用索引，不回扫 usage_events。
func (db *DB) ModelMonitor(ctx context.Context, q ModelMonitorQuery, tzMinutes int) ([]ModelMonitorRow, error) {
	if q.Status != "" || q.SessionID != "" || q.RequestID != "" || q.Search != "" || q.Cond == nil || q.From.IsZero() || q.To.IsZero() || !q.From.Before(q.To) || q.To.Sub(q.From) > 90*24*time.Hour || tzMinutes < -840 || tzMinutes > 840 || tzMinutes%15 != 0 || !q.From.Equal(q.From.Truncate(15*time.Minute)) || !q.To.Equal(q.To.Truncate(15*time.Minute)) {
		return nil, fmt.Errorf("invalid model monitor scope or window")
	}
	s := db.session(ctx).Table("model_monitor_buckets").Where(q.Cond).And("bucket_start >= ? AND bucket_start < ?", q.From.UTC(), q.To.UTC())
	defer s.Close()
	for _, f := range []struct{ column, value string }{{"user_id", q.UserID}, {"team_id", q.TeamID}, {"project_id", q.ProjectID}, {"key_id", q.KeyID}, {"model", q.Model}, {"supplier", q.Supplier}} {
		if f.value != "" {
			s = s.And(f.column+" = ?", f.value)
		}
	}
	s = q.applyLists(s)
	// 显式 UTC 转换消除数据库会话时区对浏览器日期的影响。
	day := "to_char((bucket_start AT TIME ZONE 'UTC') + INTERVAL '" + strconv.Itoa(tzMinutes) + " minutes', 'YYYY-MM-DD')"
	sel := "CASE WHEN GROUPING(supplier)=1 THEN 'summary' WHEN GROUPING(" + day + ")=1 THEN 'model' ELSE 'trend' END AS kind, COALESCE(supplier,'') AS supplier, COALESCE(model,'') AS model, COALESCE(" + day + ",'') AS day, COALESCE(SUM(requests),0) AS requests, COALESCE(SUM(failed_requests),0) AS failed_requests, COALESCE(SUM(prompt_tokens),0) AS prompt_tokens, COALESCE(SUM(completion_tokens),0) AS completion_tokens, COALESCE(SUM(prompt_tokens+completion_tokens),0) AS total_tokens, COALESCE(SUM(cost),0) AS cost, SUM(output_tps_sum)/NULLIF(SUM(output_tps_samples),0) AS avg_tps, SUM(ttft_sum)/NULLIF(SUM(ttft_samples),0) AS avg_ttft_ms, SUM(latency_sum)/NULLIF(SUM(latency_samples),0) AS avg_latency_ms, COALESCE(SUM(output_tps_samples),0) AS tps_samples, COALESCE(SUM(ttft_samples),0) AS ttft_samples"
	rows := []ModelMonitorRow{}
	err := s.Select(sel).GroupBy("GROUPING SETS ((), (supplier,model), (supplier,model," + day + "))").OrderBy("kind,supplier,model,day").Find(&rows)
	return rows, mapErr(err)
}
