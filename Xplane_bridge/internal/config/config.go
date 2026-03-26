package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config 保存整个中间件运行时需要的关键地址配置
type Config struct {
	// 甲方 A 推任务 JSON 的 HTTP 监听地址，例如 ":32088"
	HTTPFromAAddr string `json:"httpFromAAddr"`

	// X-Plane Web API 的 http base，例如 "http://localhost:8086"
	XPlaneBaseURL string `json:"xplaneBaseUrl"`

	// 向甲方 B 推 27 个参数的 UDP 地址，例如 "192.168.1.159:6666"
	TelemetryUDP string `json:"telemetryUdpTarget"`

	// 甲方 B 的 HTTP 基地址，例如 "http://192.168.1.159:17040"
	BBaseURL string `json:"bBaseUrl"`

	// ✅ 新增：甲方 A 的 HTTP 基地址，用于上传 CSV
	// 例如 "http://192.168.1.159:17030"
	AUploadBaseURL string `json:"aUploadBaseUrl"`

	// ✅ 新增：调试开关（true 时打印 command 调用的 HTTP 状态码与响应体）
	Debug bool `json:"debug"`

	// 眼动 CSV 文件所在目录（文件名固定为 openxr_eye_gaze.csv）
	EyeGazeCSVDir string `json:"eyeGazeCsvDir"`
}

// Load 从指定路径加载配置文件（JSON）
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开配置文件失败 %s: %w", path, err)
	}
	defer f.Close()

	var cfg Config
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 简单校验一下，防止全空
	if cfg.HTTPFromAAddr == "" {
		return nil, fmt.Errorf("配置 httpFromAAddr 不能为空")
	}
	if cfg.XPlaneBaseURL == "" {
		return nil, fmt.Errorf("配置 xplaneBaseUrl 不能为空")
	}
	if cfg.TelemetryUDP == "" {
		return nil, fmt.Errorf("配置 telemetryUdpTarget 不能为空")
	}
	if cfg.BBaseURL == "" {
		return nil, fmt.Errorf("配置 bBaseUrl 不能为空")
	}
	if cfg.AUploadBaseURL == "" {
		return nil, fmt.Errorf("配置 aUploadBaseUrl 不能为空")
	}
	if cfg.EyeGazeCSVDir == "" {
		return nil, fmt.Errorf("配置 eyeGazeCsvDir 不能为空")
	}

	return &cfg, nil
}
