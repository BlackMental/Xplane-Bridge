package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// 当用户在 CLI 中选择了某个动作（0-based 下标）时，如果注册了回调，就会被调用。
// 由 main.go 注册具体实现，例如：应用 X-Plane 场景。
var OnActionSelected func(actionIndex int) error

// 供 main.go 调用，注册回调。
func RegisterActionHook(fn func(actionIndex int) error) {
	OnActionSelected = fn
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

// 当前正在执行的动作索引（0-based，-1 表示当前没有执行中的动作）
var CurrentActionIndex = -1

// 只启动一次 CLI 输入循环
var cliOnce sync.Once

// ANSI 颜色常量
const (
	ansiReset  = "\033[0m"
	ansiTitle  = "\033[1;36m" // 青色标题
	ansiText   = "\033[1;37m" // 高亮文本
	ansiHint   = "\033[2;37m" // 淡色提示
	ansiGreen  = "\033[1;32m" // 绿色
	ansiRed    = "\033[1;31m" // 红色
	ansiYellow = "\033[1;33m" // 黄色
)

// clearScreen 清空终端并把光标移动到左上角
func clearScreen() {
	fmt.Print("\033[2J\033[H")
}

// printActionList 打印当前任务里的训练动作列表（命令行小界面）
func printActionList(task *TrainTaskRecordDetail) {
	clearScreen()

	fmt.Println()
	fmt.Println("╔════════════════════════════════════════════════════╗")
	fmt.Printf("║ %s🛠 训练任务动作列表（来自甲方A）%s                   ║\n", ansiTitle, ansiReset)
	fmt.Println("╚════════════════════════════════════════════════════╝")

	if task == nil {
		fmt.Printf("  %s⚠ 当前任务为空，无法显示训练动作列表。%s\n\n", ansiRed, ansiReset)
		return
	}
	if len(task.TrainTaskActions) == 0 {
		fmt.Printf("  %s⚠ 当前任务中没有任何训练动作。%s\n\n", ansiRed, ansiReset)
		return
	}

	fmt.Printf("\n%s📦 当前任务包含 %d 个动作：%s\n\n",
		ansiText, len(task.TrainTaskActions), ansiReset)

	sep := "─────────────────────────────────────────────────────"

	for i, act := range task.TrainTaskActions {
		isActive := (i == CurrentActionIndex)

		fmt.Println(sep)

		// 编号 + 动作名称行
		if isActive {
			fmt.Printf("%s⚡ [%-2d] 🎯 动作：✈ %s  （执行中）%s\n",
				ansiYellow, i+1, act.Name, ansiReset)
		} else {
			fmt.Printf("🆔 [%-2d] 🎯 动作：✈ %s\n", i+1, act.Name)
		}

		// 状态行
		if isActive {
			fmt.Printf("      状态：%s● 正在执行…%s\n", ansiYellow, ansiReset)
		} else {
			fmt.Printf("      状态：%s○ 待命%s\n", ansiHint, ansiReset)
		}

		// 控制“按钮”行
		startLabel := "🟢 START"
		stopLabel := "🟥 STOP"
		if isActive {
			startLabel = "✅ ACTIVE"
		}

		fmt.Printf("      控制： %s[%s]%s   %s[%s]%s\n",
			ansiGreen, startLabel, ansiReset,
			ansiRed, stopLabel, ansiReset,
		)
	}
	fmt.Println(sep)
	fmt.Println()

	// 操作提示区
	fmt.Printf("%s📟 操作提示：%s\n", ansiText, ansiReset)
	fmt.Printf("%s    • 输入 1..N：选择要启动的动作\n", ansiHint)
	fmt.Printf("    • 输入 e   ：结束当前动作（发送 Complete）\n")
	fmt.Printf("    • 输入 q   ：结束当前动作并退出程序（若有执行中的动作）%s\n\n", ansiReset)
}

// startActionByIndex 根据索引启动一个动作，并发送 SessionSwitchRequest 给甲方 B
func startActionByIndex(task *TrainTaskRecordDetail, idx int) {
	if task == nil {
		fmt.Println("⚠ 当前没有任务，无法启动动作。")
		return
	}
	if idx < 0 || idx >= len(task.TrainTaskActions) {
		fmt.Println("⚠ 动作索引超出范围。")
		return
	}

	// 先调用 X-Plane 场景钩子（如果已注册）
	if OnActionSelected != nil {
		if err := OnActionSelected(idx); err != nil {
			fmt.Printf("⚠ 应用 X-Plane 场景失败（动作索引 %d）：%v\n", idx, err)
		} else {
			fmt.Printf("✅ 已为动作索引 %d 应用 X-Plane 场景（高度/姿态/时间）。\n", idx)
		}
	}

	action := task.TrainTaskActions[idx]
	req := BuildSessionSwitchRequest(task, action)

	fmt.Printf("👉 准备发送 SessionSwitchRequest 给甲方B: taskId=%d, taskActionID=%s, name=%s\n",
		req.TaskID, req.TaskActionID, action.Name)

	if err := SendSessionSwitchToB(req); err != nil {
		fmt.Printf("❌ 发送 SessionSwitchRequest 给甲方B失败: %v\n", err)
	}
}

// completeCurrentSession 向甲方 B 发送 SessionComplete（reason=stop）
func completeCurrentSession() {
	fmt.Println("👉 准备发送 SessionCompleteRequest 给甲方B（reason=stop）")

	if err := SendSessionComplete(); err != nil {
		fmt.Printf("❌ 发送 SessionCompleteRequest 给甲方B失败: %v\n", err)
		return
	}

	fmt.Println("✅ 已成功发送 SessionCompleteRequest 给甲方B。")
}

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

// handleCLIInput 处理一次命令行输入
func handleCLIInput(input string) {
	input = strings.TrimSpace(input)
	if input == "" {
		return
	}

	switch input {
	case "q", "Q":
		// 退出前，如果有正在执行的动作，先补一发 Complete 给甲方B
		if CurrentTask != nil && CurrentActionIndex != -1 {
			fmt.Printf("🔚 退出前结束当前动作：[%d] %s\n",
				CurrentActionIndex+1, CurrentTask.TrainTaskActions[CurrentActionIndex].Name)
			completeCurrentSession()
			CurrentActionIndex = -1
		}

		// 然后把本次训练的 CSV 上传给甲方A
		if CurrentTask != nil {
			fmt.Println("📤 准备向甲方A上传训练数据文件 telemetry.csv ...")
			if err := UploadTrainDataCSV("telemetry.csv"); err != nil {
				fmt.Printf("❌ 上传训练数据到甲方A失败: %v\n", err)
			} else {
				fmt.Println("✅ 训练数据已成功上传给甲方A。")
			}
		} else {
			fmt.Println("ℹ 当前没有任务，不执行训练数据上传。")
		}

		fmt.Println("👋 收到退出指令，程序即将退出。")
		os.Exit(0)

	case "e", "E":
		if CurrentTask == nil {
			fmt.Println("⚠ 当前没有任务。")
			return
		}
		if CurrentActionIndex == -1 {
			fmt.Println("⚠ 当前没有执行中的动作。")
			return
		}

		fmt.Printf("🔚 结束动作：[%d] %s\n",
			CurrentActionIndex+1, CurrentTask.TrainTaskActions[CurrentActionIndex].Name)

		// 通知甲方 B 当前 Session 已完成
		completeCurrentSession()

		CurrentActionIndex = -1
		printActionList(CurrentTask)

	default:
		// 尝试解析为数字（选择动作）
		n, err := strconv.Atoi(input)
		if err != nil {
			fmt.Println("⚠ 无法识别的指令，请输入 1..N / e / q。")
			return
		}
		if CurrentTask == nil || len(CurrentTask.TrainTaskActions) == 0 {
			fmt.Println("⚠ 当前没有任何可用动作。")
			return
		}
		if n < 1 || n > len(CurrentTask.TrainTaskActions) {
			fmt.Println("⚠ 动作编号超出范围。")
			return
		}

		idx := n - 1
		CurrentActionIndex = idx
		printActionList(CurrentTask)

		fmt.Printf("🟢 选择动作：[%d] %s\n", n, CurrentTask.TrainTaskActions[idx].Name)

		// 👉 这里会先调用 OnActionSelected（如果已注册），再发 SessionSwitch 给 B
		startActionByIndex(CurrentTask, idx)
	}
}

// startCLIOnce 启动命令行输入循环（只启动一次）
func startCLIOnce() {
	cliOnce.Do(func() {
		go func() {
			scanner := bufio.NewScanner(os.Stdin)
			for {
				fmt.Print("👉 请输入指令（1..N / e / q）：")
				if !scanner.Scan() {
					fmt.Println("\n输入流结束，CLI 停止监听。")
					return
				}
				handleCLIInput(scanner.Text())
			}
		}()
	})
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
	// 新任务进来时，默认没有执行中的动作，完全由键盘选择
	CurrentActionIndex = -1

	// 打印动作列表界面
	printActionList(CurrentTask)

	// 启动命令行输入循环（只会启动一次）
	startCLIOnce()

	// 回 ACK 给甲方A
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"code": 0,
		"msg":  "task received",
	}
	_ = json.NewEncoder(w).Encode(resp)
}
