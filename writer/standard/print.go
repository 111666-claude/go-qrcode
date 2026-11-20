package standard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/yeqown/go-qrcode/v2"
)

// This file implements print specification export: given a physical size in
// millimeters and a printer DPI, it picks a QR version whose modules cover an
// integer number of print dots (>= a minimum), and renders PNG or SVG output
// whose module grid aligns exactly to the print dot grid.

const (
	// mmPerInch converts millimeters into inches (DPI is dots per inch).
	mmPerInch = 25.4

	// defaultQuietZoneModules is the quiet zone width in modules required by
	// ISO/IEC 18004 on each side of the symbol.
	defaultQuietZoneModules = 4

	// maxPrintPixels bounds one side of the exported image so a mistaken or
	// absurd spec (e.g. kilometers of width) fails instead of exhausting
	// memory. 16384 px covers a 1.39 m poster at 300 DPI.
	maxPrintPixels = 16384
)

var (
	// ErrPrintSpecInvalid indicates a non-finite, non-positive or otherwise
	// unusable print specification.
	ErrPrintSpecInvalid = errors.New("print: invalid spec")

	// ErrPrintTooSmall indicates the content cannot be printed at the
	// requested size: each module would cover fewer dots than the minimum.
	ErrPrintTooSmall = errors.New("print: module size below minimum")

	// ErrPrintTooLarge indicates the requested output exceeds maxPrintPixels
	// on one side.
	ErrPrintTooLarge = errors.New("print: requested size too large")

	// ErrPrintSVGHalftone indicates a halftone image was configured for SVG
	// output, which is not supported (PNG output supports it).
	ErrPrintSVGHalftone = errors.New("print: halftone is not supported in SVG output")
)

// PrintSpec describes the physical constraints of a print job.
type PrintSpec struct {
	// WidthMM is the physical width (and height, the symbol is square) of the
	// whole code including the quiet zone, in millimeters. Must be finite and
	// positive.
	WidthMM float64

	// DPI is the printer resolution in dots per inch. Must be finite and
	// positive.
	DPI float64

	// MinModuleDots is the minimum number of print dots (pixels) each QR
	// module must cover. Values below 1 are treated as 1.
	MinModuleDots int

	// QuietZoneModules is the quiet zone width in modules on each side.
	// Zero selects the default of 4 (ISO/IEC 18004); negative is invalid.
	QuietZoneModules int
}

// validate rejects non-finite, non-positive or negative spec fields.
func (s PrintSpec) validate() error {
	if math.IsNaN(s.WidthMM) || math.IsInf(s.WidthMM, 0) || s.WidthMM <= 0 {
		return fmt.Errorf("%w: width must be a finite positive millimeter value, got %v",
			ErrPrintSpecInvalid, s.WidthMM)
	}
	if math.IsNaN(s.DPI) || math.IsInf(s.DPI, 0) || s.DPI <= 0 {
		return fmt.Errorf("%w: DPI must be a finite positive value, got %v",
			ErrPrintSpecInvalid, s.DPI)
	}
	if s.QuietZoneModules < 0 {
		return fmt.Errorf("%w: quiet zone modules must be >= 0, got %d",
			ErrPrintSpecInvalid, s.QuietZoneModules)
	}

	return nil
}

// normalized fills in defaults deterministically.
func (s PrintSpec) normalized() PrintSpec {
	if s.MinModuleDots < 1 {
		s.MinModuleDots = 1
	}
	if s.QuietZoneModules == 0 {
		s.QuietZoneModules = defaultQuietZoneModules
	}

	return s
}

// printLayout is the resolved dot-grid geometry of a print job.
type printLayout struct {
	availablePixels int // floor(WidthMM * DPI / 25.4), never exceeded
	modules         int // dimension + 2*quietZone
	moduleDots      int // print dots per module
	pixelWidth      int // modules * moduleDots, the exported image edge
}

// computeLayout resolves the dot-grid geometry for a matrix of the given
// dimension. The available pixel budget is floored (never rounded up) so the
// printed symbol never exceeds the ordered physical width, and the module
// count per side divides it into an integer number of dots per module; the
// remainder stays unused.
func computeLayout(spec PrintSpec, dimension int) (printLayout, error) {
	avail := spec.WidthMM * spec.DPI / mmPerInch
	if avail > maxPrintPixels {
		return printLayout{}, fmt.Errorf("%w: %.2f mm at %.2f DPI needs %d pixels per side (max %d)",
			ErrPrintTooLarge, spec.WidthMM, spec.DPI, int(math.Ceil(avail)), maxPrintPixels)
	}

	l := printLayout{
		availablePixels: int(math.Floor(avail)),
		modules:         dimension + 2*spec.QuietZoneModules,
	}
	l.moduleDots = l.availablePixels / l.modules
	if l.moduleDots < spec.MinModuleDots {
		needMM := float64(l.modules*spec.MinModuleDots) / spec.DPI * mmPerInch
		return printLayout{}, fmt.Errorf(
			"%w: %d modules (dimension %d + quiet zone %d) into %d pixels gives %d dot(s) per module (< %d); "+
				"need at least %.2f mm at %.2f DPI",
			ErrPrintTooSmall, l.modules, dimension, spec.QuietZoneModules,
			l.availablePixels, l.moduleDots, spec.MinModuleDots, needMM, spec.DPI)
	}
	l.pixelWidth = l.modules * l.moduleDots

	return l, nil
}

// PrintReport describes the resolved print job. It is JSON-serializable so it
// can be attached to a print order or stored next to the exported image.
type PrintReport struct {
	Version          int     `json:"version"`          // QR version selected for the content
	ErrorCorrection  string  `json:"errorCorrection"`  // L, M, Q or H
	Dimension        int     `json:"dimension"`        // modules per side, excluding quiet zone
	QuietZoneModules int     `json:"quietZoneModules"` // quiet zone width in modules
	ModuleDots       int     `json:"moduleDots"`       // print dots (pixels) per module
	QuietZoneDots    int     `json:"quietZoneDots"`    // quiet zone width in dots
	PixelWidth       int     `json:"pixelWidth"`       // exported image edge in pixels
	AvailablePixels  int     `json:"availablePixels"`  // pixel budget from WidthMM and DPI (floor)
	PhysicalWidthMM  float64 `json:"physicalWidthMM"`  // actual physical edge of the export
	RequestedWidthMM float64 `json:"requestedWidthMM"` // ordered physical edge
	DPI              float64 `json:"dpi"`
	MinModuleDots    int     `json:"minModuleDots"`
}

// PrintJob is a prepared print export: the content is encoded once and can be
// rendered to PNG and SVG any number of times with identical geometry.
type PrintJob struct {
	qrc    *qrcode.QRCode
	spec   PrintSpec
	layout printLayout
	report PrintReport
}

// PreparePrint encodes content (honoring optional qrcode.EncodeOption values
// such as qrcode.WithErrorCorrectionLevel) and resolves the print layout for
// spec. It selects the smallest QR version that holds the content — larger
// versions only shrink modules — and rejects the job with ErrPrintTooSmall
// when even that version cannot honor MinModuleDots. The encoded content is
// never altered by print planning.
func PreparePrint(content string, spec PrintSpec, encOpts ...qrcode.EncodeOption) (*PrintJob, error) {
	if err := spec.validate(); err != nil {
		return nil, err
	}
	spec = spec.normalized()

	qrc, err := qrcode.NewWith(content, encOpts...)
	if err != nil {
		return nil, fmt.Errorf("print: encode content failed: %w", err)
	}

	layout, err := computeLayout(spec, qrc.Dimension())
	if err != nil {
		return nil, err
	}

	job := &PrintJob{qrc: qrc, spec: spec, layout: layout}
	job.report = PrintReport{
		Version:          qrc.Version(),
		ErrorCorrection:  ecLevelString(qrc),
		Dimension:        qrc.Dimension(),
		QuietZoneModules: spec.QuietZoneModules,
		ModuleDots:       layout.moduleDots,
		QuietZoneDots:    layout.moduleDots * spec.QuietZoneModules,
		PixelWidth:       layout.pixelWidth,
		AvailablePixels:  layout.availablePixels,
		PhysicalWidthMM:  math.Round(float64(layout.pixelWidth)/spec.DPI*mmPerInch*1e4) / 1e4,
		RequestedWidthMM: spec.WidthMM,
		DPI:              spec.DPI,
		MinModuleDots:    spec.MinModuleDots,
	}

	return job, nil
}

// ecLevelString maps the encoded error correction level to its ISO letter.
func ecLevelString(qrc *qrcode.QRCode) string {
	switch qrc.ErrorCorrectionLevel() {
	case qrcode.ErrorCorrectionLow:
		return "L"
	case qrcode.ErrorCorrectionMedium:
		return "M"
	case qrcode.ErrorCorrectionQuart:
		return "Q"
	case qrcode.ErrorCorrectionHighest:
		return "H"
	}

	return "?"
}

// Report returns the resolved print specification report.
func (j *PrintJob) Report() PrintReport {
	return j.report
}

// WriteReport writes the report as indented JSON.
func (j *PrintJob) WriteReport(w io.Writer) error {
	if w == nil {
		return ErrNilWriter
	}

	b, err := json.MarshalIndent(j.report, "", "  ")
	if err != nil {
		return err
	}
	if _, err = w.Write(b); err != nil {
		return err
	}
	_, err = w.Write([]byte("\n"))

	return err
}

// printImageOptions builds output options from user options, then enforces
// the print geometry: module edge in dots, quiet zone in dots, rectangle
// modules (so PNG and SVG grids are identical and modules stay square on the
// print dot grid). User-supplied colors, logo and gradient are preserved.
func (j *PrintJob) printImageOptions(imgOpts []ImageOption) *outputImageOptions {
	oo := defaultOutputImageOption()
	for _, opt := range imgOpts {
		if opt != nil {
			opt.apply(oo)
		}
	}

	quiet := j.layout.moduleDots * j.spec.QuietZoneModules
	oo.qrWidth = j.layout.moduleDots
	oo.borderWidths = [4]int{quiet, quiet, quiet, quiet}
	oo.shape = _shapeRectangle

	return oo
}

// printWriter adapts the resolved options to the qrcode.Writer interface.
type printWriter struct {
	out    io.Writer
	option *outputImageOptions
}

func (pw *printWriter) Write(mat qrcode.Matrix) error { return drawTo(pw.out, mat, pw.option) }
func (pw *printWriter) Close() error                  { return nil }

// WritePNG renders the job as PNG. Logo, gradient, colors and transparency
// options behave exactly as in the standard writer; module geometry and quiet
// zone come from the print spec.
func (j *PrintJob) WritePNG(w io.Writer, imgOpts ...ImageOption) error {
	if w == nil {
		return ErrNilWriter
	}

	oo := j.printImageOptions(imgOpts)
	oo.imageEncoder = pngEncoder{}

	return j.qrc.Save(&printWriter{out: w, option: oo})
}

// SavePNG renders the job as PNG into a file at path.
func (j *PrintJob) SavePNG(path string, imgOpts ...ImageOption) error {
	fd, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("print: create %s failed: %w", path, err)
	}
	defer fd.Close()

	return j.WritePNG(fd, imgOpts...)
}

// SaveSVG renders the job as SVG into a file at path.
func (j *PrintJob) SaveSVG(path string, imgOpts ...ImageOption) error {
	fd, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("print: create %s failed: %w", path, err)
	}
	defer fd.Close()

	return j.WriteSVG(fd, imgOpts...)
}
