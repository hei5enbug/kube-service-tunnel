package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
)

func parseColor(colorName string) tcell.Color {
	if colorName == "" {
		return tcell.ColorDefault
	}

	if len(colorName) > 0 && colorName[0] == '#' {
		return parseHexColor(colorName)
	}

	color := tcell.GetColor(colorName)
	if color == tcell.ColorDefault && colorName != "default" {
		return tcell.ColorDefault
	}
	return color
}

func parseHexColor(hex string) tcell.Color {
	if len(hex) != 7 || hex[0] != '#' {
		return tcell.ColorDefault
	}

	var r, g, b int32
	_, err := fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b)
	if err != nil {
		return tcell.ColorDefault
	}

	return tcell.NewRGBColor(r, g, b)
}

func colorToHex(color tcell.Color) string {
	r, g, b := color.RGB()
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}
