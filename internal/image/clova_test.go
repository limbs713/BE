package image

import (
	"testing"
)

func TestDetectImageFormat(t *testing.T) {
	tests := []struct {
		fileName string
		want     string
	}{
		{"photo.jpg", "jpeg"},
		{"photo.JPG", "jpeg"},
		{"photo.jpeg", "jpeg"},
		{"photo.JPEG", "jpeg"},
		{"scan.png", "png"},
		{"doc.pdf", "pdf"},
		{"image.tiff", "tiff"},
		{"image.gif", "gif"},
		{"image.bmp", "bmp"},
		{"unknown.webp", "png"},
		{"noextension", "png"},
		{"archive.tar.gz", "png"},
	}

	for _, tt := range tests {
		t.Run(tt.fileName, func(t *testing.T) {
			got := detectImageFormat(tt.fileName)
			if got != tt.want {
				t.Errorf("detectImageFormat(%q) = %q, want %q", tt.fileName, got, tt.want)
			}
		})
	}
}
