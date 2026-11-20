package standard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"regexp"
	"strings"
	"testing"

	"github.com/yeqown/go-qrcode/v2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// printFixtureContent is the fixed text every print test encodes, so module
// grids, versions and geometry are checked against a stable reference.
const printFixtureContent = "go-qrcode print spec fixture v1"

// captureWriter records the matrix a QRCode saves, for grid comparisons.
type captureWriter struct {
	mat qrcode.Matrix
}

func (cw *captureWriter) Write(mat qrcode.Matrix) error { cw.mat = mat; return nil }
func (cw *captureWriter) Close() error                  { return nil }

// referenceBitmap encodes the content again through the plain public API and
// returns its module bitmap. Encoding is deterministic, so this is the
// ground-truth grid both exports must match.
func referenceBitmap(t *testing.T, content string, opts ...qrcode.EncodeOption) [][]bool {
	t.Helper()

	qrc, err := qrcode.NewWith(content, opts...)
	require.NoError(t, err)
	cw := &captureWriter{}
	require.NoError(t, qrc.Save(cw))

	return cw.mat.Bitmap()
}

func TestPreparePrint_Layout(t *testing.T) {
	job, err := PreparePrint(printFixtureContent, PrintSpec{
		WidthMM:          40,
		DPI:              300,
		MinModuleDots:    2,
		QuietZoneModules: 4,
	})
	require.NoError(t, err)

	r := job.Report()
	// fixed text at default EC level (Q) encodes as version 3, dimension 29.
	assert.Equal(t, 3, r.Version)
	assert.Equal(t, "Q", r.ErrorCorrection)
	assert.Equal(t, 29, r.Dimension)
	// floor(40/25.4*300) = 472 pixels budget; 29+2*4=37 modules per side.
	assert.Equal(t, 472, r.AvailablePixels)
	assert.Equal(t, 12, r.ModuleDots) // 472/37 = 12 rest 28
	assert.Equal(t, 48, r.QuietZoneDots)
	assert.Equal(t, 444, r.PixelWidth) // 37*12, remainder 28 px unused
	assert.InDelta(t, 37.592, r.PhysicalWidthMM, 1e-4)
	assert.Equal(t, 40.0, r.RequestedWidthMM)
	assert.Equal(t, 300.0, r.DPI)
	assert.Equal(t, 2, r.MinModuleDots)
	assert.Equal(t, 4, r.QuietZoneModules)
}

func TestPreparePrint_Defaults(t *testing.T) {
	job, err := PreparePrint(printFixtureContent, PrintSpec{WidthMM: 40, DPI: 300})
	require.NoError(t, err)

	r := job.Report()
	assert.Equal(t, 1, r.MinModuleDots, "min module dots defaults to 1")
	assert.Equal(t, 4, r.QuietZoneModules, "quiet zone defaults to 4 modules")
}

func TestPreparePrint_InvalidSpec(t *testing.T) {
	cases := map[string]PrintSpec{
		"NaN width":        {WidthMM: math.NaN(), DPI: 300},
		"+Inf width":       {WidthMM: math.Inf(1), DPI: 300},
		"-Inf width":       {WidthMM: math.Inf(-1), DPI: 300},
		"zero width":       {WidthMM: 0, DPI: 300},
		"negative width":   {WidthMM: -10, DPI: 300},
		"NaN DPI":          {WidthMM: 40, DPI: math.NaN()},
		"+Inf DPI":         {WidthMM: 40, DPI: math.Inf(1)},
		"zero DPI":         {WidthMM: 40, DPI: 0},
		"negative DPI":     {WidthMM: 40, DPI: -300},
		"negative quiet":   {WidthMM: 40, DPI: 300, QuietZoneModules: -1},
		"everything bogus": {WidthMM: math.NaN(), DPI: math.Inf(-1), QuietZoneModules: -5},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := PreparePrint(printFixtureContent, spec)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrPrintSpecInvalid)
		})
	}
}

func TestPreparePrint_TooSmall(t *testing.T) {
	// 10mm at 72 DPI is 28 pixels for at least 29+8=37 modules: 0 dots/module.
	_, err := PreparePrint(printFixtureContent, PrintSpec{WidthMM: 10, DPI: 72, MinModuleDots: 1})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPrintTooSmall)

	// same physical size but a demanding minimum of 4 dots per module.
	_, err = PreparePrint(printFixtureContent, PrintSpec{WidthMM: 12, DPI: 300, MinModuleDots: 4})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPrintTooSmall)
	assert.Contains(t, err.Error(), "need at least")
}

func TestPreparePrint_TooLarge(t *testing.T) {
	_, err := PreparePrint(printFixtureContent, PrintSpec{WidthMM: 100000, DPI: 300})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPrintTooLarge)
}

func TestPreparePrint_LongText(t *testing.T) {
	// beyond version 40 capacity even at level L: deterministic encode error.
	_, err := PreparePrint(strings.Repeat("A", 8000),
		PrintSpec{WidthMM: 400, DPI: 300},
		qrcode.WithErrorCorrectionLevel(qrcode.ErrorCorrectionLow))
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrPrintSpecInvalid)
	assert.Contains(t, err.Error(), "encode content failed")
}

func TestPreparePrint_ECLevels(t *testing.T) {
	levels := []struct {
		opt     qrcode.EncodeOption
		letter  string
		version int
		dim     int
	}{
		{qrcode.WithErrorCorrectionLevel(qrcode.ErrorCorrectionLow), "L", 2, 25},
		{qrcode.WithErrorCorrectionLevel(qrcode.ErrorCorrectionMedium), "M", 3, 29},
		{qrcode.WithErrorCorrectionLevel(qrcode.ErrorCorrectionQuart), "Q", 3, 29},
		{qrcode.WithErrorCorrectionLevel(qrcode.ErrorCorrectionHighest), "H", 4, 33},
	}

	prevVersion := 0
	for _, lv := range levels {
		job, err := PreparePrint(printFixtureContent,
			PrintSpec{WidthMM: 60, DPI: 300, MinModuleDots: 2}, lv.opt)
		require.NoError(t, err)

		r := job.Report()
		assert.Equal(t, lv.letter, r.ErrorCorrection)
		assert.Equal(t, lv.version, r.Version)
		assert.Equal(t, lv.dim, r.Dimension)
		assert.GreaterOrEqual(t, r.Version, prevVersion,
			"higher EC level never shrinks the version for the same text")
		prevVersion = r.Version
	}
}

func TestPreparePrint_PixelRounding(t *testing.T) {
	// 47.29mm at 254 DPI is 472.9 dots: floored to 472, never rounded up.
	job, err := PreparePrint("x", PrintSpec{WidthMM: 47.29, DPI: 254})
	require.NoError(t, err)
	assert.Equal(t, 472, job.Report().AvailablePixels)

	// 47.30mm at 254 DPI is exactly 473 dots.
	job, err = PreparePrint("x", PrintSpec{WidthMM: 47.30, DPI: 254})
	require.NoError(t, err)
	assert.Equal(t, 473, job.Report().AvailablePixels)

	// the used pixel width never exceeds the floored budget.
	job, err = PreparePrint(printFixtureContent, PrintSpec{WidthMM: 40, DPI: 300})
	require.NoError(t, err)
	r := job.Report()
	assert.LessOrEqual(t, r.PixelWidth, r.AvailablePixels)
	assert.Equal(t, r.PixelWidth, (r.Dimension+2*r.QuietZoneModules)*r.ModuleDots)
}

// pngModuleGrid decodes a PNG export and samples each module center.
func pngModuleGrid(t *testing.T, pngBytes []byte, r PrintReport) [][]bool {
	t.Helper()

	img, err := png.Decode(bytes.NewReader(pngBytes))
	require.NoError(t, err)
	require.Equal(t, r.PixelWidth, img.Bounds().Dx())
	require.Equal(t, r.PixelWidth, img.Bounds().Dy())

	grid := make([][]bool, r.Dimension)
	for y := 0; y < r.Dimension; y++ {
		grid[y] = make([]bool, r.Dimension)
		for x := 0; x < r.Dimension; x++ {
			px := r.QuietZoneDots + x*r.ModuleDots + r.ModuleDots/2
			py := r.QuietZoneDots + y*r.ModuleDots + r.ModuleDots/2
			cr, cg, cb, _ := img.At(px, py).RGBA()
			grid[y][x] = cr+cg+cb < 3*0x8000 // dark means set
		}
	}

	return grid
}

// svgModuleGrid parses the module path of an SVG export back into a grid.
func svgModuleGrid(t *testing.T, svg string, r PrintReport) [][]bool {
	t.Helper()

	grid := make([][]bool, r.Dimension)
	for y := range grid {
		grid[y] = make([]bool, r.Dimension)
	}

	re := regexp.MustCompile(`M(\d+) (\d+)h(\d+)v(\d+)h-\d+z`)
	for _, m := range re.FindAllStringSubmatch(svg, -1) {
		var px, py, mod int
		_, err := fmt.Sscanf(m[1], "%d", &px)
		require.NoError(t, err)
		_, err = fmt.Sscanf(m[2], "%d", &py)
		require.NoError(t, err)
		_, err = fmt.Sscanf(m[3], "%d", &mod)
		require.NoError(t, err)

		require.Equal(t, r.ModuleDots, mod, "every SVG module is exactly ModuleDots wide")
		require.Zero(t, (px-r.QuietZoneDots)%r.ModuleDots, "module x aligns to the dot grid")
		require.Zero(t, (py-r.QuietZoneDots)%r.ModuleDots, "module y aligns to the dot grid")

		x := (px - r.QuietZoneDots) / r.ModuleDots
		y := (py - r.QuietZoneDots) / r.ModuleDots
		require.True(t, x >= 0 && x < r.Dimension && y >= 0 && y < r.Dimension,
			"module inside the symbol area: (%d, %d)", x, y)
		require.False(t, grid[y][x], "module (%d, %d) drawn twice", x, y)
		grid[y][x] = true
	}

	return grid
}

func requireGridEqual(t *testing.T, want, got [][]bool, what string) {
	t.Helper()

	require.Equal(t, len(want), len(got), what)
	for y := range want {
		require.Equal(t, len(want[y]), len(got[y]), what)
		for x := range want[y] {
			assert.Equal(t, want[y][x], got[y][x], "%s module (%d, %d)", what, x, y)
		}
	}
}

func TestPrintJob_PNG_SVG_GeometryConsistent(t *testing.T) {
	spec := PrintSpec{WidthMM: 40, DPI: 300, MinModuleDots: 2, QuietZoneModules: 4}
	job, err := PreparePrint(printFixtureContent, spec)
	require.NoError(t, err)
	r := job.Report()

	var pngBuf, svgBuf bytes.Buffer
	require.NoError(t, job.WritePNG(&pngBuf))
	require.NoError(t, job.WriteSVG(&svgBuf))

	want := referenceBitmap(t, printFixtureContent)
	requireGridEqual(t, want, pngModuleGrid(t, pngBuf.Bytes(), r), "PNG")
	requireGridEqual(t, want, svgModuleGrid(t, svgBuf.String(), r), "SVG")

	// SVG document geometry matches the report.
	svg := svgBuf.String()
	assert.Contains(t, svg, `width="444" height="444" viewBox="0 0 444 444"`)

	// quiet zone is clear in the PNG: all four corners are background.
	img, err := png.Decode(bytes.NewReader(pngBuf.Bytes()))
	require.NoError(t, err)
	for _, p := range [][2]int{{0, 0}, {r.QuietZoneDots - 1, r.QuietZoneDots - 1}, {r.PixelWidth - 1, 0}, {0, r.PixelWidth - 1}} {
		cr, cg, cb, _ := img.At(p[0], p[1]).RGBA()
		assert.Greater(t, cr+cg+cb, uint32(3*0x8000), "quiet zone pixel (%d, %d) is background", p[0], p[1])
	}
}

func TestPrintJob_PNG_MatchesStandardWriter(t *testing.T) {
	// the print PNG must be byte-identical to the standard writer rendering
	// with equivalent geometry: the export changes sizing, never the drawing.
	spec := PrintSpec{WidthMM: 40, DPI: 300, MinModuleDots: 2, QuietZoneModules: 4}
	job, err := PreparePrint(printFixtureContent, spec)
	require.NoError(t, err)
	r := job.Report()

	var printed bytes.Buffer
	require.NoError(t, job.WritePNG(&printed))

	qrc, err := qrcode.NewWith(printFixtureContent)
	require.NoError(t, err)
	var standard bytes.Buffer
	w := NewWithWriter(nopWriteCloser{&standard},
		WithBuiltinImageEncoder(PNG_FORMAT),
		WithQRWidth(uint8(r.ModuleDots)),
		WithBorderWidth(r.QuietZoneDots),
	)
	require.NoError(t, qrc.Save(w))

	assert.Equal(t, standard.Bytes(), printed.Bytes())
}

type nopWriteCloser struct{ w *bytes.Buffer }

func (n nopWriteCloser) Write(p []byte) (int, error) { return n.w.Write(p) }
func (n nopWriteCloser) Close() error                { return nil }

func TestPrintJob_LogoAndGradient(t *testing.T) {
	spec := PrintSpec{WidthMM: 40, DPI: 300, MinModuleDots: 2, QuietZoneModules: 4}
	job, err := PreparePrint(printFixtureContent, spec)
	require.NoError(t, err)
	r := job.Report()

	logo := image.NewRGBA(image.Rect(0, 0, 24, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 24; x++ {
			logo.Set(x, y, color.RGBA{R: 0xde, G: 0x2c, B: 0x68, A: 0xff})
		}
	}
	gradient := NewGradient(45,
		ColorStop{T: 0, Color: color.RGBA{R: 0xff, A: 0xff}},
		ColorStop{T: 1, Color: color.RGBA{B: 0xff, A: 0xff}},
	)
	opts := []ImageOption{
		WithLogoImage(logo),
		WithLogoSafeZone(),
		WithFgGradient(gradient),
	}

	// PNG: logo and gradient survive print sizing; the logo sits centered.
	var pngBuf bytes.Buffer
	require.NoError(t, job.WritePNG(&pngBuf, opts...))
	img, err := png.Decode(bytes.NewReader(pngBuf.Bytes()))
	require.NoError(t, err)
	require.Equal(t, r.PixelWidth, img.Bounds().Dx())
	center := img.At(r.PixelWidth/2, r.PixelWidth/2)
	cr, cg, cb, _ := center.RGBA()
	assert.Equal(t, uint32(0xde)*0x101, cr)
	assert.Equal(t, uint32(0x2c)*0x101, cg)
	assert.Equal(t, uint32(0x68)*0x101, cb)

	// SVG: gradient and logo are embedded; module grid still matches the PNG.
	var svgBuf bytes.Buffer
	require.NoError(t, job.WriteSVG(&svgBuf, opts...))
	svg := svgBuf.String()
	assert.Contains(t, svg, `<linearGradient id="qrGradient"`)
	assert.Contains(t, svg, `data:image/png;base64,`)
	assert.Contains(t, svg, fmt.Sprintf(`x="%d" y="%d"`, (r.PixelWidth-24)/2, (r.PixelWidth-24)/2))

	// with the safe zone clearing center modules, PNG and SVG grids agree
	// outside the logo rectangle (inside it the PNG shows logo pixels).
	pngGrid := pngModuleGrid(t, pngBuf.Bytes(), r)
	svgGrid := svgModuleGrid(t, svg, r)
	logoLeft := (r.PixelWidth - 24) / 2
	for y := 0; y < r.Dimension; y++ {
		for x := 0; x < r.Dimension; x++ {
			cx := r.QuietZoneDots + x*r.ModuleDots + r.ModuleDots/2
			cy := r.QuietZoneDots + y*r.ModuleDots + r.ModuleDots/2
			if cx >= logoLeft && cx < logoLeft+24 && cy >= logoLeft && cy < logoLeft+24 {
				continue
			}
			assert.Equal(t, pngGrid[y][x], svgGrid[y][x],
				"PNG vs SVG with logo safe zone, module (%d, %d)", x, y)
		}
	}
}

func TestPrintJob_SVGRejectsHalftone(t *testing.T) {
	job, err := PreparePrint(printFixtureContent, PrintSpec{WidthMM: 40, DPI: 300})
	require.NoError(t, err)

	halftone := image.NewGray(image.Rect(0, 0, 4, 4))
	err = job.WriteSVG(&bytes.Buffer{}, WithHalftoneImage(halftone))
	assert.ErrorIs(t, err, ErrPrintSVGHalftone)
}

func TestPrintJob_NilWriter(t *testing.T) {
	job, err := PreparePrint(printFixtureContent, PrintSpec{WidthMM: 40, DPI: 300})
	require.NoError(t, err)

	assert.ErrorIs(t, job.WritePNG(nil), ErrNilWriter)
	assert.ErrorIs(t, job.WriteSVG(nil), ErrNilWriter)
	assert.ErrorIs(t, job.WriteReport(nil), ErrNilWriter)
}

func TestPrintJob_WriteReport(t *testing.T) {
	job, err := PreparePrint(printFixtureContent,
		PrintSpec{WidthMM: 40, DPI: 300, MinModuleDots: 2, QuietZoneModules: 4})
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, job.WriteReport(&buf))

	var decoded PrintReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &decoded))
	assert.Equal(t, job.Report(), decoded)
}

func TestPrintJob_SaveFiles(t *testing.T) {
	job, err := PreparePrint(printFixtureContent, PrintSpec{WidthMM: 40, DPI: 300, MinModuleDots: 2})
	require.NoError(t, err)
	r := job.Report()

	dir := t.TempDir()
	require.NoError(t, job.SavePNG(dir+"/out.png"))
	require.NoError(t, job.SaveSVG(dir+"/out.svg"))

	w, h, err := statImage(dir + "/out.png")
	require.NoError(t, err)
	assert.Equal(t, r.PixelWidth, w)
	assert.Equal(t, r.PixelWidth, h)
}

// TestPrintJob_EncodingUnchanged proves print planning does not alter the
// encoded content: the exported grid equals a plain encode of the same text.
func TestPrintJob_EncodingUnchanged(t *testing.T) {
	spec := PrintSpec{WidthMM: 50, DPI: 600, MinModuleDots: 3}
	job, err := PreparePrint(printFixtureContent, spec,
		qrcode.WithErrorCorrectionLevel(qrcode.ErrorCorrectionHighest))
	require.NoError(t, err)

	var pngBuf bytes.Buffer
	require.NoError(t, job.WritePNG(&pngBuf))

	want := referenceBitmap(t, printFixtureContent,
		qrcode.WithErrorCorrectionLevel(qrcode.ErrorCorrectionHighest))
	requireGridEqual(t, want, pngModuleGrid(t, pngBuf.Bytes(), job.Report()), "print PNG")
}
