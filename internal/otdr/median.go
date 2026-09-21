package otdr

// medianOf returns the median of a copied sub-slice x[lo:hi+1] using
// quickselect. The source slice is never modified.
func medianOf(x []float64, lo, hi int) float64 {
	buf := make([]float64, hi-lo+1)
	copy(buf, x[lo:hi+1])
	k := len(buf) / 2 // upper median; window lengths are odd
	return quickSelect(buf, k)
}

func quickSelect(a []float64, k int) float64 {
	lo, hi := 0, len(a)-1
	for lo < hi {
		pivot := a[(lo+hi)/2]
		i, j := lo, hi
		for i <= j {
			for a[i] < pivot {
				i++
			}
			for a[j] > pivot {
				j--
			}
			if i <= j {
				a[i], a[j] = a[j], a[i]
				i++
				j--
			}
		}
		if k <= j {
			hi = j
		} else if k >= i {
			lo = i
		} else {
			break
		}
	}
	return a[k]
}

// rollingMedian returns the centered rolling median with the given odd
// window. Edges use the largest centered window that fits, staying causal
// about data (never reading past the acquisition ends).
func rollingMedian(x []float64, window int) []float64 {
	n := len(x)
	out := make([]float64, n)
	if window < 3 {
		window = 3
	}
	if window%2 == 0 {
		window++
	}
	half := window / 2
	for i := 0; i < n; i++ {
		lo := i - half
		hi := i + half
		if lo < 0 {
			lo = 0
		}
		if hi > n-1 {
			hi = n - 1
		}
		out[i] = medianOf(x, lo, hi)
	}
	return out
}
