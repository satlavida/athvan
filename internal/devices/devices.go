// Package devices holds the catalogue of supported screen presets.
package devices

import "sort"

// Device is a wallpaper target. Width and Height are the native portrait
// resolution in pixels; Scale converts design points to pixels.
type Device struct {
	ID     string  `json:"id" doc:"Preset identifier used in requests" example:"iphone-16-pro"`
	Name   string  `json:"name" doc:"Human readable model names" example:"iPhone 16 Pro, iPhone 17, iPhone 17 Pro"`
	Family string  `json:"family" enum:"iphone,ipad" doc:"Device family"`
	Width  int     `json:"width" doc:"Native portrait width in pixels" example:"1206"`
	Height int     `json:"height" doc:"Native portrait height in pixels" example:"2622"`
	Scale  float64 `json:"scale" doc:"Pixels per point" example:"3"`
	// Fractions of the screen height kept free for the lock-screen clock
	// and the bottom controls (flashlight/camera, home indicator).
	SafeTop    float64 `json:"safe_top" doc:"Default fraction of height reserved at the top for the lock-screen clock" example:"0.33"`
	SafeBottom float64 `json:"safe_bottom" doc:"Default fraction of height reserved at the bottom for lock-screen controls" example:"0.13"`
}

const (
	phoneTop    = 0.33
	phoneBottom = 0.13
	padTop      = 0.27
	padBottom   = 0.08
)

func phone(id, name string, w, h int, scale float64) Device {
	return Device{id, name, "iphone", w, h, scale, phoneTop, phoneBottom}
}

func pad(id, name string, w, h int) Device {
	return Device{id, name, "ipad", w, h, 2, padTop, padBottom}
}

var all = []Device{
	// iPhone
	phone("iphone-se", "iPhone SE (2nd/3rd gen), iPhone 6/7/8", 750, 1334, 2),
	phone("iphone-8-plus", "iPhone 6/7/8 Plus", 1080, 1920, 1080.0/414),
	phone("iphone-11", "iPhone XR, iPhone 11", 828, 1792, 2),
	phone("iphone-11-pro", "iPhone X, XS, 11 Pro", 1125, 2436, 3),
	phone("iphone-11-pro-max", "iPhone XS Max, 11 Pro Max", 1242, 2688, 3),
	phone("iphone-13-mini", "iPhone 12 mini, 13 mini", 1080, 2340, 1080.0/375),
	phone("iphone-14", "iPhone 12, 12 Pro, 13, 13 Pro, 14, 16e", 1170, 2532, 3),
	phone("iphone-14-plus", "iPhone 12 Pro Max, 13 Pro Max, 14 Plus", 1284, 2778, 3),
	phone("iphone-15", "iPhone 14 Pro, 15, 15 Pro, 16", 1179, 2556, 3),
	phone("iphone-15-plus", "iPhone 14 Pro Max, 15 Plus, 15 Pro Max, 16 Plus", 1290, 2796, 3),
	phone("iphone-16-pro", "iPhone 16 Pro, 17, 17 Pro", 1206, 2622, 3),
	phone("iphone-16-pro-max", "iPhone 16 Pro Max, 17 Pro Max", 1320, 2868, 3),
	phone("iphone-air", "iPhone Air", 1260, 2736, 3),

	// iPad
	pad("ipad-mini", "iPad mini (6th gen, A17 Pro)", 1488, 2266),
	pad("ipad-10-2", "iPad 10.2\" (7th-9th gen)", 1620, 2160),
	pad("ipad-10-9", "iPad (10th gen, A16), iPad Air 10.9\"/11\"", 1640, 2360),
	pad("ipad-pro-11", "iPad Pro 11\" (1st-4th gen)", 1668, 2388),
	pad("ipad-pro-11-m4", "iPad Pro 11\" (M4, M5)", 1668, 2420),
	pad("ipad-pro-12-9", "iPad Pro 12.9\", iPad Air 13\"", 2048, 2732),
	pad("ipad-pro-13-m4", "iPad Pro 13\" (M4, M5)", 2064, 2752),
}

var byID = func() map[string]Device {
	m := make(map[string]Device, len(all))
	for _, d := range all {
		m[d.ID] = d
	}
	return m
}()

// Default is the preset used when a request names no device.
const Default = "iphone-16-pro"

// All returns every preset, iPhones first then iPads, each by size.
func All() []Device {
	out := append([]Device(nil), all...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Family != out[j].Family {
			return out[i].Family == "iphone"
		}
		return out[i].Width*out[i].Height < out[j].Width*out[j].Height
	})
	return out
}

// Get looks up a preset by id.
func Get(id string) (Device, bool) {
	d, ok := byID[id]
	return d, ok
}

// IDs returns the preset ids, for schema enums.
func IDs() []string {
	ids := make([]string, 0, len(all))
	for _, d := range All() {
		ids = append(ids, d.ID)
	}
	return ids
}
