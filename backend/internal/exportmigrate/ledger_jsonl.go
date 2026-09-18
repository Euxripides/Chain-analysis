package exportmigrate

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// JSONLLedger 是文件账本（每行一条完整行快照，后写覆盖先写）。
//
// 用途：隔离负向校准（在没有生产 PG 的前提下证明冲突/缺 sha/非 404/中断续跑/重复运行的
// 行为），以及 dry-run 计划落盘。**生产迁移使用 PGLedger**（0058 表）。
type JSONLLedger struct {
	Path string

	mu   sync.Mutex
	rows map[string]Row // key = exportID + "\x00" + toBucket
}

func (l *JSONLLedger) load() error {
	if l.rows != nil {
		return nil
	}
	l.rows = map[string]Row{}
	f, err := os.Open(l.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var row Row
		if err := json.Unmarshal(line, &row); err != nil {
			return fmt.Errorf("corrupt ledger line: %w", err)
		}
		l.rows[row.ExportID+"\x00"+row.ToBucket] = row
	}
	return sc.Err()
}

func (l *JSONLLedger) Get(ctx context.Context, exportID, toBucket string) (*Row, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.load(); err != nil {
		return nil, err
	}
	if row, ok := l.rows[exportID+"\x00"+toBucket]; ok {
		copied := row
		return &copied, nil
	}
	return nil, nil
}

func (l *JSONLLedger) Upsert(ctx context.Context, row Row) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.load(); err != nil {
		return err
	}
	if prev, ok := l.rows[row.ExportID+"\x00"+row.ToBucket]; ok {
		row.Attempts = prev.Attempts + 1
	} else {
		row.Attempts = 1
	}
	payload, err := json.Marshal(row)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(l.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(payload, '\n')); err != nil {
		return err
	}
	l.rows[row.ExportID+"\x00"+row.ToBucket] = row
	return f.Sync()
}

func (l *JSONLLedger) List(ctx context.Context, toBucket string) ([]Row, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.load(); err != nil {
		return nil, err
	}
	var out []Row
	for _, row := range l.rows {
		if row.ToBucket == toBucket {
			out = append(out, row)
		}
	}
	return out, nil
}
