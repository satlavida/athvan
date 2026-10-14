package render

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"
)

// parseColor accepts #RGB, #RGBA, #RRGGBB and #RRGGBBAA.
func parseColor(s string) (color.NRGBA, error) {
	h := strings.TrimPrefix(strings.TrimSpace(s), "#")
	switch len(h) {
	case 3, 4:
		var b strings.Builder
		for _, c := range h {
			b.WriteRune(c)
			b.WriteRune(c)
		}
		h = b.String()
	}
	if len(h) == 6 {
		h += "ff"
	}
	if len(h) != 8 {
		return color.NRGBA{}, fmt.Errorf("invalid colour %q: want #RGB, #RGBA, #RRGGBB or #RRGGBBAA", s)
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return color.NRGBA{}, fmt.Errorf("invalid colour %q", s)
	}
	return color.NRGBA{uint8(v >> 24), uint8(v >> 16), uint8(v >> 8), uint8(v)}, nil
}

func withAlpha(c color.NRGBA, a float64) color.NRGBA {
	c.A = uint8(float64(c.A) * a)
	return c
}
