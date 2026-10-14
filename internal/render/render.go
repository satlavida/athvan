// Package render turns a WallpaperRequest into a lock-screen wallpaper image.
package render

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/disintegration/imaging"
	"github.com/fogleman/gg"
	"golang.org/x/image/font"

	"github.com/satlavida/athvan/internal/devices"
)

// InputError is a problem with the request itself (HTTP 422).
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

func inputErr(format string, a ...any) error { return &InputError{fmt.Sprintf(format, a...)} }

// UpstreamError is a failure fetching a remote image (HTTP 502).
type UpstreamError struct{ Err error }

func (e *UpstreamError) Error() string { return e.Err.Error() }
func (e *UpstreamError) Unwrap() error { return e.Err }

// Result is a rendered wallpaper.
type Result struct {
	Image       []byte
	ContentType string
	Width       int
	Height      int
	Device      string
	BingCredit  string
}

// Render draws the wallpaper described by req.
func Render(ctx context.Context, req WallpaperRequest) (*Result, error) {
	p, err := normalize(req)
	if err != nil {
		return nil, err
	}
	base, credit, err := background(ctx, p.req.Background, p.w, p.h, p.scale)
	if err != nil {
		return nil, err
	}
	r := &renderer{plan: p, fc: faceCache{}}
	defer r.fc.close()
	img := r.draw(base, credit)

	var buf bytes.Buffer
	res := &Result{Width: p.w, Height: p.h, Device: p.device, BingCredit: credit}
	if p.req.Format == "jpeg" {
		res.ContentType = "image/jpeg"
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: p.req.Quality})
	} else {
		res.ContentType = "image/png"
		err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img)
	}
	if err != nil {
		return nil, err
	}
	res.Image = buf.Bytes()
	return res, nil
}

// ---------------------------------------------------------------------------
// Normalisation

type plan struct {
	req                 WallpaperRequest
	device              string
	w, h                int
	scale               float64
	safeTop, safeBottom float64
	loc                 *time.Location
	now                 time.Time

	text, secondary, accent, alert, tint, border color.NRGBA

	events    []evt
	reminders []rem
}

type evt struct {
	title, location, calendar string
	start, end                time.Time
	allDay                    bool
	day                       time.Time // the day it is listed under
	color                     color.NRGBA
}

type rem struct {
	title, list   string
	due           time.Time
	dueIsDate     bool
	done, flagged bool
	priority      int
}

func def[T comparable](v *T, d T) {
	var zero T
	if *v == zero {
		*v = d
	}
}

func normalize(req WallpaperRequest) (*plan, error) {
	p := &plan{req: req}
	r := &p.req

	def(&r.Device, devices.Default)
	dev, ok := devices.Get(r.Device)
	if !ok {
		return nil, inputErr("unknown device %q; see GET /v1/devices", r.Device)
	}
	p.device, p.w, p.h, p.scale = dev.ID, dev.Width, dev.Height, dev.Scale
	p.safeTop, p.safeBottom = dev.SafeTop, dev.SafeBottom
	if r.Width > 0 || r.Height > 0 {
		if r.Width < 200 || r.Height < 200 {
			return nil, inputErr("width and height must both be set, at least 200px each")
		}
		p.device, p.w, p.h = "custom", r.Width, r.Height
		p.scale = r.Scale
		if p.scale == 0 {
			p.scale = 3
		}
	}
	if r.Orientation == "landscape" && p.w < p.h {
		p.w, p.h = p.h, p.w
	}

	def(&r.Format, "png")
	def(&r.Quality, 92)

	bg := &r.Background
	def(&bg.Type, "color")
	def(&bg.Color, "#101418")
	def(&bg.GradientTo, "#2a3a5c")
	def(&bg.GradientAngle, 160)
	def(&bg.BingMarket, "en-US")
	def(&bg.FocusX, 0.5)

	st := &r.Style
	def(&st.TextColor, "#ffffff")
	def(&st.AccentColor, "#ff9f0a")
	def(&st.AlertColor, "#ff453a")
	def(&st.TextStyle, "glass")
	def(&st.GlassTint, "#0000004d")
	def(&st.GlassBlur, 30)
	def(&st.GlassRadius, 22)
	def(&st.GlassBorder, "#ffffff2e")
	def(&st.FontScale, 1)
	def(&st.Density, "compact")
	def(&st.TimeFormat, "12h")

	ly := &r.Layout
	def(&ly.Top, p.safeTop)
	def(&ly.Bottom, p.safeBottom)
	def(&ly.Margin, 16)
	def(&ly.Align, "top")
	if ly.Top+ly.Bottom > 0.95 {
		return nil, inputErr("layout.top + layout.bottom leaves no room for content")
	}
	if len(r.Sections) == 0 {
		r.Sections = []string{"events", "reminders", "notes"}
	}

	colors := []struct {
		name string
		src  string
		dst  *color.NRGBA
	}{
		{"background.color", bg.Color, nil},
		{"background.gradient_to", bg.GradientTo, nil},
		{"style.text_color", st.TextColor, &p.text},
		{"style.accent_color", st.AccentColor, &p.accent},
		{"style.alert_color", st.AlertColor, &p.alert},
		{"style.glass_tint", st.GlassTint, &p.tint},
		{"style.glass_border", st.GlassBorder, &p.border},
	}
	for _, c := range colors {
		v, err := parseColor(c.src)
		if err != nil {
			return nil, inputErr("%s: %v", c.name, err)
		}
		if c.dst != nil {
			*c.dst = v
		}
	}
	if st.SecondaryColor == "" {
		a := 0.68
		if st.TextStyle != "glass" {
			a = 0.85 // no card behind it, so it needs the extra contrast
		}
		p.secondary = withAlpha(p.text, a)
	} else {
		v, err := parseColor(st.SecondaryColor)
		if err != nil {
			return nil, inputErr("style.secondary_color: %v", err)
		}
		p.secondary = v
	}

	p.loc = time.Local
	if r.Timezone != "" {
		loc, err := time.LoadLocation(r.Timezone)
		if err != nil {
			return nil, inputErr("timezone: unknown zone %q", r.Timezone)
		}
		p.loc = loc
	}
	p.now = time.Now().In(p.loc)
	if r.Now != "" {
		t, err := time.Parse(time.RFC3339, r.Now)
		if err != nil {
			return nil, inputErr("now: want RFC 3339, e.g. 2026-10-14T08:00:00-07:00")
		}
		p.now = t.In(p.loc)
	}

	if err := p.parseEvents(); err != nil {
		return nil, err
	}
	if err := p.parseReminders(); err != nil {
		return nil, err
	}
	return p, nil
}

func dayStart(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// parseWhen accepts RFC 3339, a zone-less local date-time, or a plain date.
func parseWhen(s string, loc *time.Location) (t time.Time, dateOnly bool, err error) {
	s = strings.TrimSpace(s)
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		return t, true, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.In(loc), false, nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, false, nil
		}
	}
	return time.Time{}, false, fmt.Errorf("cannot parse %q; use RFC 3339 or YYYY-MM-DD", s)
}

func (p *plan) parseEvents() error {
	today := dayStart(p.now)
	for i, e := range p.req.Events {
		start, dateOnly, err := parseWhen(e.Start, p.loc)
		if err != nil {
			return inputErr("events[%d].start: %v", i, err)
		}
		ev := evt{title: e.Title, location: e.Location, calendar: e.Calendar, start: start, allDay: e.AllDay || dateOnly, color: p.accent}
		if ev.allDay {
			ev.start = dayStart(start)
		}
		switch {
		case e.End != "":
			end, _, err := parseWhen(e.End, p.loc)
			if err != nil {
				return inputErr("events[%d].end: %v", i, err)
			}
			ev.end = end
		case ev.allDay:
			ev.end = ev.start.AddDate(0, 0, 1)
		default:
			ev.end = ev.start
		}
		if e.Color != "" {
			c, err := parseColor(e.Color)
			if err != nil {
				return inputErr("events[%d].color: %v", i, err)
			}
			ev.color = c
		}
		// Ended before today: not worth wall space.
		if ev.end.Before(today) || (ev.allDay && !ev.end.After(today)) {
			continue
		}
		ev.day = dayStart(ev.start)
		if ev.day.Before(today) {
			ev.day = today // multi-day event still running
		}
		p.events = append(p.events, ev)
	}
	sort.SliceStable(p.events, func(i, j int) bool {
		a, b := p.events[i], p.events[j]
		if !a.day.Equal(b.day) {
			return a.day.Before(b.day)
		}
		if a.allDay != b.allDay {
			return a.allDay
		}
		return a.start.Before(b.start)
	})
	return nil
}

func (p *plan) parseReminders() error {
	for i, r := range p.req.Reminders {
		rm := rem{title: r.Title, list: r.List, done: r.Done, flagged: r.Flagged, priority: r.Priority}
		if r.Due != "" {
			t, dateOnly, err := parseWhen(r.Due, p.loc)
			if err != nil {
				return inputErr("reminders[%d].due: %v", i, err)
			}
			rm.due, rm.dueIsDate = t, dateOnly
		}
		p.reminders = append(p.reminders, rm)
	}
	// Open items first; otherwise keep the caller's order.
	sort.SliceStable(p.reminders, func(i, j int) bool {
		return !p.reminders[i].done && p.reminders[j].done
	})
	return nil
}

// ---------------------------------------------------------------------------
// Layout model

type row struct {
	h    float64
	draw func(dc *gg.Context, x, y, w float64)
}

type item struct {
	rows   []row
	header bool // a day heading; dropped if nothing follows it
	past   bool // already over: the first thing to go when space runs out
}

type section struct {
	items  []item
	hidden int
	more   func(n int) row
}

func (s *section) rows() []row {
	var out []row
	for _, it := range s.items {
		out = append(out, it.rows...)
	}
	if s.hidden > 0 {
		out = append(out, s.more(s.hidden))
	}
	return out
}

func (s *section) height(m *metrics) float64 {
	rs := s.rows()
	h := 2 * m.padY
	for i, r := range rs {
		h += r.h
		if i > 0 {
			h += m.rowGap
		}
	}
	return h
}

// dropOne removes an item to save space: a finished event if there is
// one, otherwise the last item. Headings left without items go too.
// It reports false when there is nothing left to drop.
func (s *section) dropOne() bool {
	victim := -1
	for i, it := range s.items {
		if it.past {
			victim = i
			break
		}
	}
	if victim < 0 {
		for i := len(s.items) - 1; i >= 0; i-- {
			if !s.items[i].header {
				victim = i
				break
			}
		}
	}
	if victim < 0 {
		return false
	}
	s.items = append(s.items[:victim], s.items[victim+1:]...)
	s.hidden++
	kept := s.items[:0]
	for i, it := range s.items {
		if it.header && (i+1 == len(s.items) || s.items[i+1].header) {
			continue
		}
		kept = append(kept, it)
	}
	s.items = kept
	return true
}

func (s *section) visible() int {
	n := 0
	for _, it := range s.items {
		if !it.header {
			n++
		}
	}
	return n
}

type metrics struct {
	pt                                       float64 // pixels per point
	title, subtitle, header, item, meta      float64 // font sizes in px
	titleLH, subLH, headerLH, itemLH, metaLH float64
	padX, padY, rowGap, cardGap, radius      float64
	comfortable                              bool
}

func (p *plan) metrics() *metrics {
	s := p.scale
	f := p.req.Style.FontScale * s
	m := &metrics{pt: s, comfortable: p.req.Style.Density == "comfortable"}
	if m.comfortable {
		m.title, m.subtitle, m.header, m.item, m.meta = 34*f, 17*f, 14*f, 17*f, 14*f
		m.padY, m.rowGap, m.cardGap = 14*s, 4*s, 12*s
	} else {
		m.title, m.subtitle, m.header, m.item, m.meta = 30*f, 16*f, 13*f, 15*f, 13*f
		m.padY, m.rowGap, m.cardGap = 11*s, 2*s, 10*s
	}
	m.titleLH, m.subLH = m.title*1.2, m.subtitle*1.35
	m.headerLH, m.itemLH, m.metaLH = m.header*1.55, m.item*1.4, m.meta*1.35
	m.padX = 14 * s
	if p.req.Style.TextStyle != "glass" {
		m.padX, m.padY = 2*s, 4*s
	}
	m.radius = p.req.Style.GlassRadius * s
	return m
}

// ---------------------------------------------------------------------------
// Drawing

type renderer struct {
	*plan
	fc faceCache
	m  *metrics
}

func (r *renderer) draw(base *image.NRGBA, credit string) image.Image {
	r.m = r.metrics()
	m := r.m
	W, H := float64(r.w), float64(r.h)
	ly := r.req.Layout

	contentW := W - 2*ly.Margin*m.pt
	if ly.Width > 0 {
		contentW = math.Min(contentW, ly.Width*m.pt)
	}
	x0 := (W - contentW) / 2
	top := H * ly.Top
	bottom := H * (1 - ly.Bottom)

	free := gg.NewContext(r.w, r.h) // title, caption: always shadowed unless plain
	if r.req.Background.Type == "bing" && r.req.Background.BingCaption && credit != "" {
		f := r.fc.get(regular, m.meta*0.85)
		lh := m.meta * 1.3
		bottom -= lh + 4*m.pt
		line := ellipsize(f, credit, contentW)
		drawText(free, f, line, (W-measure(f, line))/2, bottom+4*m.pt, lh, r.secondary)
	}

	// Title block spans all columns.
	var titleRows []row
	if r.req.Title != "" {
		f := r.fc.get(bold, m.title)
		for _, l := range wrap(f, r.req.Title, contentW, 2) {
			titleRows = append(titleRows, r.textRow(f, l, m.titleLH, r.text))
		}
	}
	if r.req.Subtitle != "" {
		f := r.fc.get(medium, m.subtitle)
		for _, l := range wrap(f, r.req.Subtitle, contentW, 2) {
			titleRows = append(titleRows, r.textRow(f, l, m.subLH, r.secondary))
		}
	}
	titleH := 0.0
	for _, tr := range titleRows {
		titleH += tr.h
	}
	if titleH > 0 {
		titleH += m.cardGap * 1.2
	}

	cols := ly.Columns
	if cols == 0 {
		cols = 1
		if contentW/m.pt >= 600 {
			cols = 2
		}
	}
	colW := (contentW - m.cardGap*float64(cols-1)) / float64(cols)
	inner := colW - 2*m.padX

	var secs []*section
	for _, name := range r.req.Sections {
		var s *section
		switch name {
		case "events":
			s = r.eventsSection()
		case "reminders":
			s = r.remindersSection()
		case "notes":
			s = r.notesSection(inner)
		}
		if s != nil && len(s.items) > 0 {
			secs = append(secs, s)
		}
	}

	// Greedy column packing: each card goes to the shortest column.
	columns := make([][]*section, cols)
	colH := func(c []*section) float64 {
		h := 0.0
		for i, s := range c {
			if i > 0 {
				h += m.cardGap
			}
			h += s.height(m)
		}
		return h
	}
	for _, s := range secs {
		best := 0
		for i := range columns {
			if colH(columns[i]) < colH(columns[best]) {
				best = i
			}
		}
		columns[best] = append(columns[best], s)
	}

	// Trim the tallest column until everything fits.
	avail := bottom - top - titleH
	for {
		tallest, th := -1, avail
		for i, c := range columns {
			if h := colH(c); h > th {
				tallest, th = i, h
			}
		}
		if tallest < 0 {
			break
		}
		var victim *section
		for _, s := range columns[tallest] {
			if victim == nil || s.visible() > victim.visible() {
				victim = s
			}
		}
		if victim == nil || !victim.dropOne() {
			break
		}
		if victim.visible() == 0 {
			// Nothing left but "+N more": drop the card entirely.
			c := columns[tallest]
			for i, s := range c {
				if s == victim {
					columns[tallest] = append(c[:i], c[i+1:]...)
					break
				}
			}
		}
	}

	blockH := titleH
	maxCol := 0.0
	for _, c := range columns {
		maxCol = math.Max(maxCol, colH(c))
	}
	blockH += maxCol
	y0 := top
	switch ly.Align {
	case "center":
		y0 = top + math.Max(0, (bottom-top-blockH)/2)
	case "bottom":
		y0 = math.Max(top, bottom-blockH)
	}

	y := y0
	for _, tr := range titleRows {
		tr.draw(free, x0, y, contentW)
		y += tr.h
	}
	if titleH > 0 {
		y += m.cardGap * 1.2
	}

	out := image.NewRGBA(image.Rect(0, 0, r.w, r.h))
	draw.Draw(out, out.Bounds(), base, image.Point{}, draw.Src)
	dc := gg.NewContextForRGBA(out) // draws straight into out
	glass := r.req.Style.TextStyle == "glass"
	cards := free
	var frost *image.NRGBA
	if glass {
		cards = gg.NewContext(r.w, r.h)
		frost = fastBlur(base, r.req.Style.GlassBlur*m.pt)
	}
	for ci, c := range columns {
		cx := x0 + float64(ci)*(colW+m.cardGap)
		cy := y
		for _, s := range c {
			h := s.height(m)
			if glass {
				r.glassCard(dc, out, frost, cx, cy, colW, h)
			}
			ry := cy + m.padY
			for i, rw := range s.rows() {
				if i > 0 {
					ry += m.rowGap
				}
				rw.draw(cards, cx+m.padX, ry, inner)
				ry += rw.h
			}
			cy += h + m.cardGap
		}
	}

	// gg.DrawImage resamples through a generic kernel; plain compositing
	// with image/draw is an order of magnitude faster.
	freeImg := free.Image().(*image.RGBA)
	if r.req.Style.TextStyle != "plain" {
		sh := softShadow(freeImg, m.pt, haloFor(r.text))
		off := int(math.Round(m.pt))
		draw.Draw(out, out.Bounds(), sh, image.Pt(0, -off), draw.Over)
	}
	draw.Draw(out, out.Bounds(), freeImg, image.Point{}, draw.Over)
	if glass {
		draw.Draw(out, out.Bounds(), cards.Image(), image.Point{}, draw.Over)
	}
	return out
}

func (r *renderer) glassCard(dc *gg.Context, out *image.RGBA, frost *image.NRGBA, x, y, w, h float64) {
	x, y, w, h = math.Round(x), math.Round(y), math.Round(w), math.Round(h)
	rad := math.Min(r.m.radius, h/2)
	mask := gg.NewContext(int(w), int(h))
	mask.DrawRoundedRectangle(0, 0, w, h, rad)
	mask.Fill()
	rect := image.Rect(int(x), int(y), int(x+w), int(y+h))
	draw.DrawMask(out, rect, frost, rect.Min, mask.Image(), image.Point{}, draw.Over)

	// Pull the frost towards its average colour, as iOS materials do, so a
	// high-contrast photo does not show through as blotches.
	mean := meanColor(frost, rect)
	mean.A = 77
	dc.DrawRoundedRectangle(x, y, w, h, rad)
	dc.SetColor(mean)
	dc.Fill()

	dc.DrawRoundedRectangle(x, y, w, h, rad)
	dc.SetColor(r.tint)
	dc.Fill()
	if r.border.A > 0 {
		lw := math.Max(1, r.m.pt/2)
		dc.DrawRoundedRectangle(x+lw/2, y+lw/2, w-lw, h-lw, rad)
		dc.SetLineWidth(lw)
		dc.SetColor(r.border)
		dc.Stroke()
	}
}

// haloFor picks a shadow colour that contrasts with the text: black behind
// light text, white behind dark text.
func haloFor(text color.NRGBA) color.NRGBA {
	lum := 0.2126*float64(text.R) + 0.7152*float64(text.G) + 0.0722*float64(text.B)
	if lum < 128 {
		return color.NRGBA{255, 255, 255, 255}
	}
	return color.NRGBA{0, 0, 0, 255}
}

func meanColor(img *image.NRGBA, r image.Rectangle) color.NRGBA {
	r = r.Intersect(img.Rect)
	var sr, sg, sb, n uint64
	for y := r.Min.Y; y < r.Max.Y; y += 4 {
		for x := r.Min.X; x < r.Max.X; x += 4 {
			i := img.PixOffset(x, y)
			sr += uint64(img.Pix[i])
			sg += uint64(img.Pix[i+1])
			sb += uint64(img.Pix[i+2])
			n++
		}
	}
	if n == 0 {
		return color.NRGBA{A: 255}
	}
	return color.NRGBA{uint8(sr / n), uint8(sg / n), uint8(sb / n), 255}
}

// softShadow builds a blurred halo from the layer's alpha.
func softShadow(layer *image.RGBA, pt float64, halo color.NRGBA) *image.NRGBA {
	b := layer.Bounds()
	sh := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for i := 3; i < len(layer.Pix); i += 4 {
		sh.Pix[i] = layer.Pix[i]
	}
	// A tight dark halo for edge contrast plus a wide soft one for busy photos.
	tight := imaging.Blur(sh, 0.9*pt)
	wide := fastBlur(sh, 4*pt)
	out := image.NewNRGBA(sh.Rect)
	for i := 3; i < len(out.Pix); i += 4 {
		a := 1 - (1-float64(tight.Pix[i])/255*0.85)*(1-float64(wide.Pix[i])/255*0.55)
		out.Pix[i-3], out.Pix[i-2], out.Pix[i-1] = halo.R, halo.G, halo.B
		out.Pix[i] = uint8(math.Min(255, a*255*1.3))
	}
	return out
}

// ---------------------------------------------------------------------------
// Sections

func (r *renderer) textRow(f font.Face, s string, h float64, c color.Color) row {
	return row{h: h, draw: func(dc *gg.Context, x, y, w float64) {
		drawText(dc, f, ellipsize(f, s, w), x, y, h, c)
	}}
}

func (r *renderer) headerRow(left, right string) row {
	m := r.m
	f := r.fc.get(semibold, m.header)
	return row{h: m.headerLH, draw: func(dc *gg.Context, x, y, w float64) {
		rw := 0.0
		if right != "" {
			rw = measure(f, right)
			drawText(dc, f, right, x+w-rw, y, m.headerLH, r.secondary)
			rw += 8 * m.pt
		}
		drawText(dc, f, ellipsize(f, left, w-rw), x, y, m.headerLH, r.text)
	}}
}

func (r *renderer) moreRow(n int) row {
	f := r.fc.get(medium, r.m.meta)
	return r.textRow(f, fmt.Sprintf("+%d more", n), r.m.metaLH, r.secondary)
}

func (r *renderer) fmtTime(t time.Time) string {
	if r.req.Style.TimeFormat == "24h" {
		return t.Format("15:04")
	}
	return t.Format("3:04 PM")
}

func (r *renderer) eventsSection() *section {
	if len(r.events) == 0 {
		return nil
	}
	m := r.m
	today := dayStart(r.now)
	s := &section{more: r.moreRow}

	dayLabel := func(d time.Time) (string, string) {
		switch {
		case d.Equal(today):
			return "Today", d.Format("Mon, Jan 2")
		case d.Equal(today.AddDate(0, 0, 1)):
			return "Tomorrow", d.Format("Mon, Jan 2")
		default:
			return d.Format("Monday"), d.Format("Jan 2")
		}
	}

	if !r.events[0].day.Equal(today) {
		l, rt := dayLabel(today)
		s.items = append(s.items,
			item{header: true, rows: []row{r.headerRow(l, rt)}},
			item{rows: []row{r.textRow(r.fc.get(medium, m.meta), "No more events", m.metaLH, r.secondary)}})
	}
	var last time.Time
	for _, e := range r.events {
		if !e.day.Equal(last) {
			l, rt := dayLabel(e.day)
			s.items = append(s.items, item{header: true, rows: []row{r.headerRow(l, rt)}})
			last = e.day
		}
		s.items = append(s.items, item{rows: r.eventRows(e), past: !e.allDay && !e.end.After(r.now) && e.start.Before(r.now)})
	}
	return s
}

func (r *renderer) eventRows(e evt) []row {
	m := r.m
	titleF := r.fc.get(medium, m.item)
	timeF := r.fc.get(medium, m.meta)
	metaF := r.fc.get(regular, m.meta)

	sample := "10:00 PM"
	if r.req.Style.TimeFormat == "24h" {
		sample = "00:00"
	}
	timeW := math.Max(measure(timeF, sample), measure(timeF, "all-day"))
	barW, gap := 3*m.pt, 8*m.pt
	textX := barW + gap + timeW + gap

	past := !e.allDay && !e.end.After(r.now) && !e.end.Equal(e.start)
	past = past || (!e.allDay && e.end.Equal(e.start) && e.start.Before(r.now))
	live := !e.allDay && !e.start.After(r.now) && e.end.After(r.now)
	alpha := 1.0
	if past {
		alpha = 0.45
	}
	label := "all-day"
	if !e.allDay {
		label = r.fmtTime(e.start)
	}
	timeColor := withAlpha(r.secondary, alpha)
	if live {
		timeColor = r.accent
	}

	rows := []row{{h: m.itemLH, draw: func(dc *gg.Context, x, y, w float64) {
		dc.DrawRoundedRectangle(x, y+m.itemLH*0.16, barW, m.itemLH*0.68, barW/2)
		dc.SetColor(withAlpha(e.color, alpha))
		dc.Fill()
		drawText(dc, timeF, label, x+barW+gap, y, m.itemLH, timeColor)
		avail := w - textX
		title := ellipsize(titleF, e.title, avail)
		drawText(dc, titleF, title, x+textX, y, m.itemLH, withAlpha(r.text, alpha))
		if !m.comfortable && e.location != "" && title == e.title {
			tw := measure(titleF, title)
			loc := ellipsize(metaF, "  "+e.location, avail-tw)
			// A stub like "Ro…" is noise; show it only if most of it fits.
			if kept := len([]rune(strings.TrimSpace(strings.TrimSuffix(loc, "…")))); kept >= 8 || loc == "  "+e.location {
				drawText(dc, metaF, loc, x+textX+tw, y, m.itemLH, withAlpha(r.secondary, alpha))
			}
		}
	}}}

	if m.comfortable {
		var parts []string
		if !e.allDay && e.end.After(e.start) {
			parts = append(parts, r.fmtTime(e.start)+" – "+r.fmtTime(e.end))
		}
		if e.location != "" {
			parts = append(parts, e.location)
		}
		if e.calendar != "" {
			parts = append(parts, e.calendar)
		}
		if len(parts) > 0 {
			line := strings.Join(parts, " · ")
			rows = append(rows, row{h: m.metaLH, draw: func(dc *gg.Context, x, y, w float64) {
				drawText(dc, metaF, ellipsize(metaF, line, w-textX), x+textX, y, m.metaLH, withAlpha(r.secondary, alpha))
			}})
		}
	}
	return rows
}

func (r *renderer) remindersSection() *section {
	if len(r.reminders) == 0 {
		return nil
	}
	open := 0
	for _, rm := range r.reminders {
		if !rm.done {
			open++
		}
	}
	s := &section{more: r.moreRow}
	s.items = append(s.items, item{header: true, rows: []row{r.headerRow("Reminders", fmt.Sprintf("%d open", open))}})
	for _, rm := range r.reminders {
		s.items = append(s.items, item{rows: r.reminderRows(rm)})
	}
	return s
}

func (r *renderer) dueLabel(rm rem) (string, bool) {
	if rm.due.IsZero() {
		return "", false
	}
	today := dayStart(r.now)
	d := dayStart(rm.due)
	overdue := !rm.done && ((rm.dueIsDate && d.Before(today)) || (!rm.dueIsDate && rm.due.Before(r.now)))
	switch {
	case d.Equal(today):
		if rm.dueIsDate {
			return "Today", overdue
		}
		return r.fmtTime(rm.due), overdue
	case d.Equal(today.AddDate(0, 0, 1)):
		return "Tomorrow", overdue
	case d.After(today) && d.Before(today.AddDate(0, 0, 7)):
		return rm.due.Format("Mon"), overdue
	case d.Year() == today.Year():
		return rm.due.Format("Jan 2"), overdue
	default:
		return rm.due.Format("Jan 2, 2006"), overdue
	}
}

func (r *renderer) reminderRows(rm rem) []row {
	m := r.m
	titleF := r.fc.get(medium, m.item)
	boldF := r.fc.get(bold, m.item)
	metaF := r.fc.get(medium, m.meta)
	due, overdue := r.dueLabel(rm)

	ring := r.secondary
	switch {
	case rm.done:
	case overdue:
		ring = r.alert
	case rm.priority > 0 || rm.flagged:
		ring = r.accent
	}
	rad := m.item * 0.36
	gap := 10 * m.pt
	textX := 2*rad + gap

	rows := []row{{h: m.itemLH, draw: func(dc *gg.Context, x, y, w float64) {
		cx, cy := x+rad, y+m.itemLH/2
		lw := 1.6 * m.pt
		if rm.done {
			dc.DrawCircle(cx, cy, rad)
			dc.SetColor(withAlpha(r.secondary, 0.8))
			dc.Fill()
			dc.MoveTo(cx-rad*0.45, cy+rad*0.02)
			dc.LineTo(cx-rad*0.1, cy+rad*0.38)
			dc.LineTo(cx+rad*0.48, cy-rad*0.36)
			dc.SetLineWidth(lw)
			dc.SetLineCap(gg.LineCapRound)
			dc.SetLineJoin(gg.LineJoinRound)
			dc.SetColor(color.NRGBA{0, 0, 0, 170})
			dc.Stroke()
		} else {
			dc.DrawCircle(cx, cy, rad-lw/2)
			dc.SetLineWidth(lw)
			dc.SetColor(ring)
			dc.Stroke()
		}

		avail := w - textX
		if due != "" {
			dw := measure(metaF, due)
			c := r.secondary
			if overdue {
				c = r.alert
			}
			drawText(dc, metaF, due, x+w-dw, y, m.itemLH, c)
			avail -= dw + 8*m.pt
		}
		tx := x + textX
		if rm.priority > 0 && !rm.done {
			bang := strings.Repeat("!", rm.priority) + " "
			drawText(dc, boldF, bang, tx, y, m.itemLH, r.accent)
			bw := measure(boldF, bang)
			tx += bw
			avail -= bw
		}
		tc := r.text
		if rm.done {
			tc = withAlpha(r.secondary, 0.75)
		}
		title := ellipsize(titleF, rm.title, avail)
		drawText(dc, titleF, title, tx, y, m.itemLH, tc)
		if rm.done {
			tw := measure(titleF, title)
			ly := y + m.itemLH/2
			dc.DrawLine(tx, ly, tx+tw, ly)
			dc.SetLineWidth(math.Max(1, m.pt*0.8))
			dc.SetColor(tc)
			dc.Stroke()
		}
	}}}

	if m.comfortable && rm.list != "" {
		rows = append(rows, row{h: m.metaLH, draw: func(dc *gg.Context, x, y, w float64) {
			f := r.fc.get(regular, m.meta)
			drawText(dc, f, ellipsize(f, rm.list, w-textX), x+textX, y, m.metaLH, r.secondary)
		}})
	}
	return rows
}

func (r *renderer) notesSection(w float64) *section {
	var notes []string
	for _, n := range r.req.Notes {
		if strings.TrimSpace(n) != "" {
			notes = append(notes, n)
		}
	}
	if len(notes) == 0 {
		return nil
	}
	m := r.m
	f := r.fc.get(regular, m.item)
	s := &section{more: r.moreRow}
	s.items = append(s.items, item{header: true, rows: []row{r.headerRow("Notes", "")}})
	for _, n := range notes {
		var rows []row
		for _, l := range wrap(f, n, w, 6) {
			rows = append(rows, r.textRow(f, l, m.itemLH, r.text))
		}
		s.items = append(s.items, item{rows: rows})
	}
	return s
}

// ---------------------------------------------------------------------------
// Text helpers

func measure(f font.Face, s string) float64 {
	return float64(font.MeasureString(f, s)) / 64
}

func drawText(dc *gg.Context, f font.Face, s string, x, y, h float64, c color.Color) {
	if s == "" {
		return
	}
	met := f.Metrics()
	asc, desc := float64(met.Ascent)/64, float64(met.Descent)/64
	dc.SetFontFace(f)
	dc.SetColor(c)
	dc.DrawString(s, x, y+(h-(asc+desc))/2+asc)
}

func ellipsize(f font.Face, s string, w float64) string {
	if measure(f, s) <= w {
		return s
	}
	rs := []rune(s)
	lo, hi := 0, len(rs)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if measure(f, strings.TrimRight(string(rs[:mid]), " ")+"…") <= w {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo == 0 {
		return ""
	}
	return strings.TrimRight(string(rs[:lo]), " ") + "…"
}

// wrap breaks s into at most maxLines lines no wider than w, ellipsizing
// the last line if text remains.
func wrap(f font.Face, s string, w float64, maxLines int) []string {
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		cur := ""
		for _, word := range words {
			try := word
			if cur != "" {
				try = cur + " " + word
			}
			if measure(f, try) <= w || cur == "" {
				cur = try
				continue
			}
			lines = append(lines, cur)
			cur = word
		}
		lines = append(lines, cur)
	}
	if len(lines) > maxLines {
		rest := strings.Join(lines[maxLines-1:], " ")
		lines = append(lines[:maxLines-1], ellipsize(f, rest, w))
	}
	for i, l := range lines {
		lines[i] = ellipsize(f, l, w) // a single over-long word
	}
	return lines
}
