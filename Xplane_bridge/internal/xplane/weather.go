package xplane

import (
	"context"
	"fmt"
	"sync"
)

type weatherDatarefs struct {
	presetID int64
}

var (
	weatherDrOnce sync.Once
	weatherDr     weatherDatarefs
	weatherDrErr  error
)

func (c *Client) initWeatherDatarefs(ctx context.Context) error {
	weatherDrOnce.Do(func() {
		id, err := c.FindDatarefByName(ctx, "sim/weather/region/weather_preset")
		if err != nil {
			weatherDrErr = fmt.Errorf("查找 DataRef sim/weather/region/weather_preset 失败: %w", err)
			return
		}
		weatherDr.presetID = id
	})

	return weatherDrErr
}

func (c *Client) SetWeatherPreset(ctx context.Context, preset int) error {
	if err := c.initWeatherDatarefs(ctx); err != nil {
		return err
	}

	if err := c.SetDatarefValue(ctx, weatherDr.presetID, preset); err != nil {
		return fmt.Errorf("写入天气预设失败: %w", err)
	}

	return nil
}
