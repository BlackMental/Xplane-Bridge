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

// OnTaskReceived 当收到甲方 A 的任务时触发的回调（由 main.go 注册）。
var OnTaskReceived func(task *TrainTaskRecordDetail) error

// OnStopReceived 当收到甲方 A 的中止指令时触发的回调（由 main.go 注册）。
// stopType: 0 暂停, 1 继续, 2 冻结, 3 重置, 4 退出
var OnStopReceived func(stopType int) error

// OnWeatherReceived 当收到甲方 A 的天气变更指令时触发回调。
var OnWeatherReceived func(weather string) error

// OnTimePeriodReceived 当收到甲方 A 的时间变更指令时触发回调。
var OnTimePeriodReceived func(timePeriod string) error

// OnVisibilityReceived 当收到甲方 A 的能见度变更指令时触发回调（单位 km）。
var OnVisibilityReceived func(visibilityKm float64) error

// OnWindSpeedReceived 当收到甲方 A 的风速变更指令时触发回调。
var OnWindSpeedReceived func(speed float64) error

// OnWindDirectionReceived 当收到甲方 A 的风向变更指令时触发回调。
var OnWindDirectionReceived func(direction float64) error

// OnFailureReceived 当收到甲方 A 的特情触发指令时触发回调。
var OnFailureReceived func(req FailureRequest) error

// OnInstructorActionReceived 当收到甲方 A 的数字教官指令时触发回调。
var OnInstructorActionReceived func(param int) error

// RegisterTaskHook 供 main.go 调用，注册任务回调。
func RegisterTaskHook(fn func(task *TrainTaskRecordDetail) error) {
	OnTaskReceived = fn
}

// RegisterStopHook 供 main.go 调用，注册中止回调。
func RegisterStopHook(fn func(stopType int) error) {
	OnStopReceived = fn
}

// RegisterWeatherHook 供 main.go 调用，注册天气变更回调。
func RegisterWeatherHook(fn func(weather string) error) {
	OnWeatherReceived = fn
}

// RegisterTimePeriodHook 供 main.go 调用，注册时间变更回调。
func RegisterTimePeriodHook(fn func(timePeriod string) error) {
	OnTimePeriodReceived = fn
}

// RegisterVisibilityHook 供 main.go 调用，注册能见度变更回调（单位 km）。
func RegisterVisibilityHook(fn func(visibilityKm float64) error) {
	OnVisibilityReceived = fn
}

// RegisterWindSpeedHook 供 main.go 调用，注册风速变更回调。
func RegisterWindSpeedHook(fn func(speed float64) error) {
	OnWindSpeedReceived = fn
}

// RegisterWindDirectionHook 供 main.go 调用，注册风向变更回调。
func RegisterWindDirectionHook(fn func(direction float64) error) {
	OnWindDirectionReceived = fn
}

// RegisterFailureHook 供 main.go 调用，注册特情触发回调。
func RegisterFailureHook(fn func(req FailureRequest) error) {
	OnFailureReceived = fn
}

// RegisterInstructorActionHook 供 main.go 调用，注册数字教官指令回调。
func RegisterInstructorActionHook(fn func(param int) error) {
	OnInstructorActionReceived = fn
}

// 甲方 A 上传训练数据的 HTTP 基地址（由 main.go 注入）
var aUploadBaseURL string
var eyeGazeCSVDir string

const eyeGazeCSVFileName = "openxr_eye_gaze.csv"

// InitAUploadEndpoint 供 main.go 调用，初始化甲方 A 上传地址
func InitAUploadEndpoint(base string) {
	// 顺手把结尾的 / 去掉，避免重复 //
	aUploadBaseURL = strings.TrimRight(base, "/")
}

// InitEyeGazeCSVDir 供 main.go 调用，初始化眼动 CSV 目录。
func InitEyeGazeCSVDir(dir string) {
	raw := strings.TrimSpace(dir)
	// 兼容 Windows 场景：即使配置里使用 "/"，也可被归一化处理。
	eyeGazeCSVDir = filepath.Clean(filepath.FromSlash(raw))
	fmt.Printf("👁️ 眼动 CSV 目录已配置: raw=%q, normalized=%q\n", raw, eyeGazeCSVDir)
}

// CurrentTask 当前任务挂在这里，给其他地方用
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
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {

		}
	}(f)

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
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("甲方A返回错误: status=%s, body=%s", resp.Status, string(body))
	}

	fmt.Println("✅ 已成功将训练数据 CSV 上传给甲方A。")
	return nil
}

// UploadEyeGazeCSV 在任务结束时，把 openxr_eye_gaze.csv 上传给甲方 A。
// 会使用 CurrentTask 中的 taskId 和 userNumber 作为 query 参数。
func UploadEyeGazeCSV() error {
	if CurrentTask == nil {
		return fmt.Errorf("CurrentTask 为空，无法上传眼动数据")
	}
	if aUploadBaseURL == "" {
		return fmt.Errorf("甲方A上传地址未初始化（aUploadBaseUrl 为空）")
	}
	if eyeGazeCSVDir == "" {
		return fmt.Errorf("眼动 CSV 目录未初始化（eyeGazeCsvDir 为空）")
	}

	taskID := CurrentTask.TaskID
	userNumber := CurrentTask.UserNumber
	csvPath := filepath.Join(eyeGazeCSVDir, eyeGazeCSVFileName)
	fmt.Printf("📤 准备上传眼动 CSV: %s\n", csvPath)

	f, err := os.Open(csvPath)
	if err != nil {
		return fmt.Errorf("打开眼动 CSV 文件失败: %w", err)
	}
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {

		}
	}(f)

	url := fmt.Sprintf("%s/flightSimulationTraining/uploadEyeGazeData?taskId=%d&userNumber=%s",
		aUploadBaseURL, taskID, userNumber)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", filepath.Base(csvPath))
	if err != nil {
		return fmt.Errorf("创建眼动上传表单文件字段失败: %w", err)
	}

	if _, err := io.Copy(part, f); err != nil {
		return fmt.Errorf("写入眼动表单文件内容失败: %w", err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("关闭眼动 multipart writer 失败: %w", err)
	}

	req, err := http.NewRequest("POST", url, &buf)
	if err != nil {
		return fmt.Errorf("构造眼动上传请求失败: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求甲方A眼动上传接口失败: %w", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("甲方A眼动上传返回错误: status=%s, body=%s", resp.Status, string(body))
	}

	fmt.Printf("✅ 已成功将眼动数据 CSV 上传给甲方A：%s\n", csvPath)
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
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(r.Body)

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
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(r.Body)

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

	if payload.Type < 0 || payload.Type > 4 {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"code": 0,
			"msg":  "stop ignored",
		}
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	if OnStopReceived != nil {
		if err := OnStopReceived(payload.Type); err != nil {
			fmt.Printf("❌ 处理中止回调失败: %v\n", err)
		}
	}

	if payload.Type == 4 {
		fmt.Println("🧾 [stop=4] 准备上传飞参 CSV ...")
		if err := UploadTrainDataCSV("telemetry.csv"); err != nil {
			fmt.Printf("❌ 上传训练数据到甲方A失败: %v\n", err)
			http.Error(w, "upload train data failed", http.StatusInternalServerError)
			return
		}
		fmt.Println("✅ [stop=4] 飞参 CSV 上传成功。")

		fmt.Println("🧾 [stop=4] 准备上传眼动 CSV ...")
		if err := UploadEyeGazeCSV(); err != nil {
			fmt.Printf("❌ 上传眼动数据到甲方A失败: %v\n", err)
			http.Error(w, "upload eye gaze data failed", http.StatusInternalServerError)
			return
		}
		fmt.Println("✅ [stop=4] 眼动 CSV 上传成功。")
	}

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"code": 0,
		"msg":  "stop received",
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// ReceiveWeatherHandler 接收甲方A天气变更 JSON
func ReceiveWeatherHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(r.Body)

	var payload struct {
		Weather *string `json:"weather"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid weather json", http.StatusBadRequest)
		return
	}
	if payload.Weather == nil {
		http.Error(w, "missing weather field", http.StatusBadRequest)
		return
	}

	if OnWeatherReceived != nil {
		if err := OnWeatherReceived(strings.TrimSpace(*payload.Weather)); err != nil {
			fmt.Printf("❌ 处理天气回调失败: %v\n", err)
			http.Error(w, "weather update failed", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"code": 0,
		"msg":  "weather received",
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// ReceiveTimePeriodHandler 接收甲方A时间变更 JSON
func ReceiveTimePeriodHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(r.Body)

	var payload struct {
		TimePeriod *string `json:"timePeriod"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid timePeriod json", http.StatusBadRequest)
		return
	}
	if payload.TimePeriod == nil {
		http.Error(w, "missing timePeriod field", http.StatusBadRequest)
		return
	}

	if OnTimePeriodReceived != nil {
		if err := OnTimePeriodReceived(strings.TrimSpace(*payload.TimePeriod)); err != nil {
			fmt.Printf("❌ 处理时间回调失败: %v\n", err)
			http.Error(w, "timePeriod update failed", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"code": 0,
		"msg":  "timePeriod received",
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// ReceiveVisibilityHandler 接收甲方A能见度变更 JSON（单位 km）
func ReceiveVisibilityHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(r.Body)

	var payload struct {
		Visibility *float64 `json:"visibility"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid visibility json", http.StatusBadRequest)
		return
	}
	if payload.Visibility == nil {
		http.Error(w, "missing visibility field", http.StatusBadRequest)
		return
	}

	if OnVisibilityReceived != nil {
		if err := OnVisibilityReceived(*payload.Visibility); err != nil {
			fmt.Printf("❌ 处理能见度回调失败: %v\n", err)
			http.Error(w, "visibility update failed", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"code": 0,
		"msg":  "visibility received",
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// ReceiveWindSpeedHandler 接收甲方A风速变更 JSON
func ReceiveWindSpeedHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(r.Body)

	var payload struct {
		WindSpeed *float64 `json:"windSpeed"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid windSpeed json", http.StatusBadRequest)
		return
	}
	if payload.WindSpeed == nil {
		http.Error(w, "missing windSpeed field", http.StatusBadRequest)
		return
	}

	if OnWindSpeedReceived != nil {
		if err := OnWindSpeedReceived(*payload.WindSpeed); err != nil {
			fmt.Printf("❌ 处理风速回调失败: %v\n", err)
			http.Error(w, "windSpeed update failed", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"code": 0,
		"msg":  "windSpeed received",
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// ReceiveWindDirectionHandler 接收甲方A风向变更 JSON
func ReceiveWindDirectionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(r.Body)

	var payload struct {
		WindDirection *float64 `json:"windDirection"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid windDirection json", http.StatusBadRequest)
		return
	}
	if payload.WindDirection == nil {
		http.Error(w, "missing windDirection field", http.StatusBadRequest)
		return
	}

	if OnWindDirectionReceived != nil {
		if err := OnWindDirectionReceived(*payload.WindDirection); err != nil {
			fmt.Printf("❌ 处理风向回调失败: %v\n", err)
			http.Error(w, "windDirection update failed", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"code": 0,
		"msg":  "windDirection received",
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// ReceiveFailureHandler 接收甲方A特情触发 JSON（单对象）
func ReceiveFailureHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(r.Body)

	var payload FailureRequest
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid failure json", http.StatusBadRequest)
		return
	}
	payload.FailureField = strings.TrimSpace(payload.FailureField)
	if payload.FailureField == "" {
		http.Error(w, "missing failure_field field", http.StatusBadRequest)
		return
	}

	if OnFailureReceived != nil {
		if err := OnFailureReceived(payload); err != nil {
			fmt.Printf("❌ 处理特情回调失败: %v\n", err)
			http.Error(w, "failure update failed", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"code": 0,
		"msg":  "failure received",
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// ReceiveInstructorActionHandler 接收甲方A数字教官指令 JSON
func ReceiveInstructorActionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(r.Body)

	var payload struct {
		Action string `json:"action"`
		Param  *int   `json:"param"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid instructor action json", http.StatusBadRequest)
		return
	}
	if payload.Param == nil {
		http.Error(w, "missing param field", http.StatusBadRequest)
		return
	}

	switch *payload.Param {
	case 0, 1, 2:
	default:
		http.Error(w, "invalid param value", http.StatusBadRequest)
		return
	}

	if OnInstructorActionReceived != nil {
		if err := OnInstructorActionReceived(*payload.Param); err != nil {
			fmt.Printf("❌ 处理数字教官回调失败: %v\n", err)
			http.Error(w, "instructor action failed", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"code": 0,
		"msg":  "instructor action received",
	}
	_ = json.NewEncoder(w).Encode(resp)
}
