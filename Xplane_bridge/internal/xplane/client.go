package xplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// ====================== 对外暴露的类型 ======================

// 代表 X-Plane Web API 的能力信息
type Capabilities struct {
	API struct {
		Versions []string `json:"versions"`
	} `json:"api"`
	XPlane struct {
		Version string `json:"version"`
	} `json:"x-plane"`
}

// 我们对“要订阅的量”的抽象：一个字段 = 你定义的 key + 对应的 DataRef 名字
type TelemetryField struct {
	Key   string
	Name  string
	Index *int // nil 表示不是数组 / 不指定下标；非 nil = 订阅数组下标
}

// 每一帧采样的数据：时间戳 + 一组 key → 值
type TelemetrySample struct {
	Timestamp time.Time
	Values    map[string]float64
}

// X-Plane 客户端：封装所有 REST / WebSocket 细节
type Client struct {
	baseHTTP   string       // 例如 "http://localhost:8086"
	baseAPI    string       // baseHTTP + "/api"
	baseV2     string       // baseHTTP + "/api/v2"
	wsURL      string       // 例如 "ws://localhost:8086/api/v2"
	httpClient *http.Client // 复用 HTTP 客户端
}

// 新建一个 X-Plane 客户端，host 形如 "http://localhost:8086"
func NewClient(baseHTTP string) *Client {
	// 简单处理一下，避免用户写成最后带 / 的
	baseHTTP = strings.TrimRight(baseHTTP, "/")

	wsURL := baseHTTP
	// 把 http:// 换成 ws://，方便复用端口
	if strings.HasPrefix(wsURL, "http://") {
		wsURL = "ws://" + strings.TrimPrefix(wsURL, "http://")
	} else if strings.HasPrefix(wsURL, "https://") {
		wsURL = "wss://" + strings.TrimPrefix(wsURL, "https://")
	}
	wsURL = wsURL + "/api/v2"

	return &Client{
		baseHTTP: baseHTTP,
		baseAPI:  baseHTTP + "/api",
		baseV2:   baseHTTP + "/api/v2",
		wsURL:    wsURL,
		httpClient: &http.Client{
			Timeout: 3 * time.Second,
		},
	}
}

// ====================== 公共方法：REST 部分 ======================

// 获取 /api/capabilities
func (c *Client) GetCapabilities(ctx context.Context) (*Capabilities, error) {
	url := c.baseAPI + "/capabilities"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 X-Plane 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("X-Plane 返回错误状态码: %d", resp.StatusCode)
	}

	var cap Capabilities
	if err := json.NewDecoder(resp.Body).Decode(&cap); err != nil {
		return nil, fmt.Errorf("解析 JSON 失败: %w", err)
	}
	return &cap, nil
}

// 根据名字精确查 DataRef，返回它的 ID
func (c *Client) FindDatarefByName(ctx context.Context, name string) (int64, error) {
	url := fmt.Sprintf("%s/datarefs?filter[name]=%s&fields=id,name,value_type",
		c.baseV2, name)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("构建请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("X-Plane 返回错误状态码: %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			ValueType string `json:"value_type"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("解析 JSON 失败: %w", err)
	}

	if len(result.Data) == 0 {
		return 0, fmt.Errorf("没有找到 DataRef: %s", name)
	}

	return result.Data[0].ID, nil
}

// 写 DataRef 的通用方法（暂时留着，后面控制飞机要用）
func (c *Client) SetDatarefValue(ctx context.Context, id int64, value any) error {
	url := fmt.Sprintf("%s/datarefs/%d/value", c.baseV2, id)

	body := struct {
		Data any `json:"data"`
	}{Data: value}

	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("编码 JSON 失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("构建 PATCH 请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("发送 PATCH 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			ErrorCode    string `json:"error_code"`
			ErrorMessage string `json:"error_message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)

		if errResp.ErrorCode != "" {
			return fmt.Errorf("写 DataRef 失败: http %d, %s: %s",
				resp.StatusCode, errResp.ErrorCode, errResp.ErrorMessage)
		}
		return fmt.Errorf("写 DataRef 返回错误状态码: %d", resp.StatusCode)
	}
	return nil
}

// ====================== WebSocket / Telemetry 部分 ======================

// SubscribeTelemetry 订阅一组 DataRef，并返回一个连续的 TelemetrySample 流。
// 每个 sample 都包含“当前时刻所有字段”的值，而不是只包含本帧变化的字段。
func (c *Client) SubscribeTelemetry(ctx context.Context, fields []TelemetryField) (<-chan TelemetrySample, error) {
	// 1. name -> id
	type fieldInfo struct {
		Field TelemetryField
		ID    int64
	}

	infos := make([]fieldInfo, 0, len(fields))
	idToKey := make(map[int64]string, len(fields))
	keyToField := make(map[string]TelemetryField, len(fields))

	for _, f := range fields {
		id, err := c.FindDatarefByName(ctx, f.Name)
		if err != nil {
			return nil, fmt.Errorf("查找 DataRef %s 失败: %w", f.Name, err)
		}
		info := fieldInfo{Field: f, ID: id}
		infos = append(infos, info)
		idToKey[id] = f.Key
		keyToField[f.Key] = f
	}

	// 2. 建立 WebSocket 连接
	wsURL := c.wsURL

	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}

	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("连接 X-Plane WebSocket 失败: %w", err)
	}

	// 3. 发送订阅请求
	type subDataref struct {
		ID    int64 `json:"id"`
		Index *int  `json:"index,omitempty"`
	}

	req := struct {
		ReqID  int            `json:"req_id"`
		Type   string         `json:"type"`
		Params map[string]any `json:"params"`
	}{
		ReqID: 1,
		Type:  "dataref_subscribe_values",
		Params: map[string]any{
			"datarefs": []subDataref{},
		},
	}

	for _, info := range infos {
		sub := subDataref{ID: info.ID}
		if info.Field.Index != nil {
			sub.Index = info.Field.Index
		}
		req.Params["datarefs"] = append(req.Params["datarefs"].([]subDataref), sub)
	}

	if err := conn.WriteJSON(req); err != nil {
		conn.Close()
		return nil, fmt.Errorf("发送订阅请求失败: %w", err)
	}

	out := make(chan TelemetrySample, 16)

	go func() {
		defer close(out)
		defer conn.Close()

		// 👇 全局状态：保存“当前最新的值”
		state := make(map[string]float64)

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			_, msgBytes, err := conn.ReadMessage()
			if err != nil {
				fmt.Printf("读取 WebSocket 消息失败: %v\n", err)
				return
			}

			var msg struct {
				Type string                 `json:"type"`
				Data map[string]interface{} `json:"data"`
			}
			if err := json.Unmarshal(msgBytes, &msg); err != nil {
				fmt.Printf("解析 WebSocket JSON 失败: %v\n", err)
				continue
			}

			if msg.Type != "dataref_update_values" {
				continue
			}

			// 更新 state（增量）
			for idStr, raw := range msg.Data {
				id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
				if err != nil {
					continue
				}
				key, ok := idToKey[id]
				if !ok {
					continue
				}
				field := keyToField[key]

				switch v := raw.(type) {
				case float64:
					// 标量 DataRef
					state[key] = v

				case []any:
					// 数组 DataRef：根据 Index 取一个元素
					if field.Index != nil {
						idx := *field.Index
						if idx >= 0 && idx < len(v) {
							if num, ok := v[idx].(float64); ok {
								state[key] = num
							}
						}
					}
				default:
					// 其他类型暂时忽略
				}
			}

			if len(state) == 0 {
				continue
			}

			// 拷贝一个快照给外面
			snapshot := make(map[string]float64, len(state))
			for k, v := range state {
				snapshot[k] = v
			}

			sample := TelemetrySample{
				Timestamp: time.Now(),
				Values:    snapshot,
			}

			select {
			case out <- sample:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}
