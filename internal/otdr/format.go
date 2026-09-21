package otdr

import "fmt"

func formatM(m float64) string  { return fmt.Sprintf("%.1fm", m) }
func formatDB(d float64) string { return fmt.Sprintf("%.2fdB", d) }
func formatRatio(r float64) string {
	return fmt.Sprintf("%.2f", r)
}

// EventLabel 返回事件的中文短标签。
func EventLabel(k Kind) string {
	switch k {
	case KindReflection:
		return "反射连接器"
	case KindSplice:
		return "熔接"
	case KindGhost:
		return "幽灵候选"
	case KindTruncation:
		return "轨迹截断（长度下界）"
	case KindFiberEnd:
		return "光纤末端"
	default:
		return string(k)
	}
}
