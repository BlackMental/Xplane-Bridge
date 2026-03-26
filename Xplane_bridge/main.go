package main

import (
	Aapi "Xplane_bridge/internal/api"
	cfgpkg "Xplane_bridge/internal/config"
	xp "Xplane_bridge/internal/xplane"

	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//
// ====================== CORS 中间件 ======================
//

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}

		reqMethod := r.Header.Get("Access-Control-Request-Method")
		reqHeaders := r.Header.Get("Access-Control-Request-Headers")

		allowMethods := "GET, POST, PUT, DELETE, OPTIONS"
		if reqMethod != "" {
			allowMethods = reqMethod + ", OPTIONS"
		}
		w.Header().Set("Access-Control-Allow-Methods", allowMethods)

		allowHeaders := "Content-Type, Authorization"
		if reqHeaders != "" {
			allowHeaders = reqHeaders
		}
		w.Header().Set("Access-Control-Allow-Headers", allowHeaders)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

//
// ====================== 单位转换 ======================
//

func normalizeSample(raw xp.TelemetrySample) xp.TelemetrySample {
	out := xp.TelemetrySample{
		Timestamp: raw.Timestamp,
		Values:    make(map[string]float64, len(raw.Values)),
	}

	for k, v := range raw.Values {
		switch k {

		case "IAS": //字段名字还是IAS　但是实际上已经是用真空速了，但根据甲方不更改Key字段名字的原则，咱们这里也不做更改
			// m/s → km/h
			out.Values[k] = v * 3.6

		case "ground_speed":
			// m/s → km/h
			out.Values[k] = v * 3.6

		case "vertical_speed":
			// ft/min → m/s
			out.Values[k] = v * 0.00508

		case "roll_rate", "pitch_rate", "yaw_rate":
			// rad/s → deg/s
			out.Values[k] = v * 57.2957795

		case "intake_pressure":
			// inHg → mmHg
			out.Values[k] = v * 25.4

		case "throttle_lever_displacement":
			// 0–1 → %
			out.Values[k] = v * 100.0

		case "engine_speed":
			// rad/s → rpm
			out.Values[k] = v * 9.549296585

		default:
			out.Values[k] = v
		}
	}

	return out
}

//
// ====================== UDP 发送 27 个字段 ======================
//

type UdpSender struct {
	conn *net.UDPConn
}

// NewUdpSender 建立 UDP“连接”，addr 形如 "192.168.1.159:6666"
func NewUdpSender(addr string) (*UdpSender, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("解析 UDP 地址失败: %w", err)
	}

	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return nil, fmt.Errorf("建立 UDP 连接失败: %w", err)
	}

	return &UdpSender{conn: conn}, nil
}

func (s *UdpSender) Close() error {
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

// SendSample 按 B 的要求打包一帧：
//
//	{
//	  "timestamp": 1732253730123,   // 毫秒级 Unix 时间戳
//	  "values": { ... 27 个字段 ... }
//	}
func (s *UdpSender) SendSample(sample xp.TelemetrySample) error {
	if s.conn == nil {
		return fmt.Errorf("UDP 连接为空")
	}

	tsMillis := sample.Timestamp.UnixNano() / int64(time.Millisecond)

	frame := struct {
		Timestamp int64              `json:"timestamp"`
		Values    map[string]float64 `json:"values"`
	}{
		Timestamp: tsMillis,
		Values:    sample.Values,
	}

	data, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("JSON 编码失败: %w", err)
	}

	if _, err := s.conn.Write(data); err != nil {
		return fmt.Errorf("UDP 发送失败: %w", err)
	}

	return nil
}

//
// ====================== main ======================
//

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "程序启动失败: %v\n", err)
		waitForExit()
	}
}

func run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	exeDir, err := executableDir()
	if err != nil {
		return fmt.Errorf("获取程序目录失败: %w", err)
	}

	// 0. 加载配置
	cfgPath := filepath.Join(exeDir, "config.json")
	cfg, err := cfgpkg.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}

	debugLogf := func(format string, args ...any) {
		if cfg.Debug {
			fmt.Printf(format, args...)
		}
	}

	// 0.1 初始化甲方 B 的 HTTP 端点（替代原来的常量 URL）
	Aapi.InitBEndpoints(cfg.BBaseURL)

	// 0.2 初始化甲方 A 的上传端点
	Aapi.InitAUploadEndpoint(cfg.AUploadBaseURL)
	Aapi.InitEyeGazeCSVDir(cfg.EyeGazeCSVDir)

	// ================= HTTP Server for 甲方A ===================
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/fromA/task", Aapi.ReceiveTaskHandler)
		mux.HandleFunc("/api/fromA/stop", Aapi.ReceiveStopHandler)
		mux.HandleFunc("/api/fromA/weather", Aapi.ReceiveWeatherHandler)
		mux.HandleFunc("/api/fromA/timePeriod", Aapi.ReceiveTimePeriodHandler)
		mux.HandleFunc("/api/fromA/visibility", Aapi.ReceiveVisibilityHandler)
		mux.HandleFunc("/api/fromA/windSpeed", Aapi.ReceiveWindSpeedHandler)
		mux.HandleFunc("/api/fromA/windDirection", Aapi.ReceiveWindDirectionHandler)
		mux.HandleFunc("/api/fromA/failure", Aapi.ReceiveFailureHandler)
		mux.HandleFunc("/api/fromA/instructor/action", Aapi.ReceiveInstructorActionHandler)

		handler := cors(mux)

		fmt.Printf("甲方A监听端点已启动：http://<本机IP>%s/api/fromA/task\n", cfg.HTTPFromAAddr)

		if err := http.ListenAndServe(cfg.HTTPFromAAddr, handler); err != nil {
			fmt.Fprintf(os.Stderr, "HTTP 服务启动失败: %v\n", err)
			waitForExit()
		}
	}()

	// 1. 创建 X-Plane 客户端（地址从配置中读取）
	client := xp.NewClient(cfg.XPlaneBaseURL)
	client.SetDebug(cfg.Debug)

	Aapi.RegisterFailureHook(func(req Aapi.FailureRequest) error {
		return client.SetDatarefValueByName(ctx, req.FailureField, req.Param)
	})

	Aapi.RegisterInstructorActionHook(func(param int) error {
		var command string
		switch param {
		case 1:
			command = "cj6/replay/play"
		case 0:
			command = "cj6/replay/stop"
		case 2:
			command = "cj6/replay/restart"
		default:
			return fmt.Errorf("未知的数字教官 param: %d", param)
		}
		debugLogf("🐞 数字教官指令: param=%d, command=%s\n", param, command)
		return client.ExecuteCommandOnce(ctx, command)
	})

	// 2. 检查 Web API 能否连通
	xCap, err := client.GetCapabilities(ctx)
	if err != nil {
		return fmt.Errorf("获取 capabilities 失败: %w", err)
	}

	fmt.Println("X-Plane Web API 能连通 ✅")
	fmt.Printf("X-Plane 版本: %s\n", xCap.XPlane.Version)
	fmt.Printf("支持的 API 版本: %v\n", xCap.API.Versions)
	fmt.Println()

	// 3. 定义 27 个字段（甲方字段名 → X-Plane DataRef）
	idx0 := 0

	fields := []xp.TelemetryField{
		{Key: "altitude", Name: "sim/flightmodel/position/elevation"},
		{Key: "yaw", Name: "sim/flightmodel/position/true_psi"},
		{Key: "pitch", Name: "sim/flightmodel/position/theta"},
		{Key: "roll", Name: "sim/flightmodel/position/phi"},

		{Key: "axial_overload", Name: "sim/flightmodel/forces/g_axil"},
		{Key: "dharma_overload", Name: "sim/flightmodel/forces/g_nrml"},
		{Key: "lateral_overload", Name: "sim/flightmodel/forces/g_side"},

		{Key: "longitude", Name: "sim/flightmodel/position/longitude"},
		{Key: "latitude", Name: "sim/flightmodel/position/latitude"},

		{Key: "IAS", Name: "sim/flightmodel/position/true_airspeed"}, //字段名字还是IAS　但是实际上已经是用真空速了，但根据甲方不更改Key字段名字的原则，咱们这里也不做更改

		// 发动机 & 油门
		{Key: "intake_pressure", Name: "sim/flightmodel/engine/ENGN_MPR", Index: &idx0},
		{Key: "throttle_lever_displacement", Name: "sim/cockpit2/engine/actuators/throttle_ratio_all"},
		{Key: "engine_speed", Name: "sim/flightmodel/engine/ENGN_tacrad", Index: &idx0},

		// 操纵机构
		{Key: "rudder_deflection", Name: "sim/cockpit2/controls/yoke_heading_ratio"},
		{Key: "elevator_deflection", Name: "sim/cockpit2/controls/yoke_pitch_ratio"},
		{Key: "aileron_deflection", Name: "sim/cockpit2/controls/yoke_roll_ratio"},

		// 垂直速度 & 角速度
		{Key: "vertical_speed", Name: "sim/flightmodel/position/vh_ind_fpm"},
		{Key: "roll_rate", Name: "sim/flightmodel/position/P"},
		{Key: "yaw_rate", Name: "sim/flightmodel/position/R"},
		{Key: "pitch_rate", Name: "sim/flightmodel/position/Q"},

		// 发动机温度 & 螺距
		{Key: "engine_temperature", Name: "sim/flightmodel/engine/ENGN_CHT_c", Index: &idx0},
		{Key: "propeller_pitch", Name: "sim/cockpit2/engine/actuators/prop_pitch_deg", Index: &idx0},

		// 其他
		{Key: "AOA", Name: "sim/flightmodel2/misc/AoA_angle_degrees"},
		{Key: "undercarriage", Name: "sim/cockpit2/controls/gear_handle_down"},
		{Key: "flape", Name: "sim/cockpit2/controls/flap_ratio"},
		{Key: "ground_speed", Name: "sim/flightmodel/position/groundspeed"},
		{Key: "Altitude_AGL", Name: "sim/flightmodel/position/y_agl"},
	}

	// 4. 建立 UDP 发送器（27 个字段实时推给 B），地址来自配置
	udpSender, err := NewUdpSender(cfg.TelemetryUDP)
	if err != nil {
		return fmt.Errorf("创建 UDP 发送器失败: %w", err)
	}
	defer func(sender *UdpSender) {
		if err := sender.Close(); err != nil {
		}
	}(udpSender)
	fmt.Printf("UDP 已连接到 %s，用于实时发送 27 个 Telemetry 字段给甲方 B。\n\n", cfg.TelemetryUDP)

	// 5. CSV 记录器（按任务重置）
	var (
		csvFile   *os.File
		csvWriter *csv.Writer
		csvMu     sync.Mutex
	)

	// CSV 表头：timestamp_ms + 27 个字段 key
	buildCSVHeader := func() []string {
		header := make([]string, 0, len(fields)+1)
		header = append(header, "timestamp_ms")
		for _, f := range fields {
			header = append(header, f.Key)
		}
		return header
	}

	resetCSV := func() error {
		csvMu.Lock()
		defer csvMu.Unlock()

		if csvWriter != nil {
			csvWriter.Flush()
		}
		if csvFile != nil {
			if err := csvFile.Close(); err != nil {
				return fmt.Errorf("关闭旧 CSV 文件失败: %w", err)
			}
		}

		file, err := os.Create(filepath.Join(exeDir, "telemetry.csv"))
		if err != nil {
			return fmt.Errorf("创建 CSV 文件失败: %w", err)
		}
		writer := csv.NewWriter(file)
		if err := writer.Write(buildCSVHeader()); err != nil {
			_ = file.Close()
			return fmt.Errorf("写入 CSV 表头失败: %w", err)
		}
		writer.Flush()

		csvFile = file
		csvWriter = writer

		fmt.Println("CSV 记录已开启，输出文件: telemetry.csv")
		return nil
	}

	closeCSV := func() error {
		csvMu.Lock()
		defer csvMu.Unlock()

		if csvWriter != nil {
			csvWriter.Flush()
			if err := csvWriter.Error(); err != nil {
				return fmt.Errorf("刷新 CSV 失败: %w", err)
			}
		}
		if csvFile != nil {
			if err := csvFile.Close(); err != nil {
				return fmt.Errorf("关闭 CSV 文件失败: %w", err)
			}
		}
		csvFile = nil
		csvWriter = nil
		return nil
	}

	var telemetryEnabled atomic.Bool
	telemetryEnabled.Store(false)

	// 2.1 注册“任务收到时”的 X-Plane 场景应用钩子
	Aapi.RegisterTaskHook(func(task *Aapi.TrainTaskRecordDetail) error {
		if task == nil {
			return fmt.Errorf("任务为空，无法应用场景")
		}

		if telemetryEnabled.Load() {
			fmt.Println("⚠ 检测到任务执行中又收到新任务，将清空当前 CSV 记录并重新开始。")
		}
		if err := resetCSV(); err != nil {
			return err
		}

		if err := client.ExecuteCommandOnce(ctx, "project/eye_gaze/toggle_record_pause"); err != nil {
			return fmt.Errorf("发送眼动开始记录指令失败: %w", err)
		}

		fmt.Printf("▶ 收到任务，开始初始化 X-Plane（位置 + 环境）...\n")

		// 1) 位置初始化：二选一
		if task.AirspaceExecute {
			fmt.Printf("   - airspaceExecuteFlag=true：按空域参数初始化位置（高度/姿态/速度）...\n")
			sc := buildScenarioFromTask(task)
			if sc == nil {
				fmt.Printf("⚠ 无法从任务构建空域场景，跳过位置初始化\n")
			} else if err := client.ApplyScenario(ctx, sc); err != nil {
				// 位置初始化失败：记录警告，但不让进程中断
				fmt.Printf("⚠ 空域位置初始化失败（不中断进程）: %v\n", err)
			}
		} else {
			icao := strings.TrimSpace(task.AirportNumber)
			fmt.Printf("   - airspaceExecuteFlag=false：按机场初始化位置（airportNumber=%s）...\n", icao)

			if err := client.PlaceAtAirport(ctx, icao); err != nil {
				fmt.Printf("⚠ 机场位置初始化失败（不中断进程）: %v\n", err)

				// ✅ 仅当“Command 不存在/无效”时，执行兜底
				if xp.IsCommandNotFound(err) {
					fallback := "GZJC"
					fmt.Printf("⚠ 检测到机场 Command 不存在，启用兜底机场：%s\n", fallback)
					if err2 := client.PlaceAtAirport(ctx, fallback); err2 != nil {
						fmt.Printf("⚠ 兜底机场初始化也失败（仍不中断进程）: %v\n", err2)
					} else {
						fmt.Printf("✅ 已成功兜底初始化到机场：%s\n", fallback)
					}
				}
			}

		}

		// 2) 环境初始化：无论走哪个分支都执行（时间 / 天气）
		if err := applyTimePeriod(ctx, client, task.TimePeriod); err != nil {
			fmt.Printf("⚠ 设置时间失败（不中断进程）: %v\n", err)
		}

		if err := applyWeather(ctx, client, task.Weather); err != nil {
			fmt.Printf("⚠ 设置天气失败（不中断进程）: %v\n", err)
		}

		telemetryEnabled.Store(true)
		return nil
	})

	Aapi.RegisterStopHook(func(stopType int) error {
		switch stopType {
		case 0, 2:
			if err := client.ExecuteCommandOnce(ctx, "sim/operation/pause_on"); err != nil {
				return fmt.Errorf("发送暂停指令失败: %w", err)
			}
			return nil
		case 1:
			if err := client.ExecuteCommandOnce(ctx, "sim/operation/pause_off"); err != nil {
				return fmt.Errorf("发送继续指令失败: %w", err)
			}
			return nil
		case 3:
			if err := resetCSV(); err != nil {
				return err
			}
			return nil
		case 4:
			telemetryEnabled.Store(false)
			if err := closeCSV(); err != nil {
				return err
			}
			if err := client.ExecuteCommandOnce(ctx, "project/eye_gaze/finish_recording"); err != nil {
				return fmt.Errorf("发送眼动结束记录指令失败: %w", err)
			}
			return nil
		default:
			return nil
		}
	})

	Aapi.RegisterWeatherHook(func(weather string) error {
		return applyWeather(ctx, client, weather)
	})

	Aapi.RegisterTimePeriodHook(func(timePeriod string) error {
		return applyTimePeriod(ctx, client, timePeriod)
	})

	Aapi.RegisterVisibilityHook(func(visibilityKm float64) error {
		visibilitySm := visibilityKm * kmToStatuteMiles
		debugLogf("🐞 能见度指令: 收到=%.3f km, 下发=%.3f sm (dataref=sim/weather/region/visibility_reported_sm)\n",
			visibilityKm, visibilitySm)
		return client.SetVisibilityReportedSm(ctx, visibilitySm)
	})

	Aapi.RegisterWindSpeedHook(func(speed float64) error {
		indices := windLayerIndices()
		debugLogf("🐞 风速指令: 收到=%.3f m/s, 下发=%.3f m/s (indices=%v, dataref=sim/weather/region/wind_speed_msc)\n",
			speed, speed, indices)
		return client.SetWindSpeedAtIndices(ctx, indices, speed)
	})

	Aapi.RegisterWindDirectionHook(func(direction float64) error {
		indices := windLayerIndices()
		debugLogf("🐞 风向指令: 收到=%.3f°, 下发=%.3f° (indices=%v, dataref=sim/weather/region/wind_direction_degt)\n",
			direction, direction, indices)
		return client.SetWindDirectionAtIndices(ctx, indices, direction)
	})

	// 6. 订阅 Telemetry 数据流（10Hz）
	fmt.Println("开始订阅 Telemetry 数据流...")
	ch, err := client.SubscribeTelemetry(ctx, fields)
	if err != nil {
		return fmt.Errorf("订阅 Telemetry 失败: %w", err)
	}

	fmt.Println("订阅成功，开始接收数据（模式：单位换算 + UDP 转发 + CSV 记录）。")

	// 7. 主循环：每一帧 → 单位转换 → UDP → CSV
	for sample := range ch {
		if !telemetryEnabled.Load() {
			continue
		}

		normalized := normalizeSample(sample)

		// 7.1 UDP 推流
		if err := udpSender.SendSample(normalized); err != nil {
			fmt.Printf("⚠ 发送 UDP 失败: %v\n", err)
		}

		// 7.2 CSV 记录
		tsMillis := normalized.Timestamp.UnixNano() / int64(time.Millisecond)
		row := make([]string, 0, len(fields)+1)
		row = append(row, strconv.FormatInt(tsMillis, 10))

		for _, f := range fields {
			v := normalized.Values[f.Key]
			row = append(row, strconv.FormatFloat(v, 'f', 6, 64))
		}

		csvMu.Lock()
		if csvWriter == nil {
			csvMu.Unlock()
			continue
		}
		if err := csvWriter.Write(row); err != nil {
			fmt.Printf("写入 CSV 行失败: %v\n", err)
		}
		csvWriter.Flush()
		csvMu.Unlock()
	}

	fmt.Println("程序结束。")
	return nil
}

func executableDir() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exePath), nil
}

func waitForExit() {
	if runtime.GOOS != "windows" {
		return
	}
	fmt.Println("按回车键退出...")
	_, _ = fmt.Fscanln(os.Stdin)
}

// ====================== 从甲方A任务构建场景 ======================

func buildScenarioFromTask(t *Aapi.TrainTaskRecordDetail) *xp.ScenarioConfig {
	if t == nil {
		return nil
	}

	sc := &xp.ScenarioConfig{
		// 横滚 / 俯仰：先按你说的默认 0
		PitchDeg: 0,
		RollDeg:  0,
	}

	// 航向：来自 airspaceXPlane.initHeading
	sc.YawDeg = t.AirspaceXPlane.InitHeading

	// 高度：来自 airspaceXPlane.initHeight（假设单位：米，对应 local_y）
	sc.AltitudeLocalY = t.AirspaceXPlane.InitHeight

	// 速度：来自 airspaceXPlane.initSpeed（假设单位 km/h）
	sc.InitSpeedKmh = t.AirspaceXPlane.InitSpeed

	// 时间：由 timePeriod 映射（昼间 / 黄昏 / 夜间 / 拂晓 / 阴天）
	// 注意：时间/天气属于“环境初始化”，不耦合在位置初始化里（由上层统一下发）
	sc.TimeZuluSec = 0

	return sc
}

const (
	kmToStatuteMiles   = 0.621371
	windLayersToUpdate = 5
)

func windLayerIndices() []int {
	indices := make([]int, windLayersToUpdate)
	for i := 0; i < windLayersToUpdate; i++ {
		indices[i] = i
	}
	return indices
}

func applyWeather(ctx context.Context, client *xp.Client, weather string) error {
	if preset, ok := weatherToPreset(weather); ok {
		if err := client.SetWeatherPreset(ctx, preset); err != nil {
			return err
		}
		return nil
	}
	if strings.TrimSpace(weather) != "" {
		return fmt.Errorf("未识别的天气类型: %s", weather)
	}
	return nil
}

func applyTimePeriod(ctx context.Context, client *xp.Client, timePeriod string) error {
	zuluSec := timePeriodToZuluSeconds(timePeriod)
	if zuluSec > 0 {
		if err := client.SetZuluTimeSec(ctx, zuluSec); err != nil {
			return err
		}
		return nil
	}
	if strings.TrimSpace(timePeriod) != "" {
		return fmt.Errorf("未识别的 timePeriod: %s", timePeriod)
	}
	return nil
}

func weatherToPreset(weather string) (int, bool) {
	switch strings.TrimSpace(weather) {
	case "晴空":
		return 0, true
	case "少云":
		return 2, true
	case "阴天":
		return 4, true
	case "多云":
		return 3, true
	case "小雨":
		return 7, true
	case "中雨":
		return 8, true
	case "雾天":
		return 5, true
	default:
		return 0, false
	}
}

// timePeriodToZuluSeconds：把“昼间/黄昏/夜间/拂晓/阴天”映射为一天中的秒数。
func timePeriodToZuluSeconds(tp string) int64 {
	switch tp {
	case "昼间":
		// 正午偏后一点，比如 13:00
		return int64(5 * 3600)
	case "黄昏":
		// 黄昏 18:30
		return int64(11 * 3600)
	case "夜间":
		// 夜间 23:00
		return int64(13 * 3600)
	case "拂晓":
		// 拂晓 05:30
		return int64(20 * 3600)
	default:
		// 默认正午 12:00
		return int64(6 * 3600)
	}
}
