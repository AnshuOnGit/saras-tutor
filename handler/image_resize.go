package handler

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"math"

	"golang.org/x/image/draw"
)

// maxImageDimension is the maximum width or height (in pixels) for uploaded images.
// Images larger than this are down-scaled proportionally before storage.
const maxImageDimension = 1568

// resizeImage decodes an image from raw bytes, down-scales it if either
// dimension exceeds maxImageDimension, then re-encodes it.
// Returns (resized bytes, output mime type, error).
// If the image is already small enough, the original bytes are returned unchanged.
func resizeImage(raw []byte, inputMime string) ([]byte, string, error) {
	reader := bytes.NewReader(raw)
	src, format, err := image.Decode(reader)
	if err != nil {
		return nil, "", fmt.Errorf("decode image: %w", err)
	}

	bounds := src.Bounds()
	origW := bounds.Dx()
	origH := bounds.Dy()

	if origW <= maxImageDimension && origH <= maxImageDimension {
		log.Printf("[resize] image %dx%d (%s) within limit — no resize needed", origW, origH, format)
		return raw, inputMime, nil
	}

	// Calculate new dimensions preserving aspect ratio
	scale := math.Min(
		float64(maxImageDimension)/float64(origW),
		float64(maxImageDimension)/float64(origH),
	)
	newW := int(math.Round(float64(origW) * scale))
	newH := int(math.Round(float64(origH) * scale))

	log.Printf("[resize] image %dx%d → %dx%d (scale=%.2f, format=%s)", origW, origH, newW, newH, scale, format)

	// Resize using high-quality CatmullRom interpolation
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)

	// Re-encode in the original format
	var buf bytes.Buffer
	outMime := inputMime

	switch format {
	case "png":
		err = png.Encode(&buf, dst)
		outMime = "image/png"
	default:
		// JPEG for jpg, or as default (smaller than PNG for photos)
		err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85})
		outMime = "image/jpeg"
	}
	if err != nil {
		return nil, "", fmt.Errorf("encode resized image: %w", err)
	}

	log.Printf("[resize] resized: %d bytes → %d bytes (%.0f%% reduction)",
		len(raw), buf.Len(), (1-float64(buf.Len())/float64(len(raw)))*100)

	return buf.Bytes(), outMime, nil
}

// readAndResize reads all bytes from r, then resizes if necessary.
// This is a convenience wrapper for the multipart upload path.
func readAndResize(r io.Reader, inputMime string) ([]byte, string, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, "", fmt.Errorf("read file: %w", err)
	}
	return resizeImage(raw, inputMime)
}
