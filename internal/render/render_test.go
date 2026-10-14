package render

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"testing"

	"github.com/satlavida/athvan/internal/devices"
)

func sampleRequest() WallpaperRequest {
	return WallpaperRequest{
		Device:   "iphone-16-pro",
		Timezone: "America/Los_Angeles",
		Now:      "2026-10-14T11:20:00-07:00",
		Title:    "Ship the beta",
		Background: Background{
			Type: "gradient", Color: "#16222e", GradientTo: "#43325a",
		},
		Events: []Event{
			{Title: "Birthday", Start: "2026-10-14"},
			{Title: "Standup", Start: "2026-10-14T08:30:00-07:00", End: "2026-10-14T09:00:00-07:00", Location: "Zoom"},
			{Title: "Design review", Start: "2026-10-14T11:00:00-07:00", End: "2026-10-14T12:00:00-07:00"},
			{Title: "Planning", Start: "2026-10-15T09:00:00-07:00"},
			{Title: "Last week", Start: "2026-10-07T09:00:00-07:00"},
		},
		Reminders: []Reminder{
			{Title: "Renew passport", Due: "2026-10-12", Priority: 2},
			{Title: "Water plants", Done: true},
		},
		Notes: []string{"Wi-Fi: guest / sunflower42"},
	}
}

func TestRenderEveryDevice(t *testing.T) {
	for _, d := range devices.All() {
		for _, orient := range []string{"portrait", "landscape"} {
			req := sampleRequest()
			req.Device, req.Orientation = d.ID, orient
			res, err := Render(context.Background(), req)
			if err != nil {
				t.Fatalf("%s %s: %v", d.ID, orient, err)
			}
			img, err := png.Decode(bytes.NewReader(res.Image))
			if err != nil {
				t.Fatalf("%s: decode: %v", d.ID, err)
			}
			w, h := d.Width, d.Height
			if orient == "landscape" {
				w, h = h, w
			}
			if b := img.Bounds(); b.Dx() != w || b.Dy() != h {
				t.Errorf("%s %s: got %dx%d, want %dx%d", d.ID, orient, b.Dx(), b.Dy(), w, h)
			}
		}
	}
}

func TestPastEventsDropped(t *testing.T) {
	p, err := normalize(sampleRequest())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range p.events {
		if e.title == "Last week" {
			t.Error("event from last week should be dropped")
		}
	}
	if len(p.events) != 4 {
		t.Errorf("got %d events, want 4", len(p.events))
	}
}

func TestInputErrors(t *testing.T) {
	cases := map[string]func(*WallpaperRequest){
		"unknown device": func(r *WallpaperRequest) { r.Device = "nokia-3310" },
		"bad colour":     func(r *WallpaperRequest) { r.Style.TextColor = "#12" },
		"bad zone":       func(r *WallpaperRequest) { r.Timezone = "Mars/Olympus" },
		"bad event time": func(r *WallpaperRequest) { r.Events[0].Start = "tomorrowish" },
		"image w/o src":  func(r *WallpaperRequest) { r.Background = Background{Type: "image"} },
		"bad base64":     func(r *WallpaperRequest) { r.Background = Background{Type: "image", ImageBase64: "!!"} },
	}
	for name, mutate := range cases {
		req := sampleRequest()
		mutate(&req)
		_, err := Render(context.Background(), req)
		var ie *InputError
		if !errors.As(err, &ie) {
			t.Errorf("%s: want InputError, got %v", name, err)
		}
	}
}

func TestPrivateURLBlocked(t *testing.T) {
	req := sampleRequest()
	req.Background = Background{Type: "image", ImageURL: "http://127.0.0.1:1/x.png"}
	_, err := Render(context.Background(), req)
	if !errors.Is(err, errPrivateAddr) {
		t.Errorf("want private address error, got %v", err)
	}
}

func TestOverflowTruncates(t *testing.T) {
	req := sampleRequest()
	for range 80 {
		req.Reminders = append(req.Reminders, Reminder{Title: "filler"})
	}
	if _, err := Render(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkRender(b *testing.B) {
	req := sampleRequest()
	for b.Loop() {
		if _, err := Render(context.Background(), req); err != nil {
			b.Fatal(err)
		}
	}
}
