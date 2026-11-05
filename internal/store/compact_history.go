package store

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"
)

// historyBlock 是同一隔离范围内可复用的完整历史项；JSON 只用于向 PostgreSQL 批量传输。
type historyBlock struct {
	Digest     string `json:"digest"`
	Payload    []byte `json:"payload"`
	Size       int    `json:"size"`
	Compressed bool   `json:"compressed"`
}

// initHistoryTables 创建新格式的内容块和响应索引；调用方为 Store.Open，失败时不启动半成品存储。
// 只使用新增表，不修改旧 kv；数据库连接应位于预期的 search_path，两个语句均可安全重复执行。
func initHistoryTables(ctx context.Context, db *sql.DB) error {
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS conversation_history_blocks (scope TEXT NOT NULL, digest TEXT NOT NULL, payload BYTEA NOT NULL, raw_size BIGINT NOT NULL CHECK(raw_size >= 0), compressed BOOLEAN NOT NULL, PRIMARY KEY(scope,digest))`,
		`CREATE TABLE IF NOT EXISTS conversation_history_manifests (id TEXT PRIMARY KEY, scope TEXT NOT NULL, digests JSONB NOT NULL)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("initialize conversation history storage: %w", err)
		}
	}
	return nil
}

// legacyHistoryScope 为旧记录构建独立作用域；参数是旧历史键，返回不可逆摘要。
// 迁移时无法从旧键恢复用户范围，因此不能把不同旧记录的内容合并到同一作用域。
func legacyHistoryScope(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "legacy:" + hex.EncodeToString(sum[:])
}

// encodeHistory 把历史数组拆为去重内容块及顺序索引；输入为历史 JSON，供写入和迁移使用。
// 返回可批量插入的块与摘要序列；损坏或非数组输入返回错误，所有内容和顺序均保留。
func encodeHistory(value string) ([]historyBlock, []string, error) {
	var items []json.RawMessage
	if err := json.Unmarshal([]byte(value), &items); err != nil || items == nil {
		return nil, nil, fmt.Errorf("conversation history must be a JSON array")
	}
	blocks := make([]historyBlock, 0, len(items))
	ids := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		sum := sha256.Sum256(item)
		id := hex.EncodeToString(sum[:])
		ids = append(ids, id)
		if seen[id] {
			continue
		}
		seen[id] = true
		block := historyBlock{Digest: id, Payload: append([]byte(nil), item...), Size: len(item)}
		if len(item) >= 256 {
			var out bytes.Buffer
			zw, err := zlib.NewWriterLevel(&out, zlib.BestSpeed)
			if err != nil {
				return nil, nil, err
			}
			if _, err = zw.Write(item); err != nil {
				return nil, nil, err
			}
			if err = zw.Close(); err != nil {
				return nil, nil, err
			}
			if out.Len() < len(item) {
				block.Payload = out.Bytes()
				block.Compressed = true
			}
		}
		blocks = append(blocks, block)
	}
	return blocks, ids, nil
}

// decodeHistoryBlock 验证并解压单个内容块；参数为存储行和预期摘要，供读取时还原历史。
// 大小、压缩流或摘要错误均返回失败；解压受声明长度约束，避免损坏数据造成无限展开。
func decodeHistoryBlock(b historyBlock) ([]byte, error) {
	if b.Size < 0 || int64(b.Size) == int64(^uint64(0)>>1) {
		return nil, errors.New("invalid history block size")
	}
	data := b.Payload
	if b.Compressed {
		r, err := zlib.NewReader(bytes.NewReader(b.Payload))
		if err != nil {
			return nil, fmt.Errorf("invalid history compression: %w", err)
		}
		data, err = io.ReadAll(io.LimitReader(r, int64(b.Size)+1))
		closeErr := r.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	if len(data) != b.Size {
		return nil, errors.New("history block length mismatch")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != b.Digest {
		return nil, errors.New("history block digest mismatch")
	}
	if !json.Valid(data) {
		return nil, errors.New("invalid history block JSON")
	}
	return data, nil
}

// putHistoryTx 在调用方事务中写入块和索引；参数分别是作用域、响应键及完整历史。
// 所有块和索引同一事务发布，已有块只复用不覆盖；编码或数据库出错由调用方回滚。
func putHistoryTx(ctx context.Context, tx *sql.Tx, key, scope, value string, onlyNew bool) (bool, error) {
	blocks, ids, err := encodeHistory(value)
	if err != nil {
		return false, err
	}
	// 先锁定索引，防止迁移与实时写入覆盖；迁移只为不存在的索引创建新副本。
	if onlyNew {
		var present bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conversation_history_manifests WHERE id=$1)`, key).Scan(&present)
		if err != nil {
			return false, err
		}
		if present {
			return false, nil
		}
	}
	// 并发响应按相同摘要顺序持有块行锁，避免相反消息顺序造成数据库死锁。
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].Digest < blocks[j].Digest })
	payload, err := json.Marshal(blocks)
	if err != nil {
		return false, err
	}
	if len(blocks) > 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO conversation_history_blocks(scope,digest,payload,raw_size,compressed)
 SELECT $1, b->>'digest', decode(b->>'payload','base64'), (b->>'size')::bigint, (b->>'compressed')::boolean
 FROM jsonb_array_elements($2::jsonb) AS b ON CONFLICT(scope,digest) DO NOTHING`, scope, string(payload))
		if err != nil {
			return false, err
		}
	}
	manifest, err := json.Marshal(ids)
	if err != nil {
		return false, err
	}
	if onlyNew {
		res, err := tx.ExecContext(ctx, `INSERT INTO conversation_history_manifests(id,scope,digests) VALUES($1,$2,$3::jsonb) ON CONFLICT(id) DO NOTHING`, key, scope, string(manifest))
		if err != nil {
			return false, err
		}
		n, err := res.RowsAffected()
		return n == 1, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO conversation_history_manifests(id,scope,digests) VALUES($1,$2,$3::jsonb) ON CONFLICT(id) DO UPDATE SET scope=EXCLUDED.scope,digests=EXCLUDED.digests`, key, scope, string(manifest))
	return err == nil, err
}

// lockHistoryKey 在事务内按响应键串行化迁移和新旧写入，避免并发切换时留下过期索引。
// 参数 tx 必须是正在执行的事务；锁随事务提交或回滚释放，数据库错误原样返回。
func lockHistoryKey(ctx context.Context, tx *sql.Tx, key string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key)
	return err
}

// WriteHistory 保存一份完整续聊历史；key 是身份隔离后的响应键，scope 是可信隔离范围。
// 环境变量 XHUB_COMPACT_HISTORY=1 启用新写入，默认旧格式；切回旧写入时事务删除旧的新格式索引，避免读到过期内容。
func (s *Store) WriteHistory(ctx context.Context, key, scope, value string) error {
	if s == nil || s.DB == nil {
		return errors.New("conversation storage unavailable")
	}
	if key == "" || scope == "" {
		return errors.New("conversation history scope required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockHistoryKey(ctx, tx, key); err != nil {
		return err
	}
	if os.Getenv("XHUB_COMPACT_HISTORY") == "1" {
		if _, err = putHistoryTx(ctx, tx, key, scope, value, false); err != nil {
			return err
		}
	} else {
		if _, err = tx.ExecContext(ctx, `DELETE FROM conversation_history_manifests WHERE id=$1`, key); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO kv(kind,id,body,created_at) VALUES($1,$2,$3,$4) ON CONFLICT(kind,id) DO UPDATE SET body=EXCLUDED.body`, conversationKind, key, value, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReadHistory 按响应键读取并验证新格式，未命中时读取旧 kv；scope 是调用链计算的可信范围。
// 所有索引及块在同一快照读取；缺块、损坏或范围不匹配返回错误，不退化成空历史或旧数据。
func (s *Store) ReadHistory(ctx context.Context, key, scope string) (string, error) {
	if s == nil || s.DB == nil {
		return "", errors.New("conversation storage unavailable")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var actual string
	var manifest []byte
	err = tx.QueryRowContext(ctx, `SELECT scope,digests FROM conversation_history_manifests WHERE id=$1`, key).Scan(&actual, &manifest)
	if errors.Is(err, sql.ErrNoRows) {
		var value string
		err = tx.QueryRowContext(ctx, `SELECT body FROM kv WHERE kind=$1 AND id=$2`, conversationKind, key).Scan(&value)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return value, err
	}
	if err != nil {
		return "", err
	}
	if actual != scope && actual != legacyHistoryScope(key) {
		return "", errors.New("conversation history scope mismatch")
	}
	var ids []string
	if err = json.Unmarshal(manifest, &ids); err != nil || ids == nil {
		return "", errors.New("invalid history manifest")
	}
	value, err := reconstructHistoryTx(ctx, tx, actual, ids)
	if err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return value, nil
}

// reconstructHistoryTx 按索引批量读块并验证完整性；调用方事务保证快照一致。
// scope 和 ids 指向一个响应的有序内容；任一块缺失或校验失败时返回错误。
func reconstructHistoryTx(ctx context.Context, tx *sql.Tx, scope string, ids []string) (string, error) {
	decoded := make(map[string][]byte, len(ids))
	if len(ids) > 0 {
		rows, err := tx.QueryContext(ctx, `SELECT digest,payload,raw_size,compressed FROM conversation_history_blocks WHERE scope=$1 AND digest=ANY($2::text[])`, scope, ids)
		if err != nil {
			return "", err
		}
		defer rows.Close()
		for rows.Next() {
			var b historyBlock
			if err = rows.Scan(&b.Digest, &b.Payload, &b.Size, &b.Compressed); err != nil {
				return "", err
			}
			data, e := decodeHistoryBlock(b)
			if e != nil {
				return "", e
			}
			decoded[b.Digest] = data
		}
		if err = rows.Err(); err != nil {
			return "", err
		}
	}
	var out bytes.Buffer
	out.WriteByte('[')
	for i, id := range ids {
		b, ok := decoded[id]
		if !ok {
			return "", errors.New("conversation history block missing")
		}
		if i > 0 {
			out.WriteByte(',')
		}
		out.Write(b)
	}
	out.WriteByte(']')
	return out.String(), nil
}

// MigrateLegacyHistories 迁移最多 limit 条旧历史；after 是上次返回的键，空串从头开始。
// 每条独立事务并锁定旧值，新格式已存在则跳过；保留全部旧副本，失败返回已完成进度及错误。
func (s *Store) MigrateLegacyHistories(ctx context.Context, after string, limit int) (string, int, error) {
	if s == nil || s.DB == nil {
		return after, 0, errors.New("conversation storage unavailable")
	}
	if limit < 1 || limit > 1000 {
		return after, 0, errors.New("migration limit must be between 1 and 1000")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT k.id FROM kv k WHERE k.kind=$1 AND k.id LIKE 'session_route:v1:history:%' AND k.id>$2 AND NOT EXISTS(SELECT 1 FROM conversation_history_manifests m WHERE m.id=k.id) ORDER BY k.id LIMIT $3`, conversationKind, after, limit)
	if err != nil {
		return after, 0, err
	}
	var keys []string
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return after, 0, err
		}
		keys = append(keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return after, 0, err
	}
	done := 0
	for _, key := range keys {
		tx, e := s.DB.BeginTx(ctx, nil)
		if e != nil {
			return after, done, e
		}
		e = lockHistoryKey(ctx, tx, key)
		var value string
		if e == nil {
			e = tx.QueryRowContext(ctx, `SELECT body FROM kv WHERE kind=$1 AND id=$2 FOR UPDATE`, conversationKind, key).Scan(&value)
		}
		var inserted bool
		if e == nil {
			inserted, e = putHistoryTx(ctx, tx, key, legacyHistoryScope(key), value, true)
		}
		if e == nil && inserted {
			var ids []string
			var manifest []byte
			e = tx.QueryRowContext(ctx, `SELECT digests FROM conversation_history_manifests WHERE id=$1`, key).Scan(&manifest)
			if e == nil {
				e = json.Unmarshal(manifest, &ids)
			}
			var restored string
			if e == nil {
				restored, e = reconstructHistoryTx(ctx, tx, legacyHistoryScope(key), ids)
			}
			if e == nil {
				var before, after bytes.Buffer
				e = json.Compact(&before, []byte(value))
				if e == nil {
					e = json.Compact(&after, []byte(restored))
				}
				if e == nil && !bytes.Equal(before.Bytes(), after.Bytes()) {
					e = errors.New("migrated history mismatch")
				}
			}
		}
		if e == nil {
			e = tx.Commit()
		} else {
			tx.Rollback()
		}
		if e != nil {
			return after, done, e
		}
		if inserted {
			done++
		}
		after = key
	}
	return after, done, nil
}
