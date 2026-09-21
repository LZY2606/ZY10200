package otdr

const zeroFloorEpsilon = 0.01 // 与零功率钳制底的判定容差（dB）

// rawReflection 是一簇超阈值正残差（一个反射峰）。
type rawReflection struct {
	anchor int     // 峰位（残差最大处）
	start  int     // 簇起点
	end    int     // 簇终点
	spike  float64 // 峰高（相对滚动中值）
}

// detectReflectionClusters 找出死区之外、宽度不小于 minWidth 的超阈值峰簇。
func detectReflectionClusters(db, med, resid []float64, deadIdx, half int, threshold float64) []rawReflection {
	var out []rawReflection
	n := len(db)
	i := deadIdx
	for i < n {
		if resid[i] < threshold {
			i++
			continue
		}
		j := i
		best := i
		for j < n && resid[j] > threshold*0.5 {
			if resid[j] > resid[best] {
				best = j
			}
			j++
		}
		// 簇宽至少为脉宽的一半，过滤孤立毛刺。
		width := j - i
		if width >= maxInt(2, int(float64(half)*0.6)) {
			out = append(out, rawReflection{
				anchor: best,
				start:  i,
				end:    j - 1,
				spike:  resid[best],
			})
		}
		i = j
	}
	return out
}

// rawStep 是一个持续向下的电平台阶。
type rawStep struct {
	anchor int
	loss   float64
}

// detectSteps 在已经过宽窗中值去峰的电平上识别持续向下台阶。
//
// 为避开反射峰残留对中值电平的污染，左右高原取在候选点两侧
// 至少 1 个峰宽之外；台阶位置则由局部最大下降斜率给出。
// 紧邻零功率缺口（截断）的下降不算台阶——截断由末端检测单独处理。
func detectSteps(db, med []float64, blocked map[int]bool, deadIdx, pulseHalf int, threshold float64) []rawStep {
	n := len(med)
	zeroFloor := SampleDB(1e-12)
	w := pulseHalf * 2 // 左右高原半窗（2 个脉宽）
	gap := pulseHalf   // 与候选点的安全间隔（1 个脉宽）
	var dips []rawStep
	for i := deadIdx + w + gap; i+w+gap < n; i++ {
		// 候选点或其高原与反射屏蔽区相交则跳过：
		// 反射峰的中值拖尾不应被解释成熔接台阶。
		if blocked[i] {
			continue
		}
		// 任一侧进入零功率缺口都跳过，防止截断边缘伪造台阶。
		lLo, lHi := i-gap-w, i-gap-1
		rLo, rHi := i+gap+1, i+gap+w
		left := median(med[lLo : lHi+1])
		right := median(med[rLo : rHi+1])
		if left < zeroFloor+2 || right < zeroFloor+2 {
			continue
		}
		hit := false
		for b := lLo; b <= rHi; b++ {
			if blocked[b] {
				hit = true
				break
			}
		}
		if hit {
			continue
		}
		loss := left - right
		if loss > threshold {
			// 定位台阶：在 [i-pulseHalf, i+pulseHalf] 内取原始电平差最大处。
			anchor := i
			best := loss
			for j := i - pulseHalf; j <= i+pulseHalf; j++ {
				if j-gap < 0 || j+gap >= n {
					continue
				}
				l := median(med[j-gap-w/2 : j-gap+1])
				r := median(med[j+gap : j+gap+w/2+1])
				if d := l - r; d > best {
					best, anchor = d, j
				}
			}
			dips = append(dips, rawStep{anchor: anchor, loss: best})
		}
	}
	// 同一台阶附近（1 个脉宽内）只保留幅度最大的候选。
	var out []rawStep
	for k := 0; k < len(dips); k++ {
		best := dips[k]
		// 同一物理台阶在一个“高原比较窗”内只保留幅度最大的候选。
		for k+1 < len(dips) && absInt(dips[k+1].anchor-best.anchor) <= w+gap {
			k++
			if dips[k].loss > best.loss {
				best = dips[k]
			}
		}
		out = append(out, best)
	}
	return out
}
