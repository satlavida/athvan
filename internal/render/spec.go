package render

// WallpaperRequest describes one wallpaper. Every field is optional; an empty
// request renders a plain dark wallpaper for the default device.
type WallpaperRequest struct {
	Device      string  `json:"device,omitempty" default:"iphone-16-pro" doc:"Device preset id. See GET /v1/devices." example:"iphone-16-pro"`
	Orientation string  `json:"orientation,omitempty" enum:"portrait,landscape" default:"portrait" doc:"Landscape swaps width and height; useful for iPad."`
	Width       int     `json:"width,omitempty" minimum:"0" maximum:"6000" doc:"Custom width in pixels. Overrides the device preset when set together with height."`
	Height      int     `json:"height,omitempty" minimum:"0" maximum:"6000" doc:"Custom height in pixels."`
	Scale       float64 `json:"scale,omitempty" minimum:"0" maximum:"4" doc:"Pixels per point for a custom size (default 3)."`
	Format      string  `json:"format,omitempty" enum:"png,jpeg" default:"png" doc:"Output image format."`
	Quality     int     `json:"quality,omitempty" minimum:"0" maximum:"100" default:"92" doc:"JPEG quality."`

	Timezone string `json:"timezone,omitempty" doc:"IANA zone used to display times and group events by day. Defaults to the server's zone." example:"America/Los_Angeles"`
	Now      string `json:"now,omitempty" format:"date-time" doc:"Reference time (RFC 3339) for Today/Tomorrow labels and overdue checks. Defaults to the current time."`

	Title     string     `json:"title,omitempty" maxLength:"200" doc:"Optional large heading shown above the cards." example:"Focus: ship v1"`
	Subtitle  string     `json:"subtitle,omitempty" maxLength:"300" doc:"Optional line under the title."`
	Events    []Event    `json:"events,omitempty" maxItems:"200" doc:"Calendar events. Past days are dropped; events are grouped by day."`
	Reminders []Reminder `json:"reminders,omitempty" maxItems:"200" doc:"Reminders / to-dos."`
	Notes     []string   `json:"notes,omitempty" maxItems:"50" doc:"Free text lines, word-wrapped."`
	Sections  []string   `json:"sections,omitempty" enum:"events,reminders,notes" doc:"Order of the cards. Defaults to events, reminders, notes."`

	Background Background `json:"background,omitzero" required:"false"`
	Style      Style      `json:"style,omitzero" required:"false"`
	Layout     Layout     `json:"layout,omitzero" required:"false"`
}

// Background picks what sits behind the text.
type Background struct {
	Type          string  `json:"type,omitempty" enum:"color,gradient,image,bing" default:"color" doc:"color: solid fill. gradient: two-colour linear gradient. image: image_url or image_base64. bing: Bing image of the day."`
	Color         string  `json:"color,omitempty" pattern:"^#([0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$" default:"#101418" doc:"Background colour, and gradient start colour." example:"#101418"`
	GradientTo    string  `json:"gradient_to,omitempty" pattern:"^#([0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$" default:"#2a3a5c" doc:"Gradient end colour."`
	GradientAngle float64 `json:"gradient_angle,omitempty" minimum:"0" maximum:"360" default:"160" doc:"Gradient direction in degrees (0 = upwards, 90 = to the right)."`
	ImageURL      string  `json:"image_url,omitempty" format:"uri" doc:"http(s) URL of a JPEG/PNG/GIF image. Cover-cropped to the screen."`
	ImageBase64   string  `json:"image_base64,omitempty" doc:"Base64 image data (a data: URL prefix is accepted). Takes precedence over image_url."`
	BingMarket    string  `json:"bing_market,omitempty" default:"en-US" doc:"Bing market, e.g. en-US, en-GB, ja-JP, de-DE." example:"en-US"`
	BingIndex     int     `json:"bing_index,omitempty" minimum:"0" maximum:"7" doc:"0 = today, 1 = yesterday, up to 7 days back."`
	BingCaption   bool    `json:"bing_caption,omitempty" doc:"Print the Bing photo credit in small text above the bottom safe area."`
	Blur          float64 `json:"blur,omitempty" minimum:"0" maximum:"100" doc:"Gaussian blur over the whole background, in points."`
	Dim           float64 `json:"dim,omitempty" minimum:"0" maximum:"1" doc:"Darken the background (0 = none, 1 = black). Helps contrast on busy photos."`
	FocusX        float64 `json:"focus_x,omitempty" minimum:"0" maximum:"1" default:"0.5" doc:"Horizontal crop anchor for images (0 = left edge, 1 = right edge)."`
}

// Style controls colours and type.
type Style struct {
	TextColor      string  `json:"text_color,omitempty" pattern:"^#([0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$" default:"#ffffff" doc:"Primary text colour."`
	SecondaryColor string  `json:"secondary_color,omitempty" pattern:"^#([0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$" doc:"Times, headings and meta text. Defaults to text_color at 65% opacity."`
	AccentColor    string  `json:"accent_color,omitempty" pattern:"^#([0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$" default:"#ff9f0a" doc:"In-progress events, flags, priorities."`
	AlertColor     string  `json:"alert_color,omitempty" pattern:"^#([0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$" default:"#ff453a" doc:"Overdue reminders."`
	TextStyle      string  `json:"text_style,omitempty" enum:"glass,shadow,plain" default:"glass" doc:"glass: frosted cards behind each section. shadow: no cards, soft drop shadow. plain: text only."`
	GlassTint      string  `json:"glass_tint,omitempty" pattern:"^#([0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$" default:"#0000004d" doc:"Colour laid over the frosted area. Use alpha (#RRGGBBAA)."`
	GlassBlur      float64 `json:"glass_blur,omitempty" minimum:"0" maximum:"80" default:"30" doc:"Frost strength, in points."`
	GlassRadius    float64 `json:"glass_radius,omitempty" minimum:"0" maximum:"60" default:"22" doc:"Card corner radius, in points."`
	GlassBorder    string  `json:"glass_border,omitempty" pattern:"^#([0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$" default:"#ffffff2e" doc:"Hairline border colour. Fully transparent disables it."`
	FontScale      float64 `json:"font_scale,omitempty" minimum:"0.5" maximum:"2.5" default:"1" doc:"Multiplier on every text size."`
	Density        string  `json:"density,omitempty" enum:"compact,comfortable" default:"compact" doc:"compact: one line per item, the most information per glance. comfortable: larger type with locations and lists on a second line."`
	TimeFormat     string  `json:"time_format,omitempty" enum:"12h,24h" default:"12h" doc:"Clock format for times."`
}

// Layout positions the content block on screen.
type Layout struct {
	Top     float64 `json:"top,omitempty" minimum:"0" maximum:"0.9" doc:"Fraction of height kept clear at the top. 0 uses the device default, which clears the lock-screen clock."`
	Bottom  float64 `json:"bottom,omitempty" minimum:"0" maximum:"0.9" doc:"Fraction of height kept clear at the bottom. 0 uses the device default."`
	Margin  float64 `json:"margin,omitempty" minimum:"0" maximum:"200" default:"16" doc:"Side margin, in points."`
	Align   string  `json:"align,omitempty" enum:"top,center,bottom" default:"top" doc:"Vertical placement of the content inside the free area."`
	Columns int     `json:"columns,omitempty" minimum:"0" maximum:"3" doc:"Card columns. 0 picks automatically: 1 on iPhone, 2 when the screen is at least 600 points wide."`
	Width   float64 `json:"width,omitempty" minimum:"0" maximum:"2000" doc:"Maximum content width in points; the block is centred. 0 = full width."`
}

// Event is a calendar entry.
type Event struct {
	Title    string `json:"title" minLength:"1" maxLength:"300" example:"Design review"`
	Start    string `json:"start" doc:"RFC 3339 date-time, or YYYY-MM-DD for all-day events." example:"2026-10-14T10:00:00-07:00"`
	End      string `json:"end,omitempty" doc:"RFC 3339 date-time, or YYYY-MM-DD."`
	AllDay   bool   `json:"all_day,omitempty" doc:"All-day event. Implied when start is a plain date."`
	Location string `json:"location,omitempty" maxLength:"300" example:"Room 4B"`
	Calendar string `json:"calendar,omitempty" maxLength:"100" doc:"Calendar name, shown in comfortable density."`
	Color    string `json:"color,omitempty" pattern:"^#([0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$" doc:"Calendar colour for the leading bar. Defaults to the accent colour." example:"#0a84ff"`
}

// Reminder is a to-do item.
type Reminder struct {
	Title    string `json:"title" minLength:"1" maxLength:"300" example:"Call the dentist"`
	Due      string `json:"due,omitempty" doc:"RFC 3339 date-time, or YYYY-MM-DD." example:"2026-10-14T17:00:00-07:00"`
	Done     bool   `json:"done,omitempty" doc:"Completed reminders are drawn struck through and sorted last."`
	Priority int    `json:"priority,omitempty" minimum:"0" maximum:"3" doc:"0 none, 1 low (!), 2 medium (!!), 3 high (!!!)."`
	Flagged  bool   `json:"flagged,omitempty"`
	List     string `json:"list,omitempty" maxLength:"100" doc:"List name, shown in comfortable density."`
}
