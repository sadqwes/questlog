// Package photos хранит фото еды в S3-совместимом хранилище (MinIO в лабе).
// Перед сохранением фото уменьшается и пересжимается в JPEG: так оно легче,
// а все метаданные (включая GPS из EXIF) отбрасываются.
package photos

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // декодер PNG
	"io"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // декодер WebP
)

const (
	MaxUpload = 15 << 20 // исходник с телефона
	maxSide   = 1600     // длинная сторона после уменьшения
	quality   = 82
)

var ErrNotImage = errors.New("not a JPEG, PNG or WebP image")

// Store — куда складывать готовые JPEG.
type Store interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) (io.ReadCloser, int64, error)
	Delete(ctx context.Context, key string) error
}

type Config struct {
	Endpoint  string // minio.minio-system.svc.cluster.local:9000
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
}

type S3 struct {
	client *minio.Client
	bucket string
}

func NewS3(cfg Config) (*S3, error) {
	c, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, err
	}
	return &S3{client: c, bucket: cfg.Bucket}, nil
}

// Check проверяет, что bucket существует и доступен с этими ключами.
func (s *S3) Check(ctx context.Context) error {
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("bucket %q does not exist", s.bucket)
	}
	return nil
}

func (s *S3) Put(ctx context.Context, key string, data []byte) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "image/jpeg"})
	return err
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, err
	}
	st, err := obj.Stat()
	if err != nil {
		obj.Close()
		return nil, 0, err
	}
	return obj, st.Size, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

// NewKey — ключ объекта: meals/<id>/<случайные байты>.jpg. Случайная часть не даёт угадать чужие фото.
func NewKey(mealID int64) string {
	b := make([]byte, 12)
	rand.Read(b)
	return fmt.Sprintf("meals/%d/%s.jpg", mealID, hex.EncodeToString(b))
}

// ValidKey защищает GET /api/photos/{key} от путей вроде ../../
func ValidKey(key string) bool {
	return strings.HasPrefix(key, "meals/") && strings.HasSuffix(key, ".jpg") &&
		!strings.Contains(key, "..") && !strings.Contains(key, "//")
}

// Normalize: декодировать, повернуть по EXIF, уменьшить и сжать в JPEG без метаданных.
func Normalize(raw []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrNotImage
	}
	// сначала уменьшаем: поворот попиксельный, на 12-мегапиксельном снимке он в разы дольше
	img = orient(shrink(img, maxSide), exifOrientation(raw))
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func shrink(img image.Image, max int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= max && h <= max {
		return img
	}
	if w >= h {
		h, w = h*max/w, max
	} else {
		w, h = w*max/h, max
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	return dst
}

// orient поворачивает картинку так, как её показывает телефон (EXIF Orientation 1–8).
func orient(img image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	swap := o >= 5
	dw, dh := w, h
	if swap {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			switch o {
			case 2:
				nx, ny = w-1-x, y
			case 3:
				nx, ny = w-1-x, h-1-y
			case 4:
				nx, ny = x, h-1-y
			case 5:
				nx, ny = y, x
			case 6:
				nx, ny = h-1-y, x
			case 7:
				nx, ny = h-1-y, w-1-x
			case 8:
				nx, ny = y, w-1-x
			}
			dst.Set(nx, ny, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// exifOrientation находит тег Orientation (0x0112) в APP1-сегменте JPEG. 1 — если его нет.
func exifOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(b) {
		if b[i] != 0xFF {
			return 1
		}
		marker := b[i+1]
		size := int(b[i+2])<<8 | int(b[i+3])
		if marker == 0xDA || size < 2 || i+2+size > len(b) { // начались данные картинки
			return 1
		}
		seg := b[i+4 : i+2+size]
		if marker == 0xE1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var u16 func([]byte) int
	var u32 func([]byte) int
	switch string(t[:2]) {
	case "II":
		u16 = func(p []byte) int { return int(p[0]) | int(p[1])<<8 }
		u32 = func(p []byte) int { return int(p[0]) | int(p[1])<<8 | int(p[2])<<16 | int(p[3])<<24 }
	case "MM":
		u16 = func(p []byte) int { return int(p[0])<<8 | int(p[1]) }
		u32 = func(p []byte) int { return int(p[0])<<24 | int(p[1])<<16 | int(p[2])<<8 | int(p[3]) }
	default:
		return 1
	}
	off := u32(t[4:8])
	if off+2 > len(t) {
		return 1
	}
	n := u16(t[off : off+2])
	for k := 0; k < n; k++ {
		e := off + 2 + k*12
		if e+12 > len(t) {
			return 1
		}
		if u16(t[e:e+2]) == 0x0112 {
			return u16(t[e+8 : e+10])
		}
	}
	return 1
}
