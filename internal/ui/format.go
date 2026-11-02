package ui

import (
	"math"
	"strconv"

	"github.com/charmbracelet/lipgloss"
)

var (
	palette = []lipgloss.Color{
		"39", "208", "170", "82", "226", "196", "51", "141",
		"214", "33", "205", "118", "220", "160", "45", "99",
	}

	titleStyle  = lipgloss.NewStyle().Bold(true)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	axisStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	headerStyle = lipgloss.NewStyle().Reverse(true)
)

func seriesStyle(i int) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(palette[i%len(palette)])
}

type prefix struct {
	scale  float64
	suffix string
}

// siPrefixes has no prefixes below 1 for unitless values: "250m" is easily
// misread as millions or minutes, so small values are shown as plain decimals.
var siPrefixes = []prefix{
	{1e15, "P"}, {1e12, "T"}, {1e9, "G"}, {1e6, "M"}, {1e3, "k"}, {1, ""},
}

var bytePrefixes = []prefix{
	{1e15, "PB"}, {1e12, "TB"}, {1e9, "GB"}, {1e6, "MB"}, {1e3, "KB"}, {1, "B"},
}

var secondPrefixes = []prefix{
	{1, "s"}, {1e-3, "ms"}, {1e-6, "µs"}, {1e-9, "ns"},
}

var longDurations = []prefix{
	{86400, "d"}, {3600, "h"}, {60, "min"},
}

// formatValue formats v for display in the given base unit. Timestamps are
// stored as ages, so they are formatted as durations.
func formatValue(v float64, unit string) string {
	switch unit {
	case "bytes":
		return humanizeWith(v, bytePrefixes, "B")
	case "seconds", "timestamp":
		return formatDuration(v)
	}
	return humanize(v)
}

// formatDuration formats seconds as e.g. 250ms, 12.5s, 3.5min, 4.2h or 3.1d.
func formatDuration(v float64) string {
	if isFinite(v) {
		a := math.Abs(v)
		for _, p := range longDurations {
			if a >= p.scale {
				// 3 significant digits keep e.g. -12.3min within the axis width.
				return trim(strconv.FormatFloat(v/p.scale, 'g', 3, 64)) + p.suffix
			}
		}
	}
	return humanizeWith(v, secondPrefixes, "s")
}

// humanize formats v with up to 4 significant digits and an SI prefix for
// large values, or as a plain decimal with 3 significant digits below 1.
func humanize(v float64) string { return humanizeWith(v, siPrefixes, "") }

// humanizeWith formats v with up to 4 significant digits and the largest
// prefix not exceeding |v|. Values below the smallest prefix get 3
// significant digits and the given suffix.
func humanizeWith(v float64, prefixes []prefix, small string) string {
	switch {
	case math.IsNaN(v):
		return "-"
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case v == 0:
		return "0" + small
	}
	a := math.Abs(v)
	for _, p := range prefixes {
		if a >= p.scale {
			return trim(strconv.FormatFloat(v/p.scale, 'g', 4, 64)) + p.suffix
		}
	}
	return strconv.FormatFloat(v, 'g', 3, 64) + small
}

// trim avoids exponent notation for values that round up to 1000, e.g.
// 999.97 formats as "1e+03" with 4 significant digits.
func trim(s string) string {
	if f, err := strconv.ParseFloat(s, 64); err == nil && math.Abs(f) >= 1000 {
		return strconv.FormatFloat(f, 'f', 0, 64)
	}
	return s
}
