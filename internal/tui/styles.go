package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorAmber  = lipgloss.AdaptiveColor{Light: "#7c4a03", Dark: "#ffd27f"}
	colorAmberBg = lipgloss.AdaptiveColor{Light: "#fff3cd", Dark: "#3b2a12"}
	colorBlue   = lipgloss.AdaptiveColor{Light: "#0b5394", Dark: "#9fc5ff"}
	colorBlueBg  = lipgloss.AdaptiveColor{Light: "#dbeafe", Dark: "#14263d"}
	colorGray   = lipgloss.AdaptiveColor{Light: "#495057", Dark: "#b8c0cc"}
	colorGrayBg  = lipgloss.AdaptiveColor{Light: "#e9ecef", Dark: "#232830"}
	colorMuted  = lipgloss.AdaptiveColor{Light: "#868e96", Dark: "#6c757d"}

	colorP0 = lipgloss.AdaptiveColor{Light: "#dc3545", Dark: "#ff6b6b"}
	colorP1 = lipgloss.AdaptiveColor{Light: "#fd7e14", Dark: "#ffa94d"}
	colorP2 = lipgloss.AdaptiveColor{Light: "#198754", Dark: "#69db7c"}
	colorP3 = lipgloss.AdaptiveColor{Light: "#868e96", Dark: "#6c757d"}

	styleID = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#4263eb", Dark: "#74c0fc"})

	styleSectionInProgress = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorAmber).
				Background(colorAmberBg).
				PaddingLeft(1).PaddingRight(1)

	styleSectionOpen = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorBlue).
				Background(colorBlueBg).
				PaddingLeft(1).PaddingRight(1)

	styleSectionDone = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorGray).
				Background(colorGrayBg).
				PaddingLeft(1).PaddingRight(1)

	styleSectionReady  = styleSectionOpen
	styleSectionReview = styleSectionInProgress
	styleSectionBacklog = styleSectionDone

	styleTitle  = lipgloss.NewStyle().Bold(true)
	styleMuted  = lipgloss.NewStyle().Foreground(colorMuted)
	styleHeader = lipgloss.NewStyle().Bold(true).Underline(true)

	styleCursor = lipgloss.NewStyle().
			Background(lipgloss.AdaptiveColor{Light: "#dee2e6", Dark: "#343a40"})

	styleStatusBar = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#495057", Dark: "#868e96"}).
			Background(lipgloss.AdaptiveColor{Light: "#e9ecef", Dark: "#212529"}).
			PaddingLeft(1).PaddingRight(1)

	styleInputPrompt = lipgloss.NewStyle().Foreground(colorBlue).Bold(true)
)

func priorityStyle(p int) lipgloss.Style {
	switch p {
	case 0:
		return lipgloss.NewStyle().Foreground(colorP0).Bold(true)
	case 1:
		return lipgloss.NewStyle().Foreground(colorP1)
	case 2:
		return lipgloss.NewStyle().Foreground(colorP2)
	default:
		return lipgloss.NewStyle().Foreground(colorP3)
	}
}

func priorityLabel(p int) string {
	switch p {
	case 0:
		return "P0"
	case 1:
		return "P1"
	case 2:
		return "P2"
	case 3:
		return "P3"
	default:
		return "P?"
	}
}

func statusIcon(s string) string {
	switch s {
	case "open":
		return "○"
	case "backlog":
		return "·"
	case "ready":
		return "◇"
	case "in_progress":
		return "◐"
	case "in_review":
		return "◑"
	case "done":
		return "✓"
	case "cancelled":
		return "✗"
	default:
		return "?"
	}
}

func sectionStyle(status string) lipgloss.Style {
	switch status {
	case "in_progress":
		return styleSectionInProgress
	case "in_review":
		return styleSectionReview
	case "open":
		return styleSectionOpen
	case "ready":
		return styleSectionReady
	case "backlog":
		return styleSectionBacklog
	case "done", "cancelled":
		return styleSectionDone
	default:
		return styleSectionOpen
	}
}
