package standard

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/color"
	"image/png"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/yeqown/go-qrcode/v2"
)

// svgWriter renders a QR matrix as SVG using the exact geometry resolved by
// the print job, so finder patterns, data modules and the quiet zone land on
// the same coordinates as the PNG export.
type svgWriter struct {
	out    io.Writer
	option *outputImageOptions
	module int // dots per module
	quiet  int // quiet zone width in dots
	size   int // total edge in dots
}

func (sw *svgWriter) Close() error { return nil }

func (sw *svgWriter) Write(mat qrcode.Matrix) error {
	return renderSVG(sw.out, mat, sw.option, sw.module, sw.quiet, sw.size)
}

// WriteSVG renders the job as SVG. Modules are emitted as a single path of
// axis-aligned squares; a configured gradient becomes a userSpaceOnUse
// linearGradient with the same direction vector as the PNG export, and a
// configured logo is embedded as a centered base64 PNG. Halftone images are
// not supported in SVG and are rejected with ErrPrintSVGHalftone.
func (j *PrintJob) WriteSVG(w io.Writer, imgOpts ...ImageOption) error {
	if w == nil {
		return ErrNilWriter
	}

	oo := j.printImageOptions(imgOpts)
	if oo.halftoneImg != nil {
		return ErrPrintSVGHalftone
	}

	quiet := j.layout.moduleDots * j.spec.QuietZoneModules

	return j.qrc.Save(&svgWriter{
		out:    w,
		option: oo,
		module: j.layout.moduleDots,
		quiet:  quiet,
		size:   j.layout.pixelWidth,
	})
}

// renderSVG writes the matrix as an SVG document. Coordinates mirror draw():
// module (x, y) occupies [quiet+x*module, quiet+y*module) with edge module.
func renderSVG(w io.Writer, mat qrcode.Matrix, oo *outputImageOptions, module, quiet, size int) error {
	var buf bytes.Buffer

	fmt.Fprintf(&buf, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`,
		size, size, size, size)

	// background, mirroring draw(): a full-bleed rectangle unless transparent.
	if !oo.bgTransparent {
		buf.WriteString(`<rect width="100%" height="100%" fill="`)
		buf.WriteString(rgbaHex(oo.bgColor))
		buf.WriteString(`"/>`)
	}

	// gradient definition with the same direction vector as applyGradient.
	fgFill := rgbaHex(oo.qrColor)
	if oo.qrGradient != nil && len(oo.qrGradient.Stops) > 0 {
		writeSVGGradient(&buf, oo.qrGradient, size)
		fgFill = "url(#qrGradient)"
	}

	// logo geometry, identical to draw(): centered, validated against the
	// same size multiplier, and optionally protected by a safe zone.
	var (
		logoValid        bool
		logoWidth, logoH int
	)
	if logo := oo.logoImage(); logo != nil {
		bound := logo.Bounds()
		logoWidth, logoH = bound.Dx(), bound.Dy()
		logoValid = validLogoImage(size, size, logoWidth, logoH, oo.logoSizeMultiplier)
	}

	// one path holding every set module as an axis-aligned square.
	var path strings.Builder
	mat.Iterate(qrcode.IterDirection_ROW, func(x, y int, v qrcode.QRValue) {
		if !v.IsSet() {
			return
		}
		if logoValid && oo.logoSafeZone &&
			blockOverlapsLogo(x, y, module, quiet, quiet, size, size, logoWidth, logoH) {
			return
		}

		px, py := quiet+x*module, quiet+y*module
		fmt.Fprintf(&path, "M%d %dh%dv%dh-%dz", px, py, module, module, module)
	})
	buf.WriteString(`<path fill="`)
	buf.WriteString(fgFill)
	buf.WriteString(`" d="`)
	buf.WriteString(path.String())
	buf.WriteString(`"/>`)

	// centered logo, same offset formula as draw(): (size-logo)/2.
	if logoValid {
		var pngBuf bytes.Buffer
		if err := png.Encode(&pngBuf, oo.logoImage()); err != nil {
			return fmt.Errorf("print: encode logo for SVG failed: %w", err)
		}
		fmt.Fprintf(&buf, `<image x="%d" y="%d" width="%d" height="%d" href="data:image/png;base64,%s"/>`,
			(size-logoWidth)/2, (size-logoH)/2, logoWidth, logoH,
			base64.StdEncoding.EncodeToString(pngBuf.Bytes()))
	}

	buf.WriteString(`</svg>`)

	_, err := w.Write(buf.Bytes())
	return err
}

// writeSVGGradient emits a linearGradient whose axis matches applyGradient:
// angle 0 points right, 90 points up (y grows downward in both renderers).
func writeSVGGradient(buf *bytes.Buffer, g *LinearGradient, size int) {
	angleRad := g.Angle * math.Pi / 180.0
	dx, dy := math.Cos(angleRad), -math.Sin(angleRad)

	minProj, maxProj := math.Inf(1), math.Inf(-1)
	for _, c := range [4][2]float64{{0, 0}, {0, float64(size)}, {float64(size), 0}, {float64(size), float64(size)}} {
		proj := c[0]*dx + c[1]*dy
		minProj = math.Min(minProj, proj)
		maxProj = math.Max(maxProj, proj)
	}

	buf.WriteString(`<defs><linearGradient id="qrGradient" gradientUnits="userSpaceOnUse"`)
	fmt.Fprintf(buf, ` x1="%s" y1="%s" x2="%s" y2="%s">`,
		svgFloat(dx*minProj), svgFloat(dy*minProj), svgFloat(dx*maxProj), svgFloat(dy*maxProj))
	for _, stop := range g.Stops {
		fmt.Fprintf(buf, `<stop offset="%s%%" stop-color="%s"`,
			svgFloat(stop.T*100), rgbaHex(stop.Color))
		if stop.Color.A != 0xff {
			fmt.Fprintf(buf, ` stop-opacity="%s"`, svgFloat(float64(stop.Color.A)/255))
		}
		buf.WriteString(`/>`)
	}
	buf.WriteString(`</linearGradient></defs>`)
}

// rgbaHex formats a color as #rrggbb.
func rgbaHex(c color.RGBA) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// svgFloat formats a float deterministically with the shortest round-trip
// representation.
func svgFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
