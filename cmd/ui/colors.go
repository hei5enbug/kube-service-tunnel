package ui

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

func getColorTag(color tcell.Color) string {
	if color == tcell.ColorWhite {
		return "[white]"
	}
	if color == tcell.ColorBlack {
		return "[black]"
	}
	if color == tcell.ColorRed {
		return "[red]"
	}
	if color == tcell.ColorGreen {
		return "[green]"
	}
	if color == tcell.ColorYellow {
		return "[yellow]"
	}
	if color == tcell.ColorBlue {
		return "[blue]"
	}
	return "[white]"
}

