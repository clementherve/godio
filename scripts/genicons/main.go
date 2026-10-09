// Command genicons draws godio's tray icons.
//
// The icons are generated rather than hand-drawn so the five states stay
// visually consistent: one mic glyph, one status dot, same geometry throughout.
// Run with `go run ./scripts/genicons` after changing colours or shapes.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

const size = 32

// A tray icon has to survive both the light and the dark Windows taskbar, which
// a single flat grey cannot do. Every shape is therefore stroked in a dark
// outline and filled light: the outline carries it on light backgrounds, the
// fill carries it on dark ones.
var (
	micFill    = color.NRGBA{0xEC, 0xEE, 0xF1, 0xFF}
	micOutline = color.NRGBA{0x25, 0x28, 0x2D, 0xFF}
	green      = color.NRGBA{0x3F, 0xC9, 0x6B, 0xFF}
	red        = color.NRGBA{0xE5, 0x4B, 0x4B, 0xFF}
	amber      = color.NRGBA{0xF0, 0xA8, 0x2E, 0xFF}
	inactive   = color.NRGBA{0x6B, 0x70, 0x78, 0xFF}
)

// state is one tray icon: the mic glyph plus a right-hand column of two status
// dots, upper = microphone, lower = system sound.
type state struct {
	name string
	// upper and lower are the two dot colours; nil draws the inactive grey.
	upper, lower *color.NRGBA
	// badge replaces the two dots with a single crossed disc, for errors.
	badge *color.NRGBA
}

func main() {
	states := []state{
		{name: "idle"},
		{name: "rec-mic-sys", upper: &green, lower: &green},
		{name: "rec-mic", upper: &green, lower: &red},
		{name: "rec-sys", upper: &red, lower: &green},
		{name: "rec-none", upper: &red, lower: &red},
		{name: "processing", upper: &amber, lower: &amber},
		{name: "error", badge: &red},
	}

	out := filepath.Join("internal", "assets", "icons")
	if err := os.MkdirAll(out, 0o755); err != nil {
		panic(err)
	}

	for _, s := range states {
		img := draw(s)

		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			panic(err)
		}
		write(filepath.Join(out, s.name+".png"), buf.Bytes())
		write(filepath.Join(out, s.name+".ico"), ico(buf.Bytes()))
		fmt.Printf("  %s\n", s.name)
	}
}

func write(path string, data []byte) {
	if err := os.WriteFile(path, data, 0o644); err != nil {
		panic(err)
	}
}

// draw renders one 32x32 icon: a microphone glyph on the left, two status dots
// stacked on the right.
func draw(s state) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))

	// Outline pass first, then the fill on top. A tray icon has to survive both
	// the light and the dark Windows taskbar, which a single flat tone cannot.
	for _, pass := range []struct {
		grow float64
		c    color.NRGBA
	}{
		{1.2, micOutline},
		{0, micFill},
	} {
		g := pass.grow
		// The glyph is shifted left and shrunk from the single-dot version to
		// clear the right-hand column for two dots.
		capsule(img, 6, 3, 12, 14, 3, g, pass.c)
		arc(img, 9, 11, 6, 2+2*g, 0, math.Pi, pass.c)
		capsule(img, 8, 16, 10, 22, 1, g, pass.c)
		capsule(img, 4, 21, 14, 23, 1, g, pass.c)
	}

	const (
		dotX  = 23.5
		upper = 8.5
		lower = 23.0
		ringR = 7.0
		fillR = 5.0
	)

	if s.badge != nil {
		// One crossed disc, centred between where the two dots would sit, so an
		// error never reads as a half-working recording.
		cy := (upper + lower) / 2
		disc(img, dotX, cy, ringR+1.5, micOutline)
		disc(img, dotX, cy, fillR+1.5, *s.badge)
		line(img, dotX-3.5, cy-3.5, dotX+3.5, cy+3.5, 2.0, micOutline)
		line(img, dotX+3.5, cy-3.5, dotX-3.5, cy+3.5, 2.0, micOutline)
		return img
	}

	for _, d := range []struct {
		y float64
		c *color.NRGBA
	}{{upper, s.upper}, {lower, s.lower}} {
		fill := inactive
		if d.c != nil {
			fill = *d.c
		}
		disc(img, dotX, d.y, ringR, micOutline)
		disc(img, dotX, d.y, fillR, fill)
	}
	return img
}

// blend draws c over the pixel at (x,y) with the given coverage (0..1).
func blend(img *image.NRGBA, x, y int, c color.NRGBA, a float64) {
	if x < 0 || y < 0 || x >= size || y >= size || a <= 0 {
		return
	}
	if a > 1 {
		a = 1
	}
	dst := img.NRGBAAt(x, y)
	da := float64(dst.A) / 255
	na := a + da*(1-a)
	if na <= 0 {
		img.SetNRGBA(x, y, color.NRGBA{})
		return
	}
	mix := func(s, d uint8) uint8 {
		v := (float64(s)*a + float64(d)*da*(1-a)) / na
		return uint8(math.Round(math.Min(255, math.Max(0, v))))
	}
	img.SetNRGBA(x, y, color.NRGBA{mix(c.R, dst.R), mix(c.G, dst.G), mix(c.B, dst.B), uint8(math.Round(na * 255))})
}

// coverage antialiases a signed distance: inside -> 1, outside -> 0.
func coverage(d float64) float64 { return clamp(0.5 - d) }

func clamp(v float64) float64 { return math.Min(1, math.Max(0, v)) }

func disc(img *image.NRGBA, cx, cy, r float64, c color.NRGBA) {
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy) - r
			blend(img, x, y, c, coverage(d))
		}
	}
}

// capsule draws a rounded rectangle from (x0,y0) to (x1,y1), expanded by grow.
func capsule(img *image.NRGBA, x0, y0, x1, y1 int, r, grow float64, c color.NRGBA) {
	fx0, fy0 := float64(x0)+r, float64(y0)+r
	fx1, fy1 := float64(x1)-r, float64(y1)-r
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			dx := math.Max(math.Max(fx0-px, px-fx1), 0)
			dy := math.Max(math.Max(fy0-py, py-fy1), 0)
			blend(img, x, y, c, coverage(math.Hypot(dx, dy)-r-grow))
		}
	}
}

// arc draws a stroked arc of radius r between angles a0 and a1. Angles are
// measured with y growing downwards, so 0..Pi sweeps the lower half.
func arc(img *image.NRGBA, cx, cy, r, w, a0, a1 float64, c color.NRGBA) {
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			dx, dy := px-cx, py-cy
			ang := math.Atan2(dy, dx)
			if ang < 0 {
				ang += 2 * math.Pi
			}
			if ang < a0 || ang > a1 {
				continue
			}
			d := math.Abs(math.Hypot(dx, dy)-r) - w/2
			blend(img, x, y, c, coverage(d))
		}
	}
}

// line draws an antialiased stroke between two points.
func line(img *image.NRGBA, x0, y0, x1, y1, w float64, c color.NRGBA) {
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			blend(img, x, y, c, coverage(distToSegment(px, py, x0, y0, x1, y1)-w/2))
		}
	}
}

func distToSegment(px, py, x0, y0, x1, y1 float64) float64 {
	vx, vy := x1-x0, y1-y0
	wx, wy := px-x0, py-y0
	l2 := vx*vx + vy*vy
	t := 0.0
	if l2 > 0 {
		t = math.Min(1, math.Max(0, (wx*vx+wy*vy)/l2))
	}
	return math.Hypot(px-(x0+t*vx), py-(y0+t*vy))
}

// ico wraps a PNG in a single-image ICO container. Windows has accepted
// PNG-compressed ICO entries since Vista, which avoids writing a BMP encoder.
func ico(pngData []byte) []byte {
	var b bytes.Buffer
	w := func(v any) { binary.Write(&b, binary.LittleEndian, v) }

	w(uint16(0)) // reserved
	w(uint16(1)) // type: icon
	w(uint16(1)) // image count

	w(uint8(size)) // width
	w(uint8(size)) // height
	w(uint8(0))    // palette size
	w(uint8(0))    // reserved
	w(uint16(1))   // colour planes
	w(uint16(32))  // bits per pixel
	w(uint32(len(pngData)))
	w(uint32(22)) // offset: 6-byte header + 16-byte entry

	b.Write(pngData)
	return b.Bytes()
}
