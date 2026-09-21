package otdr

import "math"

// 固定夹具的物理地面实况（对两条轨迹相同）：
//
//	0 m        发射端（在死区内）
//	300 m      真实反射连接器（插入损耗 + 强反射峰）
//	600 m      幽灵峰（300m 强反射的二次回波，无真实设施）
//	850 m      低损耗熔接
//	1500 m     轨迹截断处（真实光纤可能更长，只能报告长度下界）
const (
	FixtureConnectorM  = 300.0
	FixtureGhostM      = 2 * FixtureConnectorM
	FixtureSpliceM     = 850.0
	FixtureTruncateM   = 1500.0
	FixtureFiberAlpha  = 0.20 // dB/km 光纤衰减斜率
	FixtureConnectorDB = 0.55 // 连接器插入损耗
	FixtureSpliceDB    = 0.45 // 熔接损耗（低损耗但可可靠检出）
)

// rng 是确定性的线性同余噪声源，保证夹具每次生成都一致。
type rng struct{ state uint64 }

func (r *rng) next() float64 {
	r.state = r.state*6364136223846793005 + 1442695040888963407
	// 取高位并映射到 (0,1)
	return float64(r.state>>11) / float64(1<<53)
}

func (r *rng) gaussian() float64 {
	u1 := r.next()
	u2 := r.next()
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}

// fixtureSpec 描述一条合成轨迹的采集配置。
type fixtureSpec struct {
	code       string
	name       string
	pulseNS    float64
	groupIndex float64
	avgCount   int
	sampleNS   float64
	samples    int
	noiseSigma float64
	seed       uint64
}

// FixtureSpecs 返回两条采集参数不同的夹具轨迹。
// 两条轨迹采样间隔不同（因此同一物理事件落在不同采样索引上），
// 群折射率也不同（验收时会再修改并重新对齐）。
func FixtureSpecs() []fixtureSpec {
	return []fixtureSpec{
		{
			code: "TR-A", name: "干线 A（窄脉宽 100ns）",
			pulseNS: 100, groupIndex: 1.4680, avgCount: 16384,
			sampleNS: 5.0, samples: 1600, noiseSigma: 0.015, seed: 0xA11CE,
		},
		{
			code: "TR-B", name: "干线 B（中宽脉宽 150ns）",
			pulseNS: 150, groupIndex: 1.4650, avgCount: 8192,
			sampleNS: 6.0, samples: 1400, noiseSigma: 0.030, seed: 0xB0B22,
		},
	}
}

// GenerateTrace 按规格合成一条夹具轨迹。返回的线性功率是原始采样，
// 只取决于采样索引与物理参数，与任何后续折射率修订无关。
func GenerateTrace(id int64, spec fixtureSpec) Trace {
	params := TraceParams{
		PulseWidthNS:   spec.pulseNS,
		GroupIndex:     spec.groupIndex,
		AvgCount:       spec.avgCount,
		SampleInterval: spec.sampleNS,
		// 默认发射端死区 40m（覆盖发射反射尾瓣），用户可在界面调整。
		LaunchDeadzone: 40,
	}

	r := &rng{state: spec.seed}
	power := make([]float64, spec.samples)

	// 反射峰的时间宽度跟随脉宽：宽脉宽峰更宽。
	spikeSigmaNS := spec.pulseNS * 0.55
	gauss := func(tNS, centerNS, sigmaNS, ampDB float64) float64 {
		d := tNS - centerNS
		return ampDB * math.Exp(-(d*d)/(2*sigmaNS*sigmaNS))
	}

	for i := 0; i < spec.samples; i++ {
		tNS := float64(i) * spec.sampleNS
		// 该采样点的单程距离（仅用于放置物理事件）。
		dist := LightSpeedMS * (tNS * 1e-9) / spec.groupIndex

		if dist > FixtureTruncateM {
			// 截断之后没有任何采集到的回波：保持零功率（检出器据此只报下界）。
			power[i] = 0
			continue
		}

		db := -5.0 - FixtureFiberAlpha*(dist/1000.0)
		if dist >= FixtureConnectorM {
			db -= FixtureConnectorDB // 连接器插入损耗（持久台阶）
		}
		if dist >= FixtureSpliceM {
			db -= FixtureSpliceDB // 熔接损耗（持久台阶）
		}

		// 发射端反射（位于死区内）。
		db += gauss(tNS, 0, spikeSigmaNS, 6.0)
		// 300m 处真实连接器的强反射峰。
		connT := FixtureConnectorM * spec.groupIndex / LightSpeedMS * 1e9
		db += gauss(tNS, connT, spikeSigmaNS, 4.8)
		// 600m 处幽灵峰：同一强反射的二次回波，幅度更低、无台阶。
		ghostT := FixtureGhostM * spec.groupIndex / LightSpeedMS * 1e9
		db += gauss(tNS, ghostT, spikeSigmaNS, 1.15)

		db += r.gaussian() * spec.noiseSigma
		power[i] = math.Pow(10, db/10.0)
	}

	return Trace{
		ID:          id,
		Code:        spec.code,
		Name:        spec.name,
		Params:      params,
		SamplePower: power,
		CreatedRev:  1,
	}
}

// DefaultDeadzoneM 新轨迹默认发射端死区。
const DefaultDeadzoneM = 40.0
