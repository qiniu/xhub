// Package usagespool 为最终用量提供独立于 PostgreSQL/Redis 的本机持久快照，不调用或重试上游。
package usagespool

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/sunqirui1987/xhub/internal/iam"
)

const (
	// DefaultBytes 限制本机待结算文件与并发在途预留总量，默认一 GiB，不代表可跨磁盘故障恢复。
	DefaultBytes int64 = 1 << 30
	// DefaultEntries 限制待结算文件及在途请求数量，防止无界目录扫描。
	DefaultEntries = 8192
	// DefaultRecordBytes 为每次准入保守预留的最大记录空间；完整正文不得静默截断以适应上限。
	DefaultRecordBytes int64 = 16 << 20
)

var (
	ErrUnavailable = errors.New("usage spool unavailable")
	ErrFull        = errors.New("usage spool capacity exhausted")
	ErrCorrupt     = errors.New("usage spool has quarantined evidence")
	ErrConflict    = errors.New("usage spool request snapshot conflict")
)

// Config 为启动时固定的目录和容量；目录必须专属于一个网关实例，多个实例应挂不同持久卷。
type Config struct {
	Dir            string
	MaxBytes       int64
	MaxEntries     int
	MaxRecordBytes int64
}

// Status 只包含数量和健康状态，不暴露请求正文、费用归属或磁盘文件内容。
type Status struct {
	Pending, Reserved, Quarantined int
	Bytes, ReservedBytes           int64
	Healthy                        bool
}

// envelope 以版本和内容摘要保护完整 UsageRecord；摘要检测损坏，不是来自不可信调用方的授权签名。
type envelope struct {
	Version  int             `json:"version"`
	Checksum string          `json:"checksum"`
	Payload  json.RawMessage `json:"payload"`
}

// Spool 独占本机目录；进程锁防止两实例共享目录，mu 保护文件更新、容量预留和失败重试时间。
type Spool struct {
	mu             sync.Mutex
	root           *os.Root
	lock           *flock.Flock
	config         Config
	reserved       map[string]bool
	activeDelivery map[string]bool
	retry          map[string]time.Time
	lastErr        error
	closed         bool
}

// Open 创建或打开专用目录并取得非阻塞进程锁，返回可恢复队列或错误；网关启动调用。
// 参数容量零值采用默认值；禁止根目录、最终路径符号链接和宽松权限，目录0700/文件0600，失败关闭已打开资源。
func Open(config Config) (*Spool, error) {
	if config.MaxBytes == 0 {
		config.MaxBytes = DefaultBytes
	}
	if config.MaxEntries == 0 {
		config.MaxEntries = DefaultEntries
	}
	if config.MaxRecordBytes == 0 {
		config.MaxRecordBytes = DefaultRecordBytes
	}
	if config.Dir == "" || config.MaxBytes < 1 || config.MaxEntries < 1 || config.MaxRecordBytes < 1 || config.MaxRecordBytes > config.MaxBytes {
		return nil, fmt.Errorf("%w: invalid configuration", ErrUnavailable)
	}
	absolute, err := filepath.Abs(config.Dir)
	if err != nil {
		return nil, err
	}
	if absolute == string(filepath.Separator) {
		return nil, fmt.Errorf("%w: dedicated directory required", ErrUnavailable)
	}
	if err = os.MkdirAll(absolute, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: unsafe directory", ErrUnavailable)
	}
	config.Dir = absolute
	// 已有非空目录必须有本队列格式标记，避免配置误指向其他应用目录后改变其中数据。
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return nil, err
	}
	if len(entries) > 0 {
		markerPath := filepath.Join(absolute, ".spool-format")
		markerInfo, infoErr := os.Lstat(markerPath)
		if infoErr != nil || !markerInfo.Mode().IsRegular() || markerInfo.Size() > 64 {
			return nil, fmt.Errorf("%w: invalid format marker", ErrUnavailable)
		}
		marker, err := os.ReadFile(markerPath)
		if err != nil || string(marker) != "xhub-usage-spool-v1\n" {
			return nil, fmt.Errorf("%w: directory is not an initialized usage spool", ErrUnavailable)
		}
	}
	if err = os.Chmod(absolute, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, err
	}
	if info, err = root.Lstat(".lock"); err == nil && !info.Mode().IsRegular() {
		root.Close()
		return nil, fmt.Errorf("%w: unsafe lock", ErrUnavailable)
	} else if err != nil && !os.IsNotExist(err) {
		root.Close()
		return nil, err
	}
	lock := flock.New(filepath.Join(absolute, ".lock"))
	locked, err := lock.TryLock()
	if err != nil || !locked {
		lock.Close()
		root.Close()
		return nil, fmt.Errorf("%w: directory already in use", ErrUnavailable)
	}
	s := &Spool{root: root, lock: lock, config: config, reserved: map[string]bool{}, activeDelivery: map[string]bool{}, retry: map[string]time.Time{}}
	if len(entries) == 0 {
		marker, createErr := root.OpenFile(".spool-format", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if createErr != nil {
			s.Close()
			return nil, createErr
		}
		_, writeErr := marker.Write([]byte("xhub-usage-spool-v1\n"))
		err = errors.Join(writeErr, marker.Sync(), marker.Close(), s.syncDirectoryLocked("."))
		if err != nil {
			s.Close()
			return nil, err
		}
	}
	if err = root.Mkdir("quarantine", 0700); err != nil && !os.IsExist(err) {
		s.Close()
		return nil, err
	}
	if info, err = root.Lstat("quarantine"); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		s.Close()
		return nil, fmt.Errorf("%w: unsafe quarantine", ErrUnavailable)
	}
	if err = root.Chmod("quarantine", 0700); err != nil {
		s.Close()
		return nil, err
	}
	// 在第一次准入前恢复临时文件/隔离损坏记录；坏证据保留且阻止新付费调用，其他有效记录仍允许后台恢复。
	s.mu.Lock()
	err = s.scanLocked(true)
	s.mu.Unlock()
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// Close 释放目录与进程锁，供进程退出和隔离重启测试调用；重复调用安全，不删除任何快照。
func (s *Spool) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return errors.Join(s.root.Close(), s.lock.Close())
}

// basename 用 RequestID 的 SHA256 生成固定安全文件名；参数不会成为目录路径，返回64位十六进制名。
func basename(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:]) + ".json"
}

// token 生成临时文件随机后缀；失败返回错误，不能复用固定名称覆盖未确认快照。
func token() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(random[:]), nil
}

// syncDirectoryLocked 同步目录项变更，参数为根目录内相对名；持mu调用，返回打开/fsync/关闭错误。
func (s *Spool) syncDirectoryLocked(name string) error {
	dir, err := s.root.Open(name)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

// encode 验证并序列化完整快照，保留 reservation ID、原时间和冻结价格；返回带校验摘要的不可变记录。
func encode(record iam.UsageRecord) ([]byte, error) {
	if record.RequestID == "" || record.TS.IsZero() || record.Cost < 0 || math.IsNaN(record.Cost) || math.IsInf(record.Cost, 0) || record.PromptTokens < 0 || record.CompletionTokens < 0 || (record.CachedTokens != nil && *record.CachedTokens < 0) {
		return nil, fmt.Errorf("invalid usage snapshot")
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(payload)
	return json.Marshal(envelope{Version: 1, Checksum: hex.EncodeToString(sum[:]), Payload: payload})
}

// decode 校验版本、摘要、请求ID与文件名及数值；参数只来自私有目录，错误不删除证据。
func decode(raw []byte, name string) (iam.UsageRecord, error) {
	var record iam.UsageRecord
	var value envelope
	if json.Unmarshal(raw, &value) != nil || value.Version != 1 {
		return record, fmt.Errorf("unsupported or invalid usage envelope")
	}
	sum := sha256.Sum256(value.Payload)
	if hex.EncodeToString(sum[:]) != value.Checksum {
		return record, fmt.Errorf("usage checksum mismatch")
	}
	if err := json.Unmarshal(value.Payload, &record); err != nil {
		return record, err
	}
	if _, err := encode(record); err != nil {
		return record, err
	}
	if name != "" && basename(record.RequestID) != name {
		return record, fmt.Errorf("usage identity mismatch")
	}
	return record, nil
}

// readLocked 限长读取普通0600文件，返回完整字节；拒绝符号链接、特殊文件及超限载荷，不跟随用户路径。
func (s *Spool) readLocked(name string) ([]byte, error) {
	info, err := s.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > s.config.MaxRecordBytes {
		return nil, fmt.Errorf("unsafe or oversized usage file")
	}
	file, err := s.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, s.config.MaxRecordBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > s.config.MaxRecordBytes {
		return nil, ErrFull
	}
	return raw, nil
}

// quarantineLocked 原子移走损坏/冲突证据并同步两个目录，返回文件系统错误；不读取符号链接目标或覆盖旧证据。
func (s *Spool) quarantineLocked(name string) error {
	suffix, err := token()
	if err != nil {
		return err
	}
	if err = s.root.Rename(name, "quarantine/"+suffix+".evidence"); err != nil {
		return err
	}
	return errors.Join(s.syncDirectoryLocked("."), s.syncDirectoryLocked("quarantine"))
}

// scanLocked 检查目录中的最终/临时记录；recover 为真时将完整临时文件发布，其余损坏证据隔离。
// Open及状态检查调用，持mu执行；未知普通文件也隔离，防止忽略可能的账单，不清理证据。
func (s *Spool) scanLocked(recover bool) error {
	dir, err := s.root.Open(".")
	if err != nil {
		return err
	}
	entries, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == ".lock" || name == ".spool-format" || name == "quarantine" {
			continue
		}
		temporary := strings.HasPrefix(name, ".pending-") && strings.HasSuffix(name, ".tmp")
		if strings.HasPrefix(name, ".probe-") {
			if err = s.root.Remove(name); err != nil {
				return err
			}
			if err = s.syncDirectoryLocked("."); err != nil {
				return err
			}
			continue
		}
		if !strings.HasSuffix(name, ".json") && !temporary {
			return fmt.Errorf("unexpected file in usage spool")
		}
		raw, readErr := s.readLocked(name)
		expected := name
		if temporary {
			expected = ""
		}
		record, decodeErr := decode(raw, expected)
		if readErr != nil || decodeErr != nil {
			if err = s.quarantineLocked(name); err != nil {
				return err
			}
			continue
		}
		if temporary && recover {
			target := basename(record.RequestID)
			if old, oldErr := s.readLocked(target); oldErr == nil {
				previous, previousErr := decode(old, target)
				if previousErr == nil && previous.DeliveryUnconfirmed && previous.DeliverySpoolOnly && record.DeliveryPending && record.DeliverySpoolOnly && !record.DeliveryUnconfirmed && (record.Cost == 0 || record.Status == "success" && record.HTTPStatus < 400) && sameDeliveryOwner(previous, record) {
					// 完整终态临时文件在客户端写出后 fsync；崩溃发生在 rename 前时可安全发布。
					if err = s.root.Rename(name, target); err != nil {
						return err
					}
				} else if string(old) != string(raw) {
					if err = s.quarantineLocked(name); err != nil {
						return err
					}
				} else {
					if err = s.root.Remove(name); err != nil {
						return err
					}
				}
			} else if os.IsNotExist(oldErr) {
				if err = s.root.Rename(name, target); err != nil {
					return err
				}
			} else {
				return oldErr
			}
			if err = s.syncDirectoryLocked("."); err != nil {
				return err
			}
		}
	}
	return nil
}

// sameDeliveryOwner 校验磁盘标记与终态属于同一请求和计费范围；不比较费用或用户正文。
// 参数 a/b 为校验过的快照，返回是否可原子替换；扫描恢复与正常终态写入共用。
func sameDeliveryOwner(a, b iam.UsageRecord) bool {
	return a.RequestID == b.RequestID && a.KeyID == b.KeyID && a.OwnerType == b.OwnerType && a.UserID == b.UserID && a.TeamID == b.TeamID && a.ProjectID == b.ProjectID && a.OrganizationID == b.OrganizationID && a.Model == b.Model && (a.CallType == "" || a.CallType == b.CallType) && a.BudgetReservationID == b.BudgetReservationID
}

// statusLocked 汇总容量和隔离状态；持mu调用，读取失败返回错误，不读取正文或对外暴露身份。
func (s *Spool) statusLocked() (Status, error) {
	result := Status{Reserved: len(s.reserved), ReservedBytes: int64(len(s.reserved)) * s.config.MaxRecordBytes}
	dir, err := s.root.Open(".")
	if err != nil {
		return result, err
	}
	entries, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		if entry.Name() == ".lock" || entry.Name() == ".spool-format" || entry.Name() == "quarantine" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return result, err
		}
		result.Pending++
		result.Bytes += info.Size()
	}
	dir, err = s.root.Open("quarantine")
	if err != nil {
		return result, err
	}
	quarantine, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return result, err
	}
	result.Quarantined = len(quarantine)
	for _, entry := range quarantine {
		info, err := entry.Info()
		if err != nil {
			return result, err
		}
		result.Bytes += info.Size()
	}
	result.Healthy = !s.closed && s.lastErr == nil && result.Quarantined == 0
	return result, nil
}

// Status 返回本机队列健康/容量快照，供准入和诊断测试使用；无调用方请求正文，关闭或读取失败返回错误。
func (s *Spool) Status() (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Status{}, ErrUnavailable
	}
	return s.statusLocked()
}

// Reserve 在付费外发前保守预留一个最大记录槽并验证真实写入/fsync，重复同callID不多占槽。
// 并发在途与待结算共同受字节/条数限制；磁盘满、权限错误、隔离证据时拒绝，调用结束必须Release。
// 预留限制本队列并发容量，不承诺抵抗外部进程在准入后耗尽磁盘或硬件故障。
func (s *Spool) Reserve(callID string) error {
	if callID == "" {
		return ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrUnavailable
	}
	if s.reserved[callID] {
		return nil
	}
	status, err := s.statusLocked()
	if err != nil {
		return err
	}
	if status.Quarantined > 0 {
		return ErrCorrupt
	}
	if status.Pending+status.Reserved >= s.config.MaxEntries || status.Bytes+status.ReservedBytes+s.config.MaxRecordBytes > s.config.MaxBytes {
		return ErrFull
	}
	info, err := s.root.Stat(".")
	if err != nil || info.Mode().Perm() != 0700 {
		return ErrUnavailable
	}
	suffix, err := token()
	if err != nil {
		return err
	}
	name := ".probe-" + suffix
	file, err := s.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		s.lastErr = err
		return err
	}
	_, writeErr := file.Write([]byte("usage-spool-probe\n"))
	syncErr := file.Sync()
	closeErr := file.Close()
	removeErr := s.root.Remove(name)
	dirErr := s.syncDirectoryLocked(".")
	if err = errors.Join(writeErr, syncErr, closeErr, removeErr, dirErr); err != nil {
		s.lastErr = err
		return err
	}
	s.lastErr = nil
	s.reserved[callID] = true
	return nil
}

// Release 归还尚未写入的在途容量；无返回值，重复或未知callID安全，不删除持久文件。
func (s *Spool) Release(callID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeDelivery[basename(callID)] {
		return
	}
	delete(s.reserved, callID)
}

// Put 先写完整临时记录并fsync，再原子改名及dirfsync；返回成功仅表示快照可由新进程恢复，不表示账单已入库。
// 相同RequestID/内容幂等；冲突保留两份证据并返回错误；失败不截断正文，准入后的磁盘故障交由调用方尝试其他持久介质。
func (s *Spool) Put(record iam.UsageRecord) error {
	raw, err := encode(record)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.putLocked(record, raw)
}

// putLocked 写入已编码快照；调用方持有队列锁，返回完整持久化、容量或快照冲突错误。
// 正常账单和同步响应的未确认标记共用容量检查，不能覆盖不同内容。
func (s *Spool) putLocked(record iam.UsageRecord, raw []byte) error {
	var err error
	defer delete(s.reserved, record.RequestID)
	if s.closed {
		return ErrUnavailable
	}
	if int64(len(raw)) > s.config.MaxRecordBytes {
		s.lastErr = ErrFull
		return ErrFull
	}
	name := basename(record.RequestID)
	if old, readErr := s.readLocked(name); readErr == nil {
		if string(old) == string(raw) {
			return nil
		}
		status, statusErr := s.statusLocked()
		if statusErr != nil {
			return statusErr
		}
		if status.Bytes+int64(len(raw)) > s.config.MaxBytes {
			return ErrFull
		}
		sum := sha256.Sum256(raw)
		conflictName := "quarantine/conflict-" + hex.EncodeToString(sum[:]) + ".evidence"
		if _, existsErr := s.root.Lstat(conflictName); os.IsNotExist(existsErr) {
			if err = s.writeLocked(raw, conflictName); err != nil {
				return err
			}
		} else if existsErr != nil {
			return existsErr
		}
		s.lastErr = ErrConflict
		return ErrConflict
	} else if !os.IsNotExist(readErr) {
		s.lastErr = readErr
		return readErr
	}
	status, err := s.statusLocked()
	if err != nil {
		return err
	}
	reserved := status.ReservedBytes
	if s.reserved[record.RequestID] {
		reserved -= s.config.MaxRecordBytes
	}
	if status.Pending+status.Reserved-boolInt(s.reserved[record.RequestID]) >= s.config.MaxEntries || status.Bytes+reserved+int64(len(raw)) > s.config.MaxBytes {
		s.lastErr = ErrFull
		return ErrFull
	}
	return s.writeLocked(raw, name)
}

// BeginDelivery 持久化零费用未确认标记并将其标记为当前实例在途；参数只包含脱敏账单元数据。
// 返回错误时不允许把标记视为持久化；在途标记不由后台重放，进程退出后才允许作为未知结果恢复。
func (s *Spool) BeginDelivery(record iam.UsageRecord) error {
	if !record.DeliveryPending || !record.DeliverySpoolOnly || !record.DeliveryUnconfirmed || record.Cost != 0 || record.Status != "error" || record.RequestBody != "" || record.ResponseBody != "" || record.ProxyRequest != "" {
		return fmt.Errorf("invalid delivery marker")
	}
	raw, err := encode(record)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	name := basename(record.RequestID)
	if s.activeDelivery[name] {
		return ErrConflict
	}
	reserved := s.reserved[record.RequestID]
	if err := s.putLocked(record, raw); err != nil {
		return err
	}
	if reserved {
		s.reserved[record.RequestID] = true
	}
	s.activeDelivery[name] = true
	return nil
}

// CompleteDelivery 原子替换当前实例持有的未确认标记为写出终态；参数为同一次调用的完整最终快照。
// 写入或目录同步失败时保留未知结果证据并阻止本实例重放；成功后后台可恢复最终账单。
func (s *Spool) CompleteDelivery(record iam.UsageRecord) error {
	if !record.DeliveryPending || !record.DeliverySpoolOnly || record.DeliveryUnconfirmed {
		return fmt.Errorf("invalid delivery final snapshot")
	}
	if record.Cost > 0 && (record.Status != "success" || record.HTTPStatus >= 400) {
		return fmt.Errorf("invalid failed delivery charge")
	}
	raw, err := encode(record)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	name := basename(record.RequestID)
	if s.closed || !s.activeDelivery[name] {
		return ErrUnavailable
	}
	oldRaw, err := s.readLocked(name)
	if err != nil {
		return err
	}
	old, err := decode(oldRaw, name)
	if err != nil {
		return err
	}
	if !old.DeliveryUnconfirmed || !sameDeliveryOwner(old, record) {
		return ErrConflict
	}
	status, err := s.statusLocked()
	if err != nil {
		return err
	}
	reservedBytes := status.ReservedBytes
	if s.reserved[record.RequestID] {
		reservedBytes -= s.config.MaxRecordBytes
	}
	if int64(len(raw)) > s.config.MaxRecordBytes || status.Bytes-int64(len(oldRaw))+int64(len(raw))+reservedBytes > s.config.MaxBytes {
		return ErrFull
	}
	if err := s.writeLocked(raw, name); err != nil {
		return err
	}
	delete(s.activeDelivery, name)
	delete(s.reserved, record.RequestID)
	return nil
}

// DiscardUnconfirmedDelivery 清理已由数据库严格结算的磁盘标记或相同终态。
// record 为数据库已确认的完整终态；仅归属一致的未确认标记或逐字相同的终态可删除。
// 调用方必须先完成严格结算；删除或目录同步失败返回错误，保留后续恢复机会。
func (s *Spool) DiscardUnconfirmedDelivery(record iam.UsageRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrUnavailable
	}
	name := basename(record.RequestID)
	raw, err := s.readLocked(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	marker, err := decode(raw, name)
	if err != nil {
		return err
	}
	if !sameDeliveryOwner(marker, record) {
		return ErrConflict
	}
	if !marker.DeliveryUnconfirmed {
		expected, err := encode(record)
		if err != nil {
			return err
		}
		if !bytes.Equal(raw, expected) {
			return ErrConflict
		}
	}
	if err := s.root.Remove(name); err != nil {
		return err
	}
	delete(s.activeDelivery, name)
	delete(s.reserved, record.RequestID)
	return s.syncDirectoryLocked(".")
}

// writeLocked 将完整字节原子发布到根内指定名字；持mu调用，任何同步失败保留临时或已发布文件供恢复。
func (s *Spool) writeLocked(raw []byte, name string) error {
	suffix, err := token()
	if err != nil {
		return err
	}
	temporary := ".pending-" + suffix + ".tmp"
	file, err := s.root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		s.lastErr = err
		return err
	}
	_, writeErr := file.Write(raw)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		s.lastErr = err
		return err
	}
	if err = s.root.Rename(temporary, name); err != nil {
		s.lastErr = err
		return err
	}
	if err = s.syncDirectoryLocked("."); err != nil {
		s.lastErr = err
		return err
	}
	if filepath.Dir(name) != "." {
		if err = s.syncDirectoryLocked(filepath.Dir(name)); err != nil {
			s.lastErr = err
			return err
		}
	}
	s.lastErr = nil
	return nil
}

// boolInt 将预留存在性转换成容量计数；只用于本包数值计算，无副作用。
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// acknowledgeLocked 仅删除与已提交完整内容一致的快照，并同步目录；错误保留/重放由数据库request_id保证幂等。
func (s *Spool) acknowledgeLocked(name string, raw []byte) error {
	current, err := s.readLocked(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if string(current) != string(raw) {
		return ErrConflict
	}
	if err = s.root.Remove(name); err != nil {
		return err
	}
	delete(s.retry, name)
	return s.syncDirectoryLocked(".")
}

// Acknowledge 在调用方确认账单事务成功后删除同一快照；record必须是Put时完整值，不可仅凭RequestID删除新内容。
func (s *Spool) Acknowledge(record iam.UsageRecord) error {
	raw, err := encode(record)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrUnavailable
	}
	return s.acknowledgeLocked(basename(record.RequestID), raw)
}

// Replay 至多提交limit条快照；commit只负责本地数据库结算，成功才确认，失败保留并至少30秒后再试。
// 参数ctx取消即停止，返回本轮成功数和合并错误；单条冲突不阻塞其他记录，不包含正文的错误可供运维观察。
func (s *Spool) Replay(ctx context.Context, limit int, commit func(context.Context, iam.UsageRecord) error) (int, error) {
	if limit < 1 || commit == nil {
		return 0, ErrUnavailable
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, ErrUnavailable
	}
	if err := s.scanLocked(true); err != nil {
		s.mu.Unlock()
		return 0, err
	}
	dir, err := s.root.Open(".")
	if err != nil {
		s.mu.Unlock()
		return 0, err
	}
	entries, err := dir.ReadDir(-1)
	dir.Close()
	s.mu.Unlock()
	if err != nil {
		return 0, err
	}
	count, attempted := 0, 0
	var failures []error
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if attempted >= limit {
			break
		}
		if err := ctx.Err(); err != nil {
			return count, errors.Join(append(failures, err)...)
		}
		name := entry.Name()
		s.mu.Lock()
		if s.activeDelivery[name] {
			s.mu.Unlock()
			continue
		}
		if time.Now().Before(s.retry[name]) {
			s.mu.Unlock()
			continue
		}
		raw, err := s.readLocked(name)
		s.mu.Unlock()
		if err != nil {
			failures = append(failures, err)
			continue
		}
		record, err := decode(raw, name)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		attempted++
		err = commit(ctx, record)
		s.mu.Lock()
		if err == nil {
			err = s.acknowledgeLocked(name, raw)
		}
		if err != nil {
			s.retry[name] = time.Now().Add(30 * time.Second)
			failures = append(failures, fmt.Errorf("usage spool record %s: %w", name, err))
		} else {
			count++
		}
		s.mu.Unlock()
	}
	return count, errors.Join(failures...)
}
