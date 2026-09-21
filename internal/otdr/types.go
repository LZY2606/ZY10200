// Package otdr 实现 OTDR 轨迹的距离校准、分段基线、事件识别与累积损耗计算。
//
// 数据口径（重要）：
//   - SamplePower 是不可变的原始线性采样；导入后永不随折射率变化。
//   - 采样索引（sample index）才是样本身份；米制距离只是由群折射率与
//     仪器时间基换算出来的一个属性。
//   - 事件身份锚定在“锚点采样索引”上，绝不锚定在米制距离上。
//   - 多条轨迹只能在距离校准后的同一米制坐标上对齐显示；外观相近的峰
//     不会自动视为同一物理设施，跨轨迹对应必须由用户显式确认。
package otdr

import "fmt"

// TraceParams 保存一条轨迹的采集参数。
type TraceParams struct {
	PulseWidthNS   float64 `json:"pulse_width_ns"`     // 脉宽（纳秒）
	GroupIndex     float64 `json:"group_index"`        // 群折射率 n
	AvgCount       int     `json:"avg_count"`          // 平均次数
	SampleInterval float64 `json:"sample_interval_ns"` // 采样点时间间隔（纳秒）
	LaunchDeadzone float64 `json:"launch_deadzone_m"`  // 发射端死区（米），用户可调
}

// Validate 校验采集参数。
func (p TraceParams) Validate() error {
	switch {
	case p.PulseWidthNS <= 0:
		return fmt.Errorf("脉宽必须为正")
	case p.GroupIndex <= 1:
		return fmt.Errorf("群折射率必须大于 1")
	case p.AvgCount <= 0:
		return fmt.Errorf("平均次数必须为正")
	case p.SampleInterval <= 0:
		return fmt.Errorf("采样间隔必须为正")
	case p.LaunchDeadzone < 0:
		return fmt.Errorf("发射端死区不能为负")
	}
	return nil
}

// Trace 是一条 OTDR 轨迹的完整原始记录。
type Trace struct {
	ID          int64       `json:"id"`
	Code        string      `json:"code"` // 稳定编码，如 TR-A
	Name        string      `json:"name"`
	Params      TraceParams `json:"params"`
	SamplePower []float64   `json:"sample_power"` // 线性功率，按采样索引排列
	CreatedRev  int         `json:"created_rev"`  // 导入时的参数修订号
}

// LightSpeedMS 真空中光速（米/秒）。
const LightSpeedMS = 299792458.0

// SampleDistanceM 返回第 idx 个采样点在当前群折射率下的单程距离（米）。
// 折射率改变后距离必须重新调用本函数计算；采样索引本身不携带米制身份。
func SampleDistanceM(params TraceParams, idx int) float64 {
	t := float64(idx) * params.SampleInterval * 1e-9
	return LightSpeedMS * t / params.GroupIndex
}

// DistanceToSampleIndex 把米制距离换算回最近的采样索引。
// 用于折射率修订后，由旧距离找回同一个样本身份。
func DistanceToSampleIndex(params TraceParams, distanceM float64) int {
	t := distanceM * params.GroupIndex / LightSpeedMS
	idx := int(t/(params.SampleInterval*1e-9) + 0.5)
	if idx < 0 {
		return 0
	}
	return idx
}

// SampleDB 把线性功率换算成分贝；非正值钳制到噪声底。
func SampleDB(linear float64) float64 {
	const floor = 1e-12
	if linear <= 0 {
		return 10 * log10(floor)
	}
	if linear < floor {
		linear = floor
	}
	return 10 * log10(linear)
}
