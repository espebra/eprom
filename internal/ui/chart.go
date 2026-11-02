package ui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/espebra/eprom/internal/metrics"
)

// Each braille character is a 2x4 grid of dots.
const (
	dotsX = 2
	dotsY = 4
)

// brailleBits[y][x] is the bit for the dot at column x, row y of a cell.
var brailleBits = [dotsY][dotsX]rune{
	{0x01, 0x08},
	{0x02, 0x10},
	{0x04, 0x20},
	{0x40, 0x80},
}

// axisColor is the canvas color used for the zero baseline.
const axisColor = -2

// canvas is a braille dot canvas where each cell remembers the color of the
// last series drawn into it.
type canvas struct {
	w, h  int // in cells
	bits  []rune
	color []int
}

func newCanvas(w, h int) *canvas {
	return &canvas{w: w, h: h, bits: make([]rune, w*h), color: make([]int, w*h)}
}

func (c *canvas) set(x, y, color int) {
	if x < 0 || y < 0 || x >= c.w*dotsX || y >= c.h*dotsY {
		return
	}
	i := (y/dotsY)*c.w + x/dotsX
	c.bits[i] |= brailleBits[y%dotsY][x%dotsX]
	c.color[i] = color
}

// line draws a line between two dots using Bresenham's algorithm.
func (c *canvas) line(x0, y0, x1, y1, color int) {
	dx, dy := abs(x1-x0), -abs(y1-y0)
	sx, sy := sign(x1-x0), sign(y1-y0)
	err := dx + dy
	for {
		c.set(x0, y0, color)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

// rows renders the canvas as styled lines.
func (c *canvas) rows() []string {
	out := make([]string, c.h)
	for row := 0; row < c.h; row++ {
		var b strings.Builder
		run, runColor := []rune{}, -1
		flush := func() {
			if len(run) == 0 {
				return
			}
			switch runColor {
			case -1:
				b.WriteString(string(run))
			case axisColor:
				b.WriteString(axisStyle.Render(string(run)))
			default:
				b.WriteString(seriesStyle(runColor).Render(string(run)))
			}
			run = run[:0]
		}
		for col := 0; col < c.w; col++ {
			i := row*c.w + col
			r, color := ' ', -1
			if c.bits[i] != 0 {
				r, color = 0x2800+c.bits[i], c.color[i]
			}
			if color != runColor {
				flush()
				runColor = color
			}
			run = append(run, r)
		}
		flush()
		out[row] = b.String()
	}
	return out
}

// chart renders series as a braille line chart of the given size (in cells,
// including the y-axes on both sides). The right edge is the latest scrape seq.
// Axis labels are formatted for the given base unit.
func chart(series []*metrics.Series, latest uint64, width, height int, unit string) []string {
	const axisWidth = 9 // label plus tick character
	cw := width - 2*axisWidth
	if cw < 4 || height < 2 {
		return nil
	}
	cols := uint64(cw * dotsX)

	// x position of a seq, or false if it is off-screen.
	xOf := func(seq uint64) (int, bool) {
		if seq > latest || latest-seq >= cols {
			return 0, false
		}
		return int(cols-1) - int(latest-seq), true
	}

	lo, hi := math.Inf(1), math.Inf(-1)
	for _, s := range series {
		for _, p := range s.Points {
			if _, ok := xOf(p.Seq); ok && isFinite(p.V) {
				lo, hi = math.Min(lo, p.V), math.Max(hi, p.V)
			}
		}
	}
	if math.IsInf(lo, 1) {
		lo, hi = 0, 1
	} else if lo == hi {
		pad := math.Abs(lo) * 0.1
		if pad == 0 {
			pad = 1
		}
		lo, hi = lo-pad, hi+pad
	}

	if lo < 0 && hi > 0 {
		lo, hi = alignZero(lo, hi, height)
	}

	rowsDots := height * dotsY
	yOf := func(v float64) int {
		return (rowsDots - 1) - int(math.Round((v-lo)/(hi-lo)*float64(rowsDots-1)))
	}

	cv := newCanvas(cw, height)

	// Draw a dotted baseline at Y=0 when it is inside the range. It is drawn
	// first so that series take over the color of cells they pass through.
	zeroRow := -1
	if lo < 0 && hi > 0 {
		y := yOf(0)
		zeroRow = y / dotsY
		for x := 0; x < cw*dotsX; x += 2 {
			cv.set(x, y, axisColor)
		}
	}

	for si, s := range series {
		var prevX, prevY int
		var prevSeq uint64
		havePrev := false
		for _, p := range s.Points {
			x, ok := xOf(p.Seq)
			if !ok || !isFinite(p.V) {
				havePrev = false
				continue
			}
			y := yOf(p.V)
			if havePrev && p.Seq == prevSeq+1 {
				cv.line(prevX, prevY, x, y, si)
			} else {
				cv.set(x, y, si)
			}
			prevX, prevY, prevSeq, havePrev = x, y, p.Seq, true
		}
	}

	rows := cv.rows()
	left := axisStyle.Width(axisWidth - 1).Align(lipgloss.Right)
	right := axisStyle.Width(axisWidth - 1).Align(lipgloss.Left)
	for i := range rows {
		label := ""
		switch {
		case i == 0:
			label = formatValue(hi, unit)
		case i == height-1:
			label = formatValue(lo, unit)
		case i == zeroRow:
			label = "0"
		case i == height/2 && zeroRow < 0:
			if height >= 5 {
				label = formatValue(lo+(hi-lo)*float64(height-1-i)/float64(height-1), unit)
			}
		}
		label = ansi.Truncate(label, axisWidth-1, "")
		rows[i] = left.Render(label) + axisStyle.Render("├") + rows[i] +
			axisStyle.Render("┤") + right.Render(label)
	}
	return rows
}

// alignZero widens the range [lo, hi], which must contain zero, so that zero
// falls on one of the two middle dot rows of a character cell. That puts the
// zero baseline level with the axis tick, which is drawn at the middle of the
// cell. The top and bottom rows are avoided so that their labels stay hi/lo.
func alignZero(lo, hi float64, height int) (float64, float64) {
	n := float64(height*dotsY - 1) // highest dot index
	zero := n * hi / (hi - lo)     // dot of zero, counted from the top

	first, last := 1, height-2
	if height < 3 {
		first, last = 0, height-1
	}
	best := math.Inf(1)
	for row := first; row <= last; row++ {
		for _, off := range []int{1, 2} {
			if y := float64(row*dotsY + off); math.Abs(y-zero) < math.Abs(best-zero) {
				best = y
			}
		}
	}
	// Move zero to the target dot by extending one end of the range, so that
	// all data stays visible. d is the target counted from the bottom.
	d := n - best
	if best < zero {
		lo = -d * hi / (n - d) // zero moves up: extend downwards
	} else if best > zero {
		hi = -lo * (n - d) / d // zero moves down: extend upwards
	}
	return lo, hi
}

func isFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func sign(v int) int {
	switch {
	case v < 0:
		return -1
	case v > 0:
		return 1
	}
	return 0
}
