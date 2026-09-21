package web

import "net/url"

// firstSeg 取出 /<root>/<id>/... 中的 id 段。
func firstSeg(path, root string) string {
	want := "/" + root + "/"
	i := indexOf(path, want)
	if i < 0 {
		return ""
	}
	rest := path[i+len(want):]
	for j := 0; j < len(rest); j++ {
		if rest[j] == '/' {
			return rest[:j]
		}
	}
	return rest
}

func lastSeg(path string) string {
	i := len(path) - 1
	for i >= 0 && path[i] != '/' {
		i--
	}
	if i < 0 {
		return path
	}
	return path[i+1:]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func urlQuery(s string) string {
	return url.QueryEscape(s)
}
