package image

import (
	"bytes"
	"encoding/base64"
	"fmt"
	goimage "image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	_ "image/png"
	"io"
	"os"
	"strings"
	"sync/atomic"

	"github.com/BourgeoisBear/rasterm"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// rowColDiacritics contains Unicode combining characters used to encode
// row/column positions in Kitty Unicode placeholders.
// See: https://sw.kovidgoyal.net/kitty/_downloads/f0a0de9ec8d9ff4456206db8e0814937/rowcolumn-diacritics.txt
var rowColDiacritics = []rune{
	0x0305, 0x030D, 0x030E, 0x0310, 0x0312, 0x033D, 0x033E, 0x033F,
	0x0346, 0x034A, 0x034B, 0x034C, 0x0350, 0x0351, 0x0352, 0x0357,
	0x035B, 0x0363, 0x0364, 0x0365, 0x0366, 0x0367, 0x0368, 0x0369,
	0x036A, 0x036B, 0x036C, 0x036D, 0x036E, 0x036F, 0x0483, 0x0484,
	0x0485, 0x0486, 0x0487, 0x0592, 0x0593, 0x0594, 0x0595, 0x0597,
	0x0598, 0x0599, 0x059C, 0x059D, 0x059E, 0x059F, 0x05A0, 0x05A1,
	0x05A8, 0x05A9, 0x05AB, 0x05AC, 0x05AF, 0x05C4, 0x0610, 0x0611,
	0x0612, 0x0613, 0x0614, 0x0615, 0x0616, 0x0617, 0x0657, 0x0658,
	0x0659, 0x065A, 0x065B, 0x065D, 0x065E, 0x06D6, 0x06D7, 0x06D8,
	0x06D9, 0x06DA, 0x06DB, 0x06DC, 0x06DF, 0x06E0, 0x06E1, 0x06E2,
	0x06E4, 0x06E7, 0x06E8, 0x06EB, 0x06EC, 0x0730, 0x0732, 0x0733,
	0x0735, 0x0736, 0x073A, 0x073D, 0x073F, 0x0740, 0x0741, 0x0743,
	0x0745, 0x0747, 0x0749, 0x074A, 0x07EB, 0x07EC, 0x07ED, 0x07EE,
	0x07EF, 0x07F0, 0x07F1, 0x07F3, 0x0816, 0x0817, 0x0818, 0x0819,
	0x081B, 0x081C, 0x081D, 0x081E, 0x081F, 0x0820, 0x0821, 0x0822,
	0x0823, 0x0825, 0x0826, 0x0827, 0x0829, 0x082A, 0x082B, 0x082C,
	0x082D, 0x0951, 0x0953, 0x0954, 0x0F82, 0x0F83, 0x0F86, 0x0F87,
	0x135D, 0x135E, 0x135F, 0x17DD, 0x193A, 0x1A17, 0x1A75, 0x1A76,
	0x1A77, 0x1A78, 0x1A79, 0x1A7A, 0x1A7B, 0x1A7C, 0x1B6B, 0x1B6D,
	0x1B6E, 0x1B6F, 0x1B70, 0x1B71, 0x1B72, 0x1B73, 0x1CD0, 0x1CD1,
	0x1CD2, 0x1CDA, 0x1CDB, 0x1CE0, 0x1DC0, 0x1DC1, 0x1DC3, 0x1DC4,
	0x1DC5, 0x1DC6, 0x1DC7, 0x1DC8, 0x1DC9, 0x1DCB, 0x1DCC, 0x1DD1,
	0x1DD2, 0x1DD3, 0x1DD4, 0x1DD5, 0x1DD6, 0x1DD7, 0x1DD8, 0x1DD9,
	0x1DDA, 0x1DDB, 0x1DDC, 0x1DDD, 0x1DDE, 0x1DDF, 0x1DE0, 0x1DE1,
	0x1DE2, 0x1DE3, 0x1DE4, 0x1DE5, 0x1DE6, 0x1DFE, 0x20D0, 0x20D1,
	0x20D4, 0x20D5, 0x20D6, 0x20D7, 0x20DB, 0x20DC, 0x20E1, 0x20E7,
	0x20E9, 0x20F0, 0x2CEF, 0x2CF0, 0x2CF1, 0x2DE0, 0x2DE1, 0x2DE2,
	0x2DE3, 0x2DE4, 0x2DE5, 0x2DE6, 0x2DE7, 0x2DE8, 0x2DE9, 0x2DEA,
	0x2DEB, 0x2DEC, 0x2DED, 0x2DEE, 0x2DEF, 0x2DF0, 0x2DF1, 0x2DF2,
	0x2DF3, 0x2DF4, 0x2DF5, 0x2DF6, 0x2DF7, 0x2DF8, 0x2DF9, 0x2DFA,
	0x2DFB, 0x2DFC, 0x2DFD, 0x2DFE, 0x2DFF, 0xA66F, 0xA67C, 0xA67D,
	0xA6F0, 0xA6F1, 0xA8E0, 0xA8E1, 0xA8E2, 0xA8E3, 0xA8E4, 0xA8E5,
}

// imageIDCounter is used to generate unique image IDs for Kitty protocol
var imageIDCounter uint32

// nextImageID returns a unique image ID (1-16777215 range for 24-bit color encoding)
func nextImageID() uint32 {
	id := atomic.AddUint32(&imageIDCounter, 1)
	// Keep in 24-bit range (use lower 24 bits, but avoid 0)
	id = (id % 16777215) + 1
	return id
}

// TerminalImageCapability represents the terminal's image rendering capability
type TerminalImageCapability int

const (
	CapNone  TerminalImageCapability = iota // No image support
	CapKitty                                // Kitty graphics protocol
	CapITerm                                // iTerm2 inline images
	CapSixel                                // Sixel graphics
)

// String returns the capability name
func (c TerminalImageCapability) String() string {
	switch c {
	case CapKitty:
		return "kitty"
	case CapITerm:
		return "iterm"
	case CapSixel:
		return "sixel"
	default:
		return "none"
	}
}

// DetectCapability detects the terminal's image rendering capability
// Detection order: Kitty -> iTerm -> Sixel -> None
func DetectCapability() TerminalImageCapability {
	// Check Kitty first (TERM_PROGRAM or KITTY_WINDOW_ID)
	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return CapKitty
	}
	if strings.Contains(os.Getenv("TERM"), "kitty") {
		return CapKitty
	}

	// Check iTerm2 (TERM_PROGRAM or LC_TERMINAL)
	termProgram := os.Getenv("TERM_PROGRAM")
	if termProgram == "iTerm.app" {
		return CapITerm
	}
	lcTerminal := os.Getenv("LC_TERMINAL")
	if lcTerminal == "iTerm2" {
		return CapITerm
	}

	// Check for WezTerm (supports iTerm protocol)
	if termProgram == "WezTerm" {
		return CapITerm
	}

	// Check for Ghostty (supports Kitty protocol with Unicode placeholders)
	if termProgram == "ghostty" {
		return CapKitty
	}

	// Check for Sixel support via TERM
	term := os.Getenv("TERM")
	if strings.Contains(term, "sixel") || strings.Contains(term, "mlterm") {
		return CapSixel
	}

	// Check COLORTERM for some terminal emulators
	colorTerm := os.Getenv("COLORTERM")
	if colorTerm == "truecolor" || colorTerm == "24bit" {
		// Many modern terminals with truecolor also support images
		// but we can't be sure, so fall through
	}

	return CapNone
}

// RenderImageResult contains the result of rendering an image
type RenderImageResult struct {
	// Upload contains the upload/placement commands that must go directly to terminal
	// For Kitty: delete + transmit + placement commands
	// For iTerm/Sixel: empty (everything is in Placeholder)
	Upload string
	// Placeholder contains the displayable portion
	// For Kitty: Unicode placeholder characters
	// For iTerm/Sixel: full escape sequence
	Placeholder string
	// Full is Upload + Placeholder (for convenience in non-bubbletea contexts)
	Full string
}

// RenderImageToString renders an image and returns a string for display
// For Kitty: returns full upload sequence for first display, placeholder-only for cache
// For others: returns full escape sequence (same for both)
// The caller should cache result.Cached and use result.Full for first display.
func RenderImageToString(path string) (RenderImageResult, error) {
	cap := DetectCapability()
	if cap == CapNone {
		return RenderImageResult{}, nil
	}

	// Load the image
	img, err := loadImage(path)
	if err != nil {
		return RenderImageResult{}, fmt.Errorf("failed to load image: %w", err)
	}

	// Scale image if too large (max 800px width for reasonable terminal display)
	img = scaleImageIfNeeded(img, 800)

	var buf bytes.Buffer
	switch cap {
	case CapKitty:
		// Use Unicode placeholders for Kitty
		full, err := kittyUploadWithPlaceholders(img)
		if err != nil {
			return RenderImageResult{}, err
		}
		return RenderImageResult{Upload: "", Placeholder: full, Full: full}, nil
	case CapITerm:
		if err := rasterm.ItermWriteImage(&buf, img); err != nil {
			return RenderImageResult{}, err
		}
	case CapSixel:
		paletted := convertToPaletted(img)
		if err := rasterm.SixelWriteImage(&buf, paletted); err != nil {
			return RenderImageResult{}, err
		}
	default:
		return RenderImageResult{}, nil
	}
	s := buf.String()
	// For non-Kitty: everything goes in Placeholder, Upload is empty
	return RenderImageResult{Upload: "", Placeholder: s, Full: s}, nil
}

// debugKittyPlaceholder enables debug output to stderr for placeholder issues
var debugKittyPlaceholder = os.Getenv("DEBUG_KITTY_PLACEHOLDER") != ""

// kittyUploadWithPlaceholders uploads image and returns complete string with placeholders
// Following the tupimage approach: U=1 is included in the transmit command
func kittyUploadWithPlaceholders(img goimage.Image) (string, error) {
	imageID := nextImageID()

	// Encode image as PNG and base64
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return "", fmt.Errorf("failed to encode PNG: %w", err)
	}
	b64Data := base64.StdEncoding.EncodeToString(pngBuf.Bytes())

	// Calculate display size in cells (approx 10 pixels per cell width, 20 per height)
	bounds := img.Bounds()
	cols := (bounds.Dx() + 9) / 10
	rows := (bounds.Dy() + 19) / 20
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	if cols > 80 {
		cols = 80
	}
	if rows > 40 {
		rows = 40
	}

	var result bytes.Buffer

	// Transmit image with U=1 flag (following tupimage approach)
	// First chunk includes all parameters
	const chunkSize = 4096
	totalChunks := (len(b64Data) + chunkSize - 1) / chunkSize

	for i := 0; i < len(b64Data); i += chunkSize {
		end := i + chunkSize
		more := 1
		if end >= len(b64Data) {
			end = len(b64Data)
			more = 0
		}
		chunk := b64Data[i:end]

		if i == 0 {
			// First chunk: include all parameters
			// a=T: transmit and display, U=1: unicode placeholders, f=100: PNG
			// t=d: direct data, q=2: quiet
			if totalChunks > 1 {
				fmt.Fprintf(&result, "\x1b_Ga=T,U=1,f=100,t=d,i=%d,c=%d,r=%d,q=2,m=%d;%s\x1b\\",
					imageID, cols, rows, more, chunk)
			} else {
				fmt.Fprintf(&result, "\x1b_Ga=T,U=1,f=100,t=d,i=%d,c=%d,r=%d,q=2;%s\x1b\\",
					imageID, cols, rows, chunk)
			}
		} else {
			// Continuation chunks
			fmt.Fprintf(&result, "\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
	}

	// Output placeholder characters with foreground color encoding image ID
	// Color format: 24-bit RGB where the image ID is encoded
	r := (imageID >> 16) & 0xFF
	g := (imageID >> 8) & 0xFF
	b := imageID & 0xFF
	fmt.Fprintf(&result, "\x1b[38;2;%d;%d;%dm", r, g, b)

	// Output placeholder grid
	placeholderRune := rune(0x10EEEE)
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			result.WriteRune(placeholderRune)
			result.WriteRune(rowColDiacritics[row])
			result.WriteRune(rowColDiacritics[col])
		}
		if row < rows-1 {
			result.WriteByte('\n')
		}
	}

	// Reset foreground color
	result.WriteString("\x1b[39m")

	return result.String(), nil
}

// kittyUploadAndGetPlaceholder builds the full Kitty graphics escape sequence string
// Returns:
//   - upload: delete + transmit + placement commands (must go to terminal directly)
//   - placeholder: just the placeholder characters (safe for tea.Println)
//   - error: any error that occurred
func kittyUploadAndGetPlaceholder(img goimage.Image) (upload string, placeholder string, err error) {
	imageID := nextImageID()

	// Encode image as PNG
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return "", "", fmt.Errorf("failed to encode PNG: %w", err)
	}

	// Base64 encode
	b64Data := base64.StdEncoding.EncodeToString(pngBuf.Bytes())

	// Calculate display size in cells (approx 10 pixels per cell width, 20 per height)
	bounds := img.Bounds()
	cols := (bounds.Dx() + 9) / 10
	rows := (bounds.Dy() + 19) / 20
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	// Cap at reasonable size for terminal display
	if cols > 80 {
		cols = 80
	}
	if rows > 40 {
		rows = 40
	}

	if debugKittyPlaceholder {
		fmt.Fprintf(os.Stderr, "[KITTY] Image %dx%d -> %d cols x %d rows, ID=%d\n",
			bounds.Dx(), bounds.Dy(), cols, rows, imageID)
	}

	// Build upload commands (delete + transmit + placement)
	var uploadBuf bytes.Buffer

	// Delete any existing image with this ID to ensure clean upload
	fmt.Fprintf(&uploadBuf, "\x1b_Ga=d,i=%d,q=2\x1b\\", imageID)

	// Transmit in chunks (max 4096 bytes per chunk)
	// a=T: transmit, f=100: PNG, i=ID, q=2: quiet, m=more
	const chunkSize = 4096
	chunks := 0
	for i := 0; i < len(b64Data); i += chunkSize {
		end := i + chunkSize
		more := 1
		if end >= len(b64Data) {
			end = len(b64Data)
			more = 0
		}
		chunk := b64Data[i:end]
		chunks++

		if i == 0 {
			// t=d: direct transmission, f=100: PNG format
			fmt.Fprintf(&uploadBuf, "\x1b_Ga=T,t=d,f=100,i=%d,q=2,m=%d;%s\x1b\\", imageID, more, chunk)
		} else {
			fmt.Fprintf(&uploadBuf, "\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
	}

	if debugKittyPlaceholder {
		fmt.Fprintf(os.Stderr, "[KITTY] Uploaded %d chunks (%d bytes base64)\n", chunks, len(b64Data))
	}

	// Create virtual placement with Unicode placeholders enabled
	// a=p: place, U=1: use Unicode placeholders, c/r: columns/rows, q=2: quiet
	fmt.Fprintf(&uploadBuf, "\x1b_Ga=p,U=1,i=%d,c=%d,r=%d,q=2\x1b\\", imageID, cols, rows)

	if debugKittyPlaceholder {
		fmt.Fprintf(os.Stderr, "[KITTY] Created virtual placement\n")
	}

	// Build placeholder characters separately (this is what gets cached for re-display)
	// Image ID encoded in foreground color
	// Each cell: U+10EEEE + row diacritic + column diacritic
	var placeholderBuf bytes.Buffer

	if imageID <= 255 {
		fmt.Fprintf(&placeholderBuf, "\x1b[38;5;%dm", imageID)
	} else {
		r := (imageID >> 16) & 0xFF
		g := (imageID >> 8) & 0xFF
		b := imageID & 0xFF
		fmt.Fprintf(&placeholderBuf, "\x1b[38;2;%d;%d;%dm", r, g, b)
	}

	placeholderRune := rune(0x10EEEE)
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			placeholderBuf.WriteRune(placeholderRune)
			placeholderBuf.WriteRune(rowColDiacritics[row])
			placeholderBuf.WriteRune(rowColDiacritics[col])
		}
		if row < rows-1 {
			placeholderBuf.WriteByte('\n')
		}
	}

	// Reset foreground color
	placeholderBuf.WriteString("\x1b[39m")

	if debugKittyPlaceholder {
		fmt.Fprintf(os.Stderr, "[KITTY] Upload: %d bytes, Placeholder: %d bytes\n",
			uploadBuf.Len(), placeholderBuf.Len())
	}

	return uploadBuf.String(), placeholderBuf.String(), nil
}

// RenderImageToWriter renders an image to a writer using the detected capability
// This is for one-shot display (e.g., DisplayImage) - uses rasterm directly
func RenderImageToWriter(w io.Writer, path string) error {
	cap := DetectCapability()
	if cap == CapNone {
		return nil
	}

	// Load the image
	img, err := loadImage(path)
	if err != nil {
		return fmt.Errorf("failed to load image: %w", err)
	}

	// Scale image if too large (max 800px width for reasonable terminal display)
	img = scaleImageIfNeeded(img, 800)

	switch cap {
	case CapKitty:
		// Use rasterm directly for one-shot display
		return rasterm.KittyWriteImage(w, img, rasterm.KittyImgOpts{})
	case CapITerm:
		return rasterm.ItermWriteImage(w, img)
	case CapSixel:
		// Sixel requires a paletted image
		paletted := convertToPaletted(img)
		return rasterm.SixelWriteImage(w, paletted)
	default:
		return nil
	}
}

// loadImage loads an image from a file path
func loadImage(path string) (goimage.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	img, _, err := goimage.Decode(f)
	if err != nil {
		return nil, err
	}
	return img, nil
}

// scaleImageIfNeeded scales the image if it exceeds maxWidth
func scaleImageIfNeeded(img goimage.Image, maxWidth int) goimage.Image {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	if width <= maxWidth {
		return img
	}

	// Calculate new dimensions maintaining aspect ratio
	newWidth := maxWidth
	newHeight := (height * maxWidth) / width

	// Create scaled image
	dst := goimage.NewRGBA(goimage.Rect(0, 0, newWidth, newHeight))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)
	return dst
}

// convertToPaletted converts an image to a paletted image for Sixel output
func convertToPaletted(img goimage.Image) *goimage.Paletted {
	bounds := img.Bounds()

	// Create a palette with a reasonable number of colors for Sixel
	// We use a fixed 256-color palette
	palette := make(color.Palette, 256)

	// Generate a simple 6x6x6 color cube (216 colors) plus 40 grays
	idx := 0
	for r := 0; r < 6; r++ {
		for g := 0; g < 6; g++ {
			for b := 0; b < 6; b++ {
				palette[idx] = color.RGBA{
					R: uint8(r * 51),
					G: uint8(g * 51),
					B: uint8(b * 51),
					A: 255,
				}
				idx++
			}
		}
	}
	// Add 40 gray levels
	for i := 0; i < 40; i++ {
		gray := uint8(i * 255 / 39)
		palette[idx] = color.RGBA{R: gray, G: gray, B: gray, A: 255}
		idx++
	}

	// Create paletted image
	paletted := goimage.NewPaletted(bounds, palette)
	draw.FloydSteinberg.Draw(paletted, bounds, img, bounds.Min)
	return paletted
}
