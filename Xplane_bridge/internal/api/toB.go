package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// 甲方B的接口地址（由 InitBEndpoints 在程序启动时注入）
var (
	bSessionSwitchURL   string
	bSessionCompleteURL string
)

// InitBEndpoints 使用甲方 B 的 baseURL 初始化两个具体接口地址。
// 例如 baseURL = "http://192.168.1.159:17040"
func InitBEndpoints(baseURL string) {
	base := strings.TrimRight(baseURL, "/")
	// 保持原有路径不变
	bSessionSwitchURL = base + "/api/session/switch"
	bSessionCompleteURL = base + "/api/Session/complete"
}

// SendSessionSwitchToB 把 SessionSwitchRequest 发给甲方B
// 请求体是一个扁平的 JSON：
//
//	{
//	  "taskId": ...,
//	  "userNumber": "...",
//	  "taskActionId": "...",
//	  "maneuverProfileJson": "...",
//	  "trainMode": ...,
//	  "scenarioJson": "...",
//	  "rulesJson": "..."
//	}
func SendSessionSwitchToB(req SessionSwitchRequest) error {
	if bSessionSwitchURL == "" {
		return fmt.Errorf("bSessionSwitchURL 尚未初始化，请先调用 InitBEndpoints")
	}

	// 直接序列化为 JSON
	buf, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("序列化 SessionSwitchRequest 失败: %w", err)
	}

	fmt.Printf("📤 正在发送 SessionSwitchRequest 给甲方B: taskId=%d, taskActionId=%s\n",
		req.TaskID, req.TaskActionID)
	//fmt.Println("📦 发送内容如下（JSON）：")
	//fmt.Println(string(buf))

	httpReq, err := http.NewRequest("POST", bSessionSwitchURL, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("构造 HTTP 请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("请求甲方B失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("甲方B返回错误: status=%s, body=%s", resp.Status, string(body))
	}

	fmt.Println("✅ 已成功发送 SessionSwitchRequest 给甲方B。")
	return nil
}

// SendSessionSwitchForAction：按动作下标（0-based）发送 SessionSwitch
// 方便其他地方复用，比如 CLI 里选择第 N 个动作时调用。
func SendSessionSwitchForAction(actionIndex int) error {
	if CurrentTask == nil {
		return fmt.Errorf("CurrentTask 为空：尚未收到甲方A的任务，无法切换动作")
	}

	if actionIndex < 0 || actionIndex >= len(CurrentTask.TrainTaskActions) {
		return fmt.Errorf("actionIndex 越界：%d（共有 %d 个动作）",
			actionIndex, len(CurrentTask.TrainTaskActions))
	}

	action := CurrentTask.TrainTaskActions[actionIndex]
	req := BuildSessionSwitchRequest(CurrentTask, action)
	return SendSessionSwitchToB(req)
}

// ===================== Session Complete =====================
//
// 甲方 B 的 SessionComplete 接口只接受：
//   { "reason": "string" }
// 严格来说可以为空；我们约定统一发：{ "reason": "stop" }.

// SendSessionCompleteToB 把 SessionCompleteRequest 发给甲方B，表示当前 Session 已完成
func SendSessionCompleteToB(req SessionCompleteRequest) error {
	if bSessionCompleteURL == "" {
		return fmt.Errorf("bSessionCompleteURL 尚未初始化，请先调用 InitBEndpoints")
	}

	buf, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("序列化 SessionCompleteRequest 失败: %w", err)
	}

	fmt.Println("📤 正在发送 SessionCompleteRequest 给甲方B")
	//fmt.Println("📦 完成通知内容如下（JSON）：")
	//fmt.Println(string(buf))

	httpReq, err := http.NewRequest("POST", bSessionCompleteURL, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("构造 HTTP 请求失败（Complete）: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("请求甲方B失败（Complete）: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("甲方B返回错误（Complete）: status=%s, body=%s", resp.Status, string(body))
	}

	fmt.Println("✅ 已成功发送 SessionCompleteRequest 给甲方B。")
	return nil
}

// SendSessionComplete：对外提供的“结束当前 Session”封装
// 一般在用户按 e / q 时调用。
func SendSessionComplete() error {
	req := BuildSessionCompleteRequest()
	return SendSessionCompleteToB(req)
}
