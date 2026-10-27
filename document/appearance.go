package document

// Theme contains portable design tokens. Renderers never interpret tokens as CSS
// source, URLs, scripts or remote font instructions.
type Theme struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Mode        string        `json:"mode"`
	Accent      string        `json:"accent"`
	Font        string        `json:"font"`
	Density     string        `json:"density"`
	Width       string        `json:"width"`
	Radius      int           `json:"radius"`
	Light       *ThemePalette `json:"light,omitempty"`
	Dark        *ThemePalette `json:"dark,omitempty"`
}

// ThemePalette optionally overrides individual colors in a theme's light or
// dark appearance. Every supplied value is a six digit hexadecimal color.
type ThemePalette struct {
	Background string `json:"background,omitempty"`
	Surface    string `json:"surface,omitempty"`
	Text       string `json:"text,omitempty"`
	Muted      string `json:"muted,omitempty"`
	Border     string `json:"border,omitempty"`
	Accent     string `json:"accent,omitempty"`
}

type NavigationSettings struct {
	ShowTOC          bool `json:"showTOC"`
	ShowPreviousNext bool `json:"showPreviousNext"`
	StickyHeader     bool `json:"stickyHeader"`
}

type EditorSettings struct {
	DefaultFormat string `json:"defaultFormat"`
	PreviewOnSave bool   `json:"previewOnSave"`
}

type APISettings struct {
	Enabled          bool `json:"enabled"`
	ShowCurl         bool `json:"showCurl"`
	RequestTimeoutMs int  `json:"requestTimeoutMs"`
	MaxResponseBytes int  `json:"maxResponseBytes"`
}

type PublishingSettings struct {
	AutoPublish      bool `json:"autoPublish"`
	GitPushOnPublish bool `json:"gitPushOnPublish"`
}
