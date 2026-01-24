package xplane

import (
	"context"
	"fmt"
	"sync"
)

type weatherDatarefs struct {
	presetID        int64
	visibilityID    int64
	windSpeedID     int64
	windDirectionID int64
}

var (
	weatherDrOnce sync.Once
	weatherDr     weatherDatarefs
	weatherDrErr  error
)

func (c *Client) initWeatherDatarefs(ctx context.Context) error {
	weatherDrOnce.Do(func() {
		lookup := func(name string, target *int64) {
			if weatherDrErr != nil {
				return
			}
			id, err := c.FindDatarefByName(ctx, name)
			if err != nil {
				weatherDrErr = fmt.Errorf("查找 DataRef %s 失败: %w", name, err)
				return
			}
			*target = id
		}

		lookup("sim/weather/region/weather_preset", &weatherDr.presetID)
		lookup("sim/weather/region/visibility_reported_sm", &weatherDr.visibilityID)
		lookup("sim/weather/region/wind_speed_msc", &weatherDr.windSpeedID)
		lookup("sim/weather/region/wind_direction_degt", &weatherDr.windDirectionID)
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

func (c *Client) SetVisibilityReportedSm(ctx context.Context, visibilitySm float64) error {
	if err := c.initWeatherDatarefs(ctx); err != nil {
		return err
	}

	if err := c.SetDatarefValue(ctx, weatherDr.visibilityID, visibilitySm); err != nil {
		return fmt.Errorf("写入能见度失败: %w", err)
	}

	return nil
}

func (c *Client) SetWindSpeedAtIndex(ctx context.Context, index int, speed float64) error {
	if err := c.initWeatherDatarefs(ctx); err != nil {
		return err
	}

	if err := c.writeArrayElement(ctx, weatherDr.windSpeedID, index, speed); err != nil {
		return fmt.Errorf("写入风速失败: %w", err)
	}

	return nil
}

func (c *Client) SetWindDirectionAtIndex(ctx context.Context, index int, direction float64) error {
	if err := c.initWeatherDatarefs(ctx); err != nil {
		return err
	}

	if err := c.writeArrayElement(ctx, weatherDr.windDirectionID, index, direction); err != nil {
		return fmt.Errorf("写入风向失败: %w", err)
	}

	return nil
}
