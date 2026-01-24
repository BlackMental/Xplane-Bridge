package api

// ===== 甲方A -> 中间件：任务主结构 =====

// TrainTaskRecordDetail 就对应甲方A发来的那个大 JSON
type TrainTaskRecordDetail struct {
	TaskID           int               `json:"taskId"`
	UserNumber       string            `json:"userNumber"`
	AirportNumber    string            `json:"airportNumber"`
	RunwayXPlane     RunwayXPlane      `json:"runwayXPlane"`
	ScenarioJSON     string            `json:"scenarioJson"`
	AirspaceExecute  bool              `json:"airspaceExecuteFlag"`
	AirspaceXPlane   AirspaceXPlane    `json:"airspaceXPlane"`
	Weather          string            `json:"weather"`
	TimePeriod       string            `json:"timePeriod"`
	Visibility       float64           `json:"visibility"`
	WindSpeed        float64           `json:"windSpeed"`
	WindDirection    float64           `json:"windDirection"`
	TrainTaskActions []TrainTaskAction `json:"trainTaskActions"`
}

// 起飞机场跑道
type RunwayXPlane struct {
	RunwayNumber string  `json:"runwayNumber"`
	StartLon     float64 `json:"startLon"`
	StartLat     float64 `json:"startLat"`
}

// 空域初始状态（给 X-Plane 用）
type AirspaceXPlane struct {
	Longitude   float64 `json:"longitude"`
	Latitude    float64 `json:"latitude"`
	InitHeight  float64 `json:"initHeight"`
	InitHeading float64 `json:"initHeading"`
	InitSpeed   float64 `json:"initSpeed"`
}

// 训练动作（注意：A 那边字段名是 itemId）
type TrainTaskAction struct {
	UUID                string `json:"uuid"`
	SortieID            string `json:"sortieId"`
	ItemID              string `json:"itemId"` // A -> 中间件，用 itemId
	Name                string `json:"name"`
	TrainMode           int    `json:"trainMode"`
	TrainCount          int    `json:"trainCount"`
	TrainTime           int    `json:"trainTime"`
	ManeuverProfileJSON string `json:"maneuverProfileJson"` // 字符串里的 JSON，原样转发
	RulesJSON           string `json:"rulesJson"`           // 字符串里的 JSON，原样转发
}

// ===== 中间件 -> 甲方B：SessionSwitch 请求体 =====
//
// 按 B 实际吃的结构：
//
// {
//   "taskId": 58,
//   "userNumber": "wzx",
//   "taskActionId": "xxx",         // 注意这里是 taskActionId
//   "maneuverProfileJson": "...",
//   "trainMode": 2,
//   "scenarioJson": "...",
//   "rulesJson": "..."
// }

type SessionSwitchRequest struct {
	TaskID              int    `json:"taskId"`
	UserNumber          string `json:"userNumber"`
	TaskActionID        string `json:"taskActionId"`        // B -> 用 taskActionId
	ManeuverProfileJSON string `json:"maneuverProfileJson"` // 仍然是 string
	TrainMode           int    `json:"trainMode"`
	ScenarioJSON        string `json:"scenarioJson"` // string
	RulesJSON           string `json:"rulesJson"`    // string
}

// 构造发给 B 的请求体：这里做 itemId -> taskActionId 的映射
func BuildSessionSwitchRequest(task *TrainTaskRecordDetail, action TrainTaskAction) SessionSwitchRequest {
	return SessionSwitchRequest{
		TaskID:              task.TaskID,
		UserNumber:          task.UserNumber,
		TaskActionID:        action.ItemID,              // ⭐ 从 itemId 映射到 taskActionId
		ManeuverProfileJSON: action.ManeuverProfileJSON, // 这仨都是字符串，原样转发
		TrainMode:           action.TrainMode,
		ScenarioJSON:        task.ScenarioJSON,
		RulesJSON:           action.RulesJSON,
	}
}

// ===== 中间件 -> 甲方B：SessionComplete 请求体 =====

// ===== 中间件 -> 甲方B：SessionComplete 请求体 =====
//
// 甲方 B 的 SessionComplete 接口只接受：
//   { "reason": "stop" }
// 不能带 taskId、taskActionId 等任何字段。

type SessionCompleteRequest struct {
	Reason string `json:"reason"`
}

func BuildSessionCompleteRequest() SessionCompleteRequest {
	return SessionCompleteRequest{
		Reason: "stop",
	}
}
