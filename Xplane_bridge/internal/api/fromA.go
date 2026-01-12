package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// 当收到甲方 A 的任务时触发的回调（由 main.go 注册）。
var OnTaskReceived func(task *TrainTaskRecordDetail) error

// 当收到甲方 A 的中止指令时触发的回调（由 main.go 注册）。
var OnStopReceived func() error

// 供 main.go 调用，注册任务回调。
func RegisterTaskHook(fn func(task *TrainTaskRecordDetail) error) {
	OnTaskReceived = fn
}

// 供 main.go 调用，注册中止回调。
func RegisterStopHook(fn func() error) {
	OnStopReceived = fn
}

// 甲方 A 上传训练数据的 HTTP 基地址（由 main.go 注入）
var aUploadBaseURL string

// 供 main.go 调用，初始化甲方 A 上传地址
func InitAUploadEndpoint(base string) {
	// 顺手把结尾的 / 去掉，避免重复 //
	aUploadBaseURL = strings.TrimRight(base, "/")
}

// 当前任务挂在这里，给其他地方用
var CurrentTask *TrainTaskRecordDetail

// UploadTrainDataCSV 在任务结束时，把 telemetry.csv 上传给甲方 A。
// 会使用 CurrentTask 中的 taskId 和 userNumber 作为 query 参数。
func UploadTrainDataCSV(csvPath string) error {
	if CurrentTask == nil {
		return fmt.Errorf("CurrentTask 为空，无法上传训练数据")
	}
	if aUploadBaseURL == "" {
		return fmt.Errorf("甲方A上传地址未初始化（aUploadBaseUrl 为空）")
	}

	taskID := CurrentTask.TaskID
	userNumber := CurrentTask.UserNumber

	// 打开 CSV 文件
	f, err := os.Open(csvPath)
	if err != nil {
		return fmt.Errorf("打开 CSV 文件失败: %w", err)
	}
	defer f.Close()

	// 目标 URL：
	// POST {base}/flightSimulationTraining/uploadTrainData?taskId=...&userNumber=...
	url := fmt.Sprintf("%s/flightSimulationTraining/uploadTrainData?taskId=%d&userNumber=%s",
		aUploadBaseURL, taskID, userNumber)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// 创建 form-data 里的 file 字段，字段名叫 "file"（跟 swagger 保持一致）
	part, err := writer.CreateFormFile("file", filepath.Base(csvPath))
	if err != nil {
		return fmt.Errorf("创建表单文件字段失败: %w", err)
	}

	if _, err := io.Copy(part, f); err != nil {
		return fmt.Errorf("写入表单文件内容失败: %w", err)
	}

	// 结束 multipart 写入
	if err := writer.Close(); err != nil {
		return fmt.Errorf("关闭 multipart writer 失败: %w", err)
	}

	// 构造 HTTP 请求
	req, err := http.NewRequest("POST", url, &buf)
	if err != nil {
		return fmt.Errorf("构造上传请求失败: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// 发送请求
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求甲方A上传接口失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("甲方A返回错误: status=%s, body=%s", resp.Status, string(body))
	}

	fmt.Println("✅ 已成功将训练数据 CSV 上传给甲方A。")
	return nil
}

// ReceiveTaskHandler 接收甲方A任务 JSON
func ReceiveTaskHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	fmt.Println("==== 收到甲方 A 的任务 JSON ====")
	//fmt.Println(string(body))
	fmt.Println("================================")

	// 解析成我们在 types.go 定义的结构
	var task TrainTaskRecordDetail
	if err := json.Unmarshal(body, &task); err != nil {
		fmt.Printf("❌ 解析任务 JSON 失败: %v\n", err)
		http.Error(w, "invalid task json", http.StatusBadRequest)
		return
	}

	// 简单打印一些关键字段，确认解析没问题
	fmt.Printf("✅ 解析成功: taskId=%d, user=%s, airport=%s\n",
		task.TaskID, task.UserNumber, task.AirportNumber)
	fmt.Printf("   runway=%s, actions=%d 个\n",
		task.RunwayXPlane.RunwayNumber, len(task.TrainTaskActions))

	// 挂到全局
	CurrentTask = &task

	if OnTaskReceived != nil {
		if err := OnTaskReceived(CurrentTask); err != nil {
			fmt.Printf("❌ 处理任务回调失败: %v\n", err)
		}
	}

	// 回 ACK 给甲方A
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"code": 0,
		"msg":  "task received",
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// ReceiveStopHandler 接收甲方A的中止指令
func ReceiveStopHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if len(body) > 0 {
		fmt.Printf("==== 收到甲方 A 的中止指令 ====\n%s\n================================\n", string(body))
	} else {
		fmt.Println("==== 收到甲方 A 的中止指令 ====")
		fmt.Println("================================")
	}

	var payload struct {
		Type int `json:"type"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "invalid stop json", http.StatusBadRequest)
			return
		}
	}

	if payload.Type != 4 {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"code": 0,
			"msg":  "stop ignored",
		}
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	if OnStopReceived != nil {
		if err := OnStopReceived(); err != nil {
			fmt.Printf("❌ 处理中止回调失败: %v\n", err)
		}
	}

	if err := UploadTrainDataCSV("telemetry.csv"); err != nil {
		fmt.Printf("❌ 上传训练数据到甲方A失败: %v\n", err)
		http.Error(w, "upload train data failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"code": 0,
		"msg":  "stop received",
	}
	_ = json.NewEncoder(w).Encode(resp)
}
