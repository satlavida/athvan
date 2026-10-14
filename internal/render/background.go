package render

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"strings"

	"github.com/disintegration/imaging"
	_ "golang.org/x/image/webp"
)

// background paints the base layer. It returns the Bing credit line when a
// Bing photo was used.
func background(ctx context.Context, bg Background, w, h int, scale float64) (*image.NRGBA, string, error) {
	var (
		out    *image.NRGBA
		credit string
	)
	switch bg.Type {
	case "image", "bing":
		var src image.Image
		var err error
		if bg.Type == "bing" {
			var meta BingImage
			src, meta, err = bingImage(ctx, bg.BingMarket, bg.BingIndex)
			if err != nil {
				err = &UpstreamError{err}
			}
			credit = meta.Copyright
		} else {
			src, err = userImage(ctx, bg)
		}
		if err != nil {
			return nil, "", err
		}
		out = cover(src, w, h, bg.FocusX)
	case "gradient":
		c1, err := parseColor(bg.Color)
		if err != nil {
			return nil, "", err
		}
		c2, err := parseColor(bg.GradientTo)
		if err != nil {
			return nil, "", err
		}
		out = linearGradient(w, h, bg.GradientAngle, c1, c2)
	default:
		c, err := parseColor(bg.Color)
		if err != nil {
			return nil, "", err
		}
		c.A = 255
		out = imaging.New(w, h, c)
	}

	if bg.Blur > 0 {
		out = fastBlur(out, bg.Blur*scale)
	}
	if bg.Dim > 0 {
		dimmed := imaging.New(w, h, image.Black)
		out = imaging.Overlay(out, dimmed, image.Point{}, bg.Dim)
	}
	return out, credit, nil
}

func userImage(ctx context.Context, bg Background) (image.Image, error) {
	var data []byte
	switch {
	case bg.ImageBase64 != "":
		s := bg.ImageBase64
		if i := strings.Index(s, ","); strings.HasPrefix(s, "data:") && i > 0 {
			s = s[i+1:]
		}
		var err error
		data, err = base64.StdEncoding.DecodeString(strings.TrimSpace(s))
		if err != nil {
			return nil, inputErr("image_base64: %v", err)
		}
	case bg.ImageURL != "":
		var err error
		data, err = fetch(ctx, bg.ImageURL)
		if err != nil {
			return nil, &UpstreamError{fmt.Errorf("image_url: %w", err)}
		}
	default:
		return nil, inputErr("background.type is image but neither image_url nor image_base64 is set")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, inputErr("unsupported image (want JPEG, PNG, GIF or WebP): %v", err)
	}
	if cfg.Width*cfg.Height > 80_000_000 {
		return nil, inputErr("image too large: %dx%d", cfg.Width, cfg.Height)
	}
	img, err := imaging.Decode(bytes.NewReader(data), imaging.AutoOrientation(true))
	if err != nil {
		return nil, inputErr("decode image: %v", err)
	}
	return img, nil
}

// cover scales src to fill w×h and crops the overflow. focusX picks which
// part of a too-wide image survives (0 = left, 0.5 = centre, 1 = right).
func cover(src image.Image, w, h int, focusX float64) *image.NRGBA {
	sb := src.Bounds()
	s := math.Max(float64(w)/float64(sb.Dx()), float64(h)/float64(sb.Dy()))
	rw := int(math.Ceil(float64(sb.Dx()) * s))
	rh := int(math.Ceil(float64(sb.Dy()) * s))
	resized := imaging.Resize(src, rw, rh, imaging.Lanczos)
	x := int(math.Round(float64(rw-w) * focusX))
	y := (rh - h) / 2
	return imaging.Crop(resized, image.Rect(x, y, x+w, y+h))
}

// fastBlur approximates a large Gaussian blur by blurring a downscaled copy.
func fastBlur(img *image.NRGBA, sigma float64) *image.NRGBA {
	if sigma < 0.5 {
		return img
	}
	b := img.Bounds()
	f := 1.0
	if sigma > 6 {
		f = math.Min(sigma/3, 4) // beyond 4x the upscale shows blotches
	}
	if f == 1 {
		return imaging.Blur(img, sigma)
	}
	sw := max(1, int(float64(b.Dx())/f))
	sh := max(1, int(float64(b.Dy())/f))
	small := imaging.Resize(img, sw, sh, imaging.Linear)
	small = imaging.Blur(small, sigma/f)
	return imaging.Resize(small, b.Dx(), b.Dy(), imaging.Linear)
}

func toNRGBA(img image.Image) *image.NRGBA {
	if n, ok := img.(*image.NRGBA); ok {
		return n
	}
	b := img.Bounds()
	n := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(n, n.Bounds(), img, b.Min, draw.Src)
	return n
}

// linearGradient fills w×h like CSS linear-gradient(angle, c1, c2).
func linearGradient(w, h int, angle float64, c1, c2 color.NRGBA) *image.NRGBA {
	// 0° points up, 90° points right.
	rad := angle * math.Pi / 180
	dx, dy := math.Sin(rad), -math.Cos(rad)
	half := (math.Abs(float64(w)*dx) + math.Abs(float64(h)*dy)) / 2
	cx, cy := float64(w)/2, float64(h)/2
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	lerp := func(a, b uint8, t float64) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t + 0.5) }
	for y := 0; y < h; y++ {
		row := img.Pix[y*img.Stride:]
		for x := 0; x < w; x++ {
			t := ((float64(x)-cx)*dx + (float64(y)-cy)*dy) / (2 * half)
			t = math.Min(1, math.Max(0, t+0.5))
			i := x * 4
			row[i] = lerp(c1.R, c2.R, t)
			row[i+1] = lerp(c1.G, c2.G, t)
			row[i+2] = lerp(c1.B, c2.B, t)
			row[i+3] = lerp(c1.A, c2.A, t)
		}
	}
	return img
}
