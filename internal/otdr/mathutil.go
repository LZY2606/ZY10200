package otdr

import (
	"math"
	"sort"
)

func log10(x float64) float64 { return math.Log10(x) }

// median 取已复制切片的中值，不修改入参。
func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := append([]float64(nil), xs...)
	// 插入排序对 OTDR 的小窗口足够快，且确定性强。
	for i := 1; i < len(cp); i++ {
		v := cp[i]
		j := i - 1
		for j >= 0 && cp[j] > v {
			cp[j+1] = cp[j]
			j--
		}
		cp[j+1] = v
	}
	return cp[len(cp)/2]
}

// movingMedian 返回居中滚动中值序列；边界处缩短窗口。
func movingMedian(xs []float64, half int) []float64 {
	out := make([]float64, len(xs))
	for i := range xs {
		lo, hi := i-half, i+half
		if lo < 0 {
			lo = 0
		}
		if hi >= len(xs) {
			hi = len(xs) - 1
		}
		out[i] = median(xs[lo : hi+1])
	}
	return out
}

// boxcar 居中矩形平滑，窗口宽度为 2*half+1。
func boxcar(xs []float64, half int) []float64 {
	out := make([]float64, len(xs))
	var sum float64
	for i := range xs {
		lo, hi := i-half, i+half
		if lo < 0 {
			lo = 0
		}
		if hi >= len(xs) {
			hi = len(xs) - 1
		}
		sum = 0
		for j := lo; j <= hi; j++ {
			sum += xs[j]
		}
		out[i] = sum / float64(hi-lo+1)
	}
	return out
}

// linfit 对给定索引区间做普通最小二乘 y = a + b*x。
func linfit(y []float64, lo, hi int) (intercept, slope float64) {
	n := float64(hi - lo + 1)
	var sx, sy, sxx, sxy float64
	for i := lo; i <= hi; i++ {
		x := float64(i)
		sx += x
		sy += y[i]
		sxx += x * x
		sxy += x * y[i]
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return sy / n, 0
	}
	slope = (n*sxy - sx*sy) / den
	intercept = (sy - slope*sx) / n
	return intercept, slope
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// plateauLevel 估计区间 [lo,hi] 在电平 level 上、外推到 anchor 索引处的
// 均值电平。先用稳健 OLS（剔除 3σ 外离群点）估计局部斜率并把各点平移到
// anchor，再返回截尾均值；既修正前向光纤斜率，又避免中值对截断噪声的偏置。
func plateauLevel(level []float64, lo, hi, anchor int) float64 {
	n := hi - lo + 1
	a, b := linfit(level, lo, hi)
	// 一轮 3σ 截尾后重拟合。
	var xs, ys []float64
	for i := lo; i <= hi; i++ {
		r := level[i] - (a + b*float64(i))
		if r < 0 {
			r = -r
		}
		if r > 3*medianAbsResid(level, lo, hi, a, b) {
			continue
		}
		xs = append(xs, float64(i))
		ys = append(ys, level[i])
	}
	if len(xs) >= 4 {
		var sw, sx, sy, sxx, sxy float64
		for i := range xs {
			sw++
			sx += xs[i]
			sy += ys[i]
			sxx += xs[i] * xs[i]
			sxy += xs[i] * ys[i]
		}
		if d := sw*sxx - sx*sx; d != 0 {
			b = (sw*sxy - sx*sy) / d
			a = (sy - b*sx) / sw
		}
	}
	// 把残差（已去掉斜率）平均，得到 anchor 处的稳健电平。
	resid := make([]float64, 0, n)
	for i := lo; i <= hi; i++ {
		resid = append(resid, level[i]-(a+b*float64(i)))
	}
	sort.Float64s(resid)
	// 截尾 10%，抑制残余毛刺。
	cut := n / 10
	if cut*2 >= len(resid) {
		cut = 0
	}
	sum := 0.0
	for _, r := range resid[cut : len(resid)-cut] {
		sum += r
	}
	return a + b*float64(anchor) + sum/float64(len(resid)-2*cut)
}

func medianAbsResid(y []float64, lo, hi int, a, b float64) float64 {
	var rs []float64
	for i := lo; i <= hi; i++ {
		r := y[i] - (a + b*float64(i))
		if r < 0 {
			r = -r
		}
		rs = append(rs, r)
	}
	m := median(rs)
	if m < 1e-9 {
		return 1e-9
	}
	return m
}
