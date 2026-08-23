package music

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestReadRasterImageValidatesSizeAndPixels(t *testing.T) {
	var data bytes.Buffer
	pixel := image.NewRGBA(image.Rect(0, 0, 2, 2))
	pixel.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&data, pixel); err != nil {
		t.Fatal(err)
	}

	imageData, contentType, extension, err := readRasterImage(bytes.NewReader(data.Bytes()), int64(data.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(imageData, data.Bytes()) || contentType != "image/png" || extension != ".png" {
		t.Fatalf("image/type/extension = %q, %q, %q", imageData, contentType, extension)
	}

	_, _, _, err = readRasterImage(bytes.NewReader(data.Bytes()), maxCoverSize+1)
	if !errors.Is(err, ErrCoverTooLarge) {
		t.Fatalf("oversized error = %v", err)
	}
	_, _, _, err = readRasterImage(bytes.NewReader(data.Bytes()[:data.Len()-8]), int64(data.Len()-8))
	if !errors.Is(err, ErrInvalidCover) {
		t.Fatalf("truncated error = %v", err)
	}
}
