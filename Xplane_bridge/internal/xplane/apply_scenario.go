package xplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sync"
)

// ScenarioConfig 描述“要把飞机摆成什么状态”的一组参数。
// 目前先用：高度 + 姿态 + 时间 + 初始速度。
type ScenarioConfig struct {
	// 高度：本地坐标系 Y（米）
	AltitudeLocalY float64

	// 姿态：航向 / 俯仰 / 横滚（度）
	YawDeg   float64
	PitchDeg float64
	RollDeg  float64

	// 时间：Zulu 秒（0~86399）
	TimeZuluSec int64

	// 初始速度：单位 km/h（来自甲方 A 的 initSpeed）
	InitSpeedKmh float64
}

// ====================== DataRef 缓存 ======================

type scenarioDatarefs struct {
	overridePlanePathID int64

	psiID      int64
	thetaID    int64
	phiID      int64
	localYID   int64
	zuluTimeID int64

	localVxID int64
	localVyID int64
	localVzID int64
}

var (
	scDrOnce sync.Once
	scDr     scenarioDatarefs
	scDrErr  error
)

// initScenarioDatarefs：懒加载一次所有用到的 DataRef ID
func (c *Client) initScenarioDatarefs(ctx context.Context) error {
	scDrOnce.Do(func() {
		lookup := func(name string, target *int64) {
			if scDrErr != nil {
				return
			}
			id, err := c.FindDatarefByName(ctx, name)
			if err != nil {
				scDrErr = fmt.Errorf("查找 DataRef %s 失败: %w", name, err)
				return
			}
			*target = id
		}

		lookup("sim/operation/override/override_planepath", &scDr.overridePlanePathID)

		lookup("sim/flightmodel/position/psi", &scDr.psiID)
		lookup("sim/flightmodel/position/theta", &scDr.thetaID)
		lookup("sim/flightmodel/position/phi", &scDr.phiID)

		lookup("sim/flightmodel/position/local_y", &scDr.localYID)
		lookup("sim/time/zulu_time_sec", &scDr.zuluTimeID)

		lookup("sim/flightmodel/position/local_vx", &scDr.localVxID)
		lookup("sim/flightmodel/position/local_vy", &scDr.localVyID)
		lookup("sim/flightmodel/position/local_vz", &scDr.localVzID)
	})

	return scDrErr
}

// 写数组 DataRef 的某一个 index（REST PATCH + ?index=）
func (c *Client) writeArrayElement(ctx context.Context, id int64, index int, value float64) error {
	url := fmt.Sprintf("%s/datarefs/%d/value?index=%d", c.baseV2, id, index)

	body := struct {
		Data float64 `json:"data"`
	}{
		Data: value,
	}

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
			return fmt.Errorf("写数组 DataRef 失败: http %d, %s: %s",
				resp.StatusCode, errResp.ErrorCode, errResp.ErrorMessage)
		}
		return fmt.Errorf("写数组 DataRef 返回错误状态码: %d", resp.StatusCode)
	}

	return nil
}

// ====================== 应用场景 ======================

// ApplyScenario：根据 ScenarioConfig 设置飞机高度 / 姿态 / 时间 / 初始速度
func (c *Client) ApplyScenario(ctx context.Context, sc *ScenarioConfig) error {
	if sc == nil {
		return fmt.Errorf("ScenarioConfig 不能为空")
	}

	// 1. 初始化所需的 DataRef ID
	if err := c.initScenarioDatarefs(ctx); err != nil {
		return err
	}

	// 2. 开启 override_planepath[0]，短时间接管飞机路径
	if err := c.writeArrayElement(ctx, scDr.overridePlanePathID, 0, 1); err != nil {
		return fmt.Errorf("开启 override_planepath 失败: %w", err)
	}
	// 尽量保证最后关掉（即便中途写失败，也尝试还原）
	defer func() {
		if err := c.writeArrayElement(ctx, scDr.overridePlanePathID, 0, 0); err != nil {
			fmt.Printf("⚠ 关闭 override_planepath 失败: %v\n", err)
		}
	}()

	// 3. 写姿态
	if err := c.SetDatarefValue(ctx, scDr.psiID, sc.YawDeg); err != nil {
		return fmt.Errorf("写航向 psi 失败: %w", err)
	}
	if err := c.SetDatarefValue(ctx, scDr.thetaID, sc.PitchDeg); err != nil {
		return fmt.Errorf("写俯仰 theta 失败: %w", err)
	}
	if err := c.SetDatarefValue(ctx, scDr.phiID, sc.RollDeg); err != nil {
		return fmt.Errorf("写横滚 phi 失败: %w", err)
	}

	// 4. 写高度
	if sc.AltitudeLocalY != 0 {
		if err := c.SetDatarefValue(ctx, scDr.localYID, sc.AltitudeLocalY); err != nil {
			return fmt.Errorf("写 local_y 失败: %w", err)
		}
	}

	// 5. 写时间（Zulu 秒）
	if sc.TimeZuluSec > 0 {
		if err := c.SetDatarefValue(ctx, scDr.zuluTimeID, sc.TimeZuluSec); err != nil {
			return fmt.Errorf("写 zulu_time_sec 失败: %w", err)
		}
	}

	// 6. 写初始速度（local_vx / local_vy / local_vz）
	if sc.InitSpeedKmh > 0 {
		// 甲方给的是 km/h，转成 m/s
		vMS := sc.InitSpeedKmh / 3.6

		// 以当前航向为准，分解到 X/Z 平面
		yawRad := sc.YawDeg * math.Pi / 180.0

		// X 向（东西向），Z 向（南北向）——这里采用“教科书版本”，
		// 后面你要是发现方向反了，我们再按 X-Plane 的实际坐标系微调符号就行。

		vx := vMS * math.Sin(yawRad)
		vz := -vMS * math.Cos(yawRad)
		vy := 0

		if err := c.SetDatarefValue(ctx, scDr.localVxID, vx); err != nil {
			return fmt.Errorf("写 local_vx 失败: %w", err)
		}
		if err := c.SetDatarefValue(ctx, scDr.localVyID, vy); err != nil {
			return fmt.Errorf("写 local_vy 失败: %w", err)
		}
		if err := c.SetDatarefValue(ctx, scDr.localVzID, vz); err != nil {
			return fmt.Errorf("写 local_vz 失败: %w", err)
		}
	}

	return nil
}
