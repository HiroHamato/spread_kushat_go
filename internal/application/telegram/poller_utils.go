package telegram

import (
	"math"
	"sort"
	"strings"

	"spread_kushat_pora_golang/internal/domain/entity"
)

func normalizeModes(modes []string) []string {
	selected := mapFromSlice(modes)
	out := make([]string, 0, len(modeOptions))
	for _, mode := range modeOptions {
		if _, ok := selected[mode]; ok {
			out = append(out, mode)
		}
	}
	if len(out) > 0 {
		return out
	}
	return []string{"spot-futures"}
}

func modeLabel(modes []string) string {
	labels := map[string]string{"spot-futures": "SPOT-FUTURE", "spot": "SPOT-SPOT", "futures": "FUTURE-FUTURE"}
	norm := normalizeModes(modes)
	parts := make([]string, 0, len(norm))
	for _, m := range norm {
		if label, ok := labels[m]; ok {
			parts = append(parts, label)
		} else {
			parts = append(parts, m)
		}
	}
	return strings.Join(parts, " + ")
}

func normalizeAlertSpikeModes(modes []string) []string {
	selected := mapFromSlice(modes)
	out := make([]string, 0, len(modeOptions))
	for _, mode := range modeOptions {
		if _, ok := selected[mode]; ok {
			out = append(out, mode)
		}
	}
	if len(out) > 0 {
		return out
	}
	return append([]string(nil), modeOptions...)
}

func alertSpikeModesLabel(modes []string) string {
	norm := normalizeAlertSpikeModes(modes)
	if len(norm) == len(modeOptions) {
		return "ALL"
	}
	short := map[string]string{"spot-futures": "SF", "spot": "SS", "futures": "FF"}
	parts := make([]string, 0, len(norm))
	for _, m := range norm {
		if s, ok := short[m]; ok {
			parts = append(parts, s)
		} else {
			parts = append(parts, m)
		}
	}
	return strings.Join(parts, "+")
}

func stateIcon(enabled bool) string {
	if enabled {
		return "✅"
	}
	return "❌"
}

func selectedIcon(selected bool) string {
	if selected {
		return "✅"
	}
	return "▫️"
}

func chunkButtons(items []map[string]string, size int) []any {
	if size <= 0 {
		size = 1
	}
	out := make([]any, 0)
	for i := 0; i < len(items); i += size {
		j := i + size
		if j > len(items) {
			j = len(items)
		}
		out = append(out, items[i:j])
	}
	return out
}

func trackedSlice(tracked map[string]entity.TrackedItem) []entity.TrackedItem {
	out := make([]entity.TrackedItem, 0, len(tracked))
	for _, item := range tracked {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

func mapFromSlice(items []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, item := range items {
		s := strings.ToLower(strings.TrimSpace(item))
		if s == "" {
			continue
		}
		out[s] = struct{}{}
	}
	return out
}

func intersectOrder(order []string, set map[string]struct{}) []string {
	out := make([]string, 0, len(order))
	for _, item := range order {
		if _, ok := set[item]; ok {
			out = append(out, item)
		}
	}
	if len(out) == 0 {
		return []string{order[0]}
	}
	return out
}

func containsFloat(list []float64, value float64) bool {
	for _, v := range list {
		if floatEq(v, value) {
			return true
		}
	}
	return false
}

func containsString(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

func floatEq(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func hasKey(set map[string]struct{}, key string) bool {
	_, ok := set[key]
	return ok
}

func ifZero(v, fallback float64) float64 {
	if v == 0 {
		return fallback
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func ternaryBool(ok bool, yes, no string) string {
	if ok {
		return yes
	}
	return no
}
