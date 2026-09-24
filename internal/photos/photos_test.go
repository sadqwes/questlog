package photos

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// jpegWithOrientation собирает JPEG w×h и вставляет после SOI APP1-сегмент EXIF с тегом Orientation.
func jpegWithOrientation(t *testing.T, w, h, orientation int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		img.Set(x, 0, color.RGBA{255, 0, 0, 255})
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	tiff := []byte{'I', 'I', 42, 0, 8, 0, 0, 0, // заголовок TIFF, IFD со смещения 8
		1, 0, // одна запись
		0x12, 0x01, 3, 0, 1, 0, 0, 0, byte(orientation), 0, 0, 0, // Orientation, SHORT, 1, значение
		0, 0, 0, 0} // следующего IFD нет
	seg := append([]byte("Exif\x00\x00"), tiff...)
	app1 := append([]byte{0xFF, 0xE1, byte((len(seg) + 2) >> 8), byte(len(seg) + 2)}, seg...)
	return append(append([]byte{0xFF, 0xD8}, app1...), raw[2:]...)
}

func TestExifOrientationParsed(t *testing.T) {
	if got := exifOrientation(jpegWithOrientation(t, 4, 2, 6)); got != 6 {
		t.Errorf("orientation = %d, want 6", got)
	}
	if got := exifOrientation([]byte("not a jpeg")); got != 1 {
		t.Errorf("garbage orientation = %d, want 1", got)
	}
}

func TestNormalizeRotatesShrinksAndStripsExif(t *testing.T) {
	raw := jpegWithOrientation(t, 4000, 3000, 6) // снято «лёжа», телефон показывает стоя
	out, err := Normalize(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 1200 || cfg.Height != maxSide {
		t.Errorf("size = %dx%d, want 1200x%d (rotated to portrait and shrunk)", cfg.Width, cfg.Height, maxSide)
	}
	if bytes.Contains(out, []byte("Exif")) {
		t.Error("EXIF metadata survived normalization")
	}
}

func TestNormalizeRejectsNonImages(t *testing.T) {
	if _, err := Normalize([]byte("%PDF-1.7")); err != ErrNotImage {
		t.Errorf("err = %v, want ErrNotImage", err)
	}
}

func TestValidKey(t *testing.T) {
	good := []string{"meals/1/abc.jpg", NewKey(42)}
	bad := []string{"../etc/passwd", "meals/../x.jpg", "meals//x.jpg", "other/1.jpg", "meals/1/x.png"}
	for _, k := range good {
		if !ValidKey(k) {
			t.Errorf("ValidKey(%q) = false", k)
		}
	}
	for _, k := range bad {
		if ValidKey(k) {
			t.Errorf("ValidKey(%q) = true", k)
		}
	}
}
