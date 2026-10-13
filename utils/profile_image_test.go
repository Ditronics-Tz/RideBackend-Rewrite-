package utils

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func TestNormalizeProfileImageDetectsDecodesAndStripsTrailingMetadata(t *testing.T) {
	var source bytes.Buffer
	if err := png.Encode(&source, image.NewRGBA(image.Rect(0, 0, 5, 3))); err != nil {
		t.Fatal(err)
	}
	marker := []byte("EXIF-private-marker")
	input := append(append([]byte(nil), source.Bytes()...), marker...)
	output, mime, err := NormalizeProfileImage(input)
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/png" || bytes.Contains(output, marker) {
		t.Fatal("normalized profile image retained metadata or used an unexpected format")
	}
	if _, format, err := image.Decode(bytes.NewReader(output)); err != nil || format != "png" {
		t.Fatal("normalized profile image is not a valid PNG")
	}
}

func TestNormalizeProfileImageReencodesJPEGAndRejectsSpoofedOrOversizedInput(t *testing.T) {
	var source bytes.Buffer
	if err := jpeg.Encode(&source, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatal(err)
	}
	output, mime, err := NormalizeProfileImage(source.Bytes())
	if err != nil || mime != "image/png" {
		t.Fatalf("valid JPEG normalization failed: mime=%q err=%v", mime, err)
	}
	if _, format, err := image.Decode(bytes.NewReader(output)); err != nil || format != "png" {
		t.Fatal("JPEG was not re-encoded as PNG")
	}
	for name, input := range map[string][]byte{"spoofed": []byte("MZ fake executable"), "oversized": bytes.Repeat([]byte{0}, 5*1024*1024+1)} {
		if _, _, err := NormalizeProfileImage(input); err == nil || !strings.Contains(strings.ToLower(err.Error()), "image") {
			t.Fatalf("%s image unexpectedly accepted", name)
		}
	}
}
