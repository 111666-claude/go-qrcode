## Standard Writer

[![go.dev reference](https://img.shields.io/badge/go.dev-reference-007d9c?logo=go&logoColor=white&style=flat-square)](https://pkg.go.dev/github.com/yeqown/go-qrcode/writer/standard)

Standard Writer is a writer that is used to draw QR Code image into `io.Writer`, normally a file.

### Usage

```go
options := []standard.ImageOption{
	standard.WithBgColorRGBHex("#ffffff"),
	standard.WithFgColorRGBHex("#000000"),
	// more ...
}

// New will create file automatically.
writer, err := standard.New("filename", options...)

// or use io.WriteCloser
var w io.WriterCloser
writer2, err := standard.NewWith(w, options...)
```

### Options

```go
// WithBgTransparent makes the background transparent.
func WithBgTransparent() ImageOption {}

// WithBgColor background color
func WithBgColor(c color.Color) ImageOption {}

// WithBgColorRGBHex background color
func WithBgColorRGBHex(hex string) ImageOption {}

// WithFgColor QR color
func WithFgColor(c color.Color) ImageOption {}

// WithFgColorRGBHex Hex string to set QR Color
func WithFgColorRGBHex(hex string) ImageOption {}

// QR gradient
func WithFgGradient(g *LinearGradient)

// WithLogoImage .
func WithLogoImage(img image.Image) ImageOption {}

// WithLogoImageFilePNG load image from file, PNG is required
func WithLogoImageFilePNG(f string) ImageOption {}

// WithLogoImageFileJPEG load image from file, JPEG is required
func WithLogoImageFileJPEG(f string) ImageOption {}

// WithQRWidth specify width of each qr block
func WithQRWidth(width uint8) ImageOption {}

// WithCircleShape use circle shape as rectangle(default)
func WithCircleShape() ImageOption {}

// WithCustomShape use custom shape as rectangle(default)
func WithCustomShape(shape IShape) ImageOption {}

// WithBuiltinImageEncoder option includes: JPEG_FORMAT as default, PNG_FORMAT.
// This works like WithBuiltinImageEncoder, the different between them is
// formatTyp is enumerated in (JPEG_FORMAT, PNG_FORMAT)
func WithBuiltinImageEncoder(format formatTyp) ImageOption

// WithCustomImageEncoder to use custom image encoder to encode image.Image into
// io.Writer
func WithCustomImageEncoder(encoder ImageEncoder) ImageOption

// WithBorderWidth specify the both 4 sides' border width. Notice that
// WithBorderWidth(a) means all border width use this variable `a`,
// WithBorderWidth(a, b) mean top/bottom equal to `a`, left/right equal to `b`.
// WithBorderWidth(a, b, c, d) mean top, right, bottom, left.
func WithBorderWidth(widths ...int) ImageOption

// WithHalftone ...
func WithHalftone(path string) ImageOption

// WithLogoSizeMultiplier used in Writer in validLogoImage method to validate logo size
func WithLogoSizeMultiplier(multiplier int)

// WithLogoSafeZone specify the safe zone of logo image
func WithLogoSafeZone()
```

### extension

- [How to customize QR Code shape](./how-to-use-custom-shape.md)
- [How to customize ImageEncoder](./how-to-use-image-encoder.md)

### Print specification export

`PreparePrint` plans a QR code for a physical print job: give it the printed
width in millimeters, the printer DPI, the minimum number of print dots each
module must cover, and the quiet zone width. It selects the smallest QR
version that holds the content and rejects the job when even that version
cannot honor the minimum module size — modules always cover an integer number
of print dots, so nothing on the grid is smaller than a printed dot.

```go
job, err := standard.PreparePrint("https://yeqown.xyz", standard.PrintSpec{
	WidthMM:          40,  // physical edge including quiet zone
	DPI:              300, // printer resolution
	MinModuleDots:    2,   // each module covers at least 2x2 print dots
	QuietZoneModules: 4,   // 0 selects the ISO/IEC 18004 default of 4
}, qrcode.WithErrorCorrectionLevel(qrcode.ErrorCorrectionQuart))
if err != nil {
	// ErrPrintSpecInvalid (non-finite / non-positive spec),
	// ErrPrintTooSmall (module would be smaller than MinModuleDots),
	// ErrPrintTooLarge (output bigger than 16384 px per side),
	// or an encoding error (content too long for version 40).
}

// PNG and SVG share the same module grid, quiet zone, logo and gradient.
err = job.SavePNG("code.png", standard.WithLogoImageFilePNG("logo.png"))
err = job.SaveSVG("code.svg", standard.WithFgGradient(gradient))

// JSON report: version, EC level, dimension, dots per module, pixel size,
// actual physical size, DPI, ...
report, _ := json.Marshal(job.Report())
```

Notes:

- The available pixel budget `WidthMM * DPI / 25.4` is always floored, so the
  exported symbol never exceeds the ordered physical width; leftover pixels
  stay unused (`AvailablePixels - PixelWidth` in the report).
- The encoded content is never altered by print planning; logo, gradient,
  colors and transparency behave exactly as in the standard writer. Module
  shape is forced to rectangles so PNG and SVG grids are identical.
- Halftone images are PNG-only; `WriteSVG` rejects them with
  `ErrPrintSVGHalftone`.
