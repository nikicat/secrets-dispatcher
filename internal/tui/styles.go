package tui

import "github.com/charmbracelet/lipgloss"

// List pane (left pane) styles.
var (
	// listPaneStyle is the outer border/container for the request list pane.
	listPaneStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), true).
			BorderForeground(lipgloss.Color("240"))

	// selectedItemStyle highlights the currently focused list item.
	selectedItemStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("15")).
				Background(lipgloss.Color("62"))

	// normalItemStyle is the default style for unselected list items.
	normalItemStyle = lipgloss.NewStyle()

	// dimmedStyle is used for resolved/history items (greyed out).
	dimmedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	// historySectionStyle labels the history divider.
	historySectionStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("240")).
				Italic(true)
)

// Badge styles for request type indicators.
var (
	// secretBadgeStyle styles [SECRET] badges in green.
	secretBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("46")).
				Bold(true)

	// signBadgeStyle styles [SIGN] badges in purple/violet.
	signBadgeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("135")).
			Bold(true)

	// searchBadgeStyle styles [SEARCH] badges in cyan.
	searchBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("51")).
				Bold(true)
)

// Countdown timer styles (applied based on remaining time).
var (
	// countdownNormalStyle for timers with > 60 seconds remaining.
	countdownNormalStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("248"))

	// countdownWarnStyle for timers with 30–60 seconds remaining.
	countdownWarnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))

	// countdownUrgentStyle for timers with < 30 seconds remaining.
	countdownUrgentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

// Detail pane (right pane) styles.
var (
	// detailPaneStyle is the outer border/container for the detail pane.
	detailPaneStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), true).
			BorderForeground(lipgloss.Color("240"))

	// headerStyle is used for section headers in the detail view.
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("75")).
			Underline(true)

	// keyStyle labels individual fields in the detail view (bold, dimmed).
	keyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("244"))

	// valueStyle renders field values in the detail view.
	valueStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))

	// statusBarStyle renders the bottom status bar.
	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Italic(true)

	// lockedStyle highlights the status bar when VT is locked.
	lockedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)

	// idleTitleStyle is used for the idle state heading in the detail pane.
	idleTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("248"))
)
