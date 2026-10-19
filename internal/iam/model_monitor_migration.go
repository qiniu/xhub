package iam

import (
	"context"
	"strings"

	"xorm.io/xorm"
)

const outputTPSMigration = "output-tps-v1"

// migrateMonitorTPS 按现存账单一次性修正历史 TPS；ctx 控制取消，返回迁移或提交错误。
// Migrate 在表结构就绪后调用；与用量写入共用事务锁，聚合修正和完成标记原子提交，重复启动不重算。
// 只更新 TPS 累加和/样本数，不重放账单、不修改费用、Token、请求数和 TTFT；已清理原始事件的桶标记为无 TPS 样本。
func (db *DB) migrateMonitorTPS(ctx context.Context) error {
	return db.tx(ctx, func(s *xorm.Session) error {
		rows, err := s.QueryString("SELECT name FROM model_monitor_migrations WHERE name = ?", outputTPSMigration)
		if err != nil || len(rows) != 0 {
			return err
		}
		// 不能用旧 TPS 总和按输入/输出占比缩放：每次请求的计时不同，且旧总和混入了同步请求。
		// 先清空可重建的指标，再在同一事务中由原始事实恢复；无原始事件时保持未知，不伪造速度。
		if _, err := s.Exec("UPDATE model_monitor_buckets SET output_tps_sum = 0, output_tps_samples = 0"); err != nil {
			return err
		}
		// 操作名来自包内固定能力表，不能来自请求；SQL 与实时聚合共用同一文本操作范围。
		calls := "'" + strings.Join(textTimingCalls, "','") + "'"
		_, err = s.Exec(`WITH corrected AS (
 SELECT to_timestamp(floor(extract(epoch FROM ts)/900)*900) AS bucket_start,
 organization_id,team_id,project_id,user_id,key_id,owner_type,supplier,upstream_model AS model,
 SUM(completion_tokens::double precision * 1000 / (duration_ms-ttft_ms)) AS tps_sum,
 COUNT(*) AS tps_samples
 FROM usage_events
 WHERE streamed AND NOT cache_hit AND status IN ('','success','completed') AND http_status < 400
 AND ttft_ms >= 0 AND duration_ms > ttft_ms AND completion_tokens > 0 AND call_type IN (` + calls + `)
 GROUP BY 1,2,3,4,5,6,7,8,9
 ) UPDATE model_monitor_buckets b SET output_tps_sum=c.tps_sum,output_tps_samples=c.tps_samples FROM corrected c
 WHERE b.bucket_start=c.bucket_start AND b.organization_id=c.organization_id AND b.team_id=c.team_id
 AND b.project_id=c.project_id AND b.user_id=c.user_id AND b.key_id=c.key_id AND b.owner_type=c.owner_type
 AND b.supplier=c.supplier AND b.model=c.model`)
		if err != nil {
			return err
		}
		_, err = s.Exec("INSERT INTO model_monitor_migrations(name) VALUES (?)", outputTPSMigration)
		return err
	})
}
