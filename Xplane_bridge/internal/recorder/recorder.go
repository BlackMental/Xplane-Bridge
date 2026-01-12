package recorder

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

// 一帧采样的数据（和 xplane.TelemetrySample 结构类似）
// 我们单独在 recorder 里定义一份，避免直接依赖 xplane 包。
type Sample struct {
	Timestamp time.Time
	Values    map[string]float64 // key: "lat", "lon", "alt_m" 等
}

// Recorder 负责：
// - 接收样本（Append）
// - 在 Start/Stop 控制下决定是否记录
// - 导出为 CSV
type Recorder struct {
	mu      sync.Mutex
	enabled bool
	fields  []string // 要写进 CSV 的字段顺序（不含 timestamp）
	samples []Sample
}

// 创建一个新的 Recorder
// fields: 想在 CSV 里输出哪些字段，以及顺序，比如：["lat", "lon", "alt_m", "pitch", ...]
func NewRecorder(fields []string) *Recorder {
	return &Recorder{
		fields: fields,
	}
}

// 开始记录：清空已有数据，并打开记录开关
func (r *Recorder) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.enabled = true
	r.samples = nil
}

// 停止记录：只是关闭开关，不会清空已有数据
func (r *Recorder) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.enabled = false
}

// 追加一帧样本
func (r *Recorder) Append(sample Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.enabled {
		return
	}
	// 这里简单起见，不拷贝 map，直接引用。
	// 对于我们的场景足够安全。
	r.samples = append(r.samples, sample)
}

// 导出为 CSV 文件
// path: 文件路径，比如 "telemetry.csv"
func (r *Recorder) ExportCSV(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.samples) == 0 {
		return fmt.Errorf("没有可导出的数据")
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("创建文件失败: %w", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	// 写表头：timestamp + 各字段名
	header := make([]string, 0, len(r.fields)+1)
	header = append(header, "timestamp")
	header = append(header, r.fields...)
	if err := w.Write(header); err != nil {
		return fmt.Errorf("写表头失败: %w", err)
	}

	// 写每一行数据
	for _, s := range r.samples {
		row := make([]string, 0, len(r.fields)+1)
		// 时间戳格式你可以改，这里用 "2006-01-02 15:04:05.000"
		row = append(row, s.Timestamp.Format("2006-01-02 15:04:05.000"))

		for _, key := range r.fields {
			v, ok := s.Values[key]
			if !ok {
				row = append(row, "") // 没有这个字段就留空
				continue
			}
			row = append(row, strconv.FormatFloat(v, 'f', 6, 64))
		}

		if err := w.Write(row); err != nil {
			return fmt.Errorf("写数据行失败: %w", err)
		}
	}

	return nil
}
