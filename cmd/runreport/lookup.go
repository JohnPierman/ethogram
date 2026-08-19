package main

// Result-document navigation. Every helper returns ok=false when the value is absent,
// so a missing measurement becomes an explicit "not recorded" line rather than a zero.
// The contracts mirror cmd/partii's helpers of the same names, so the two renderers
// read the result files the same way.

// mapAt returns a nested object, ok=false when any step of the path is absent.
func mapAt(m map[string]any, path ...string) (map[string]any, bool) {
	cur := m
	for _, k := range path {
		next, ok := cur[k].(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

// numAt returns a nested number, ok=false when absent.
func numAt(m map[string]any, path ...string) (float64, bool) {
	if len(path) == 0 {
		return 0, false
	}
	parent, ok := mapAt(m, path[:len(path)-1]...)
	if !ok {
		return 0, false
	}
	v, ok := parent[path[len(path)-1]].(float64)
	return v, ok
}

// strAt returns a nested string, ok=false when absent.
func strAt(m map[string]any, path ...string) (string, bool) {
	if len(path) == 0 {
		return "", false
	}
	parent, ok := mapAt(m, path[:len(path)-1]...)
	if !ok {
		return "", false
	}
	v, ok := parent[path[len(path)-1]].(string)
	return v, ok
}

// boolAt returns a nested boolean, ok=false when absent.
func boolAt(m map[string]any, path ...string) (bool, bool) {
	if len(path) == 0 {
		return false, false
	}
	parent, ok := mapAt(m, path[:len(path)-1]...)
	if !ok {
		return false, false
	}
	v, ok := parent[path[len(path)-1]].(bool)
	return v, ok
}

// listAt returns a nested list, ok=false when absent.
func listAt(m map[string]any, path ...string) ([]any, bool) {
	if len(path) == 0 {
		return nil, false
	}
	parent, ok := mapAt(m, path[:len(path)-1]...)
	if !ok {
		return nil, false
	}
	v, ok := parent[path[len(path)-1]].([]any)
	return v, ok
}

// intervalAt reads a recorded {point, low, high} confidence interval, the shape
// cmd/analyse writes and cmd/partii reads. ok is false when any bound is absent, so
// a partial interval is never rendered as a whole one.
func intervalAt(m map[string]any, key string) (point, low, high float64, ok bool) {
	iv, okMap := mapAt(m, key)
	if !okMap {
		return 0, 0, 0, false
	}
	p, okP := numAt(iv, "point")
	l, okL := numAt(iv, "low")
	h, okH := numAt(iv, "high")
	return p, l, h, okP && okL && okH
}

// stringItems extracts the string members of a recorded list.
func stringItems(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, isStr := it.(string); isStr {
			out = append(out, s)
		}
	}
	return out
}

// scalar renders a JSON leaf value; ok is false for objects, arrays and nulls, whose
// rendering is owned by the section that understands their shape.
func scalar(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case float64:
		return fmtNum(t), true
	case bool:
		if t {
			return "true", true
		}
		return "false", true
	default:
		return "", false
	}
}
