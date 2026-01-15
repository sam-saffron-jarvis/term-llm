package ui

// ThemePreset represents a predefined color theme
type ThemePreset struct {
	Name        string
	Description string
	Config      ThemeConfig
}

// PresetThemeNames defines the display order of themes
var PresetThemeNames = []string{
	"gruvbox",
	"dracula",
	"nord",
	"solarized",
	"monokai",
	"classic",
}

// PresetThemes contains all predefined themes
var PresetThemes = map[string]ThemePreset{
	"classic": {
		Name:        "classic",
		Description: "Classic green terminal style",
		Config: ThemeConfig{
			Primary:   "10",  // bright green
			Secondary: "4",   // blue
			Success:   "10",  // bright green
			Error:     "9",   // bright red
			Warning:   "11",  // yellow
			Muted:     "245", // light grey
			Text:      "15",  // white
			Spinner:   "205", // pink/magenta
		},
	},
	"dracula": {
		Name:        "dracula",
		Description: "Popular dark theme with purple accents",
		Config: ThemeConfig{
			Primary:   "#bd93f9", // purple
			Secondary: "#8be9fd", // cyan
			Success:   "#50fa7b", // green
			Error:     "#ff5555", // red
			Warning:   "#f1fa8c", // yellow
			Muted:     "#6272a4", // comment grey
			Text:      "#f8f8f2", // foreground
			Spinner:   "#ff79c6", // pink
		},
	},
	"nord": {
		Name:        "nord",
		Description: "Arctic, north-bluish color palette",
		Config: ThemeConfig{
			Primary:   "#88c0d0", // frost cyan
			Secondary: "#81a1c1", // frost blue
			Success:   "#a3be8c", // aurora green
			Error:     "#bf616a", // aurora red
			Warning:   "#ebcb8b", // aurora yellow
			Muted:     "#4c566a", // polar night
			Text:      "#eceff4", // snow storm
			Spinner:   "#b48ead", // aurora purple
		},
	},
	"solarized": {
		Name:        "solarized",
		Description: "Precision colors for machines and people",
		Config: ThemeConfig{
			Primary:   "#268bd2", // blue
			Secondary: "#2aa198", // cyan
			Success:   "#859900", // green
			Error:     "#dc322f", // red
			Warning:   "#b58900", // yellow
			Muted:     "#586e75", // base01
			Text:      "#839496", // base0
			Spinner:   "#d33682", // magenta
		},
	},
	"monokai": {
		Name:        "monokai",
		Description: "Vibrant colors inspired by Sublime Text",
		Config: ThemeConfig{
			Primary:   "#a6e22e", // green
			Secondary: "#66d9ef", // cyan
			Success:   "#a6e22e", // green
			Error:     "#f92672", // red/pink
			Warning:   "#e6db74", // yellow
			Muted:     "#75715e", // comment
			Text:      "#f8f8f2", // foreground
			Spinner:   "#ae81ff", // purple
		},
	},
	"gruvbox": {
		Name:        "gruvbox",
		Description: "Retro groove color scheme (default)",
		Config: ThemeConfig{
			Primary:   "#b8bb26", // green
			Secondary: "#83a598", // aqua
			Success:   "#b8bb26", // green
			Error:     "#fb4934", // red
			Warning:   "#fabd2f", // yellow
			Muted:     "#928374", // gray
			Text:      "#ebdbb2", // foreground
			Spinner:   "#d3869b", // purple
		},
	},
}

// GetPresetTheme returns a preset by name, or nil if not found
func GetPresetTheme(name string) *ThemePreset {
	if preset, ok := PresetThemes[name]; ok {
		return &preset
	}
	return nil
}

// MatchPresetTheme finds a preset that matches the given config, or returns empty string
func MatchPresetTheme(cfg ThemeConfig) string {
	for name, preset := range PresetThemes {
		if themesMatch(cfg, preset.Config) {
			return name
		}
	}
	return ""
}

func themesMatch(a, b ThemeConfig) bool {
	return a.Primary == b.Primary &&
		a.Secondary == b.Secondary &&
		a.Success == b.Success &&
		a.Error == b.Error &&
		a.Warning == b.Warning &&
		a.Muted == b.Muted &&
		a.Text == b.Text &&
		a.Spinner == b.Spinner
}
