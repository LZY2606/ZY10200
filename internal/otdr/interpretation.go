package otdr

// Kind 标识事件候选的类别。
type Kind string

const (
	KindReflection Kind = "reflection" // 反射事件（连接器等）
	KindSplice     Kind = "splice"     // 非反射熔接/台阶损耗
	KindGhost      Kind = "ghost"      // 幽灵候选
	KindTruncation Kind = "truncation" // 轨迹截断：只有长度下界
	KindFiberEnd   Kind = "fiber_end"  // 自然光纤末端
)

// Event 是一次解释中检出的单个事件。
// 身份由 TraceID + AnchorIndex 决定；距离只是当前折射率下的快照属性。
type Event struct {
	AnchorIndex int     `json:"anchor_index"` // 锚点采样索引（身份）
	StartIndex  int     `json:"start_index"`  // 事件窗起点（含）
	EndIndex    int     `json:"end_index"`    // 事件窗终点（含）
	DistanceM   float64 `json:"distance_m"`   // 锚点距离（当前折射率）
	StartM      float64 `json:"start_m"`
	EndM        float64 `json:"end_m"`
	Kind        Kind    `json:"kind"`
	LossDB      float64 `json:"loss_db"`      // 该事件造成的台阶损耗
	SpikeDB     float64 `json:"spike_db"`     // 反射峰相对局部基线的高度
	ParentIndex int     `json:"parent_index"` // 幽灵候选所引用的前导强反射锚点；无则 -1
	ParentDistM float64 `json:"parent_dist_m"`
	Ratio       float64 `json:"ghost_ratio"` // d_ghost / d_parent，用于证据
	Evidence    string  `json:"evidence"`    // 人类可读的证据
}

// Anchor 是分段基线的一个拐点（锚点），位于两个拟合段之间。
type Anchor struct {
	Index     int     `json:"index"`
	DistanceM float64 `json:"distance_m"`
}

// Interpretation 是某条轨迹在给定参数与参数修订号下的一次解释快照。
type Interpretation struct {
	TraceID     int64     `json:"trace_id"`
	TraceCode   string    `json:"trace_code"`
	Rev         int       `json:"rev"`         // 所依据的参数修订号
	GroupIndex  float64   `json:"group_index"` // 本次解释使用的折射率
	DeadzoneM   float64   `json:"deadzone_m"`
	SampleCount int       `json:"sample_count"`
	DB          []float64 `json:"db"`       // 对数功率（每采样）
	Baseline    []float64 `json:"baseline"` // 分段基线（每采样）
	Events      []Event   `json:"events"`
	Anchors     []Anchor  `json:"anchors"`
	CumLoss     []float64 `json:"cumulative_loss"` // 累计事件损耗（每采样）
	TotalLossDB float64   `json:"total_loss_db"`   // 并集后事件损耗合计（不重复计损）
	LowerBoundM float64   `json:"lower_bound_m"`   // 截断时的光纤长度下界；0 表示无下界声明
	LastM       float64   `json:"last_m"`          // 最后一个采集样本的距离
}

// EventByAnchor 按锚点采样索引查找事件，找不到返回 nil。
func (it *Interpretation) EventByAnchor(idx int) *Event {
	for i := range it.Events {
		if it.Events[i].AnchorIndex == idx {
			return &it.Events[i]
		}
	}
	return nil
}
