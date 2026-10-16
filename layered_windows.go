//go:build windows

package main

import (
	"image"
	"unsafe"
)

// pushLayered uploads a premultiplied RGBA image into a layered window.
// If pos is non-nil it is used as the destination (screen coords, top-level windows only).
func pushLayered(hwnd HWND, img *image.RGBA, pos *Point) bool {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if w <= 0 || h <= 0 {
		return false
	}
	screen := call(pGetDC, 0)
	defer call(pReleaseDC, 0, screen)
	mem := call(pCreateCompatibleDC, screen)
	defer call(pDeleteDC, mem)

	bi := BITMAPINFOHEADER{Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
	bi.Size = uint32(unsafe.Sizeof(bi))
	var bits unsafe.Pointer
	bmp := call(pCreateDIBSection, mem, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 {
		return false
	}
	defer call(pDeleteObject, bmp)
	old := call(pSelectObject, mem, bmp)
	defer call(pSelectObject, mem, old)

	dst := unsafe.Slice((*byte)(bits), w*h*4)
	src := img.Pix
	stride := img.Stride
	for y := 0; y < h; y++ {
		row := src[y*stride : y*stride+w*4]
		o := dst[y*w*4 : (y+1)*w*4]
		for x := 0; x < w*4; x += 4 {
			o[x], o[x+1], o[x+2], o[x+3] = row[x+2], row[x+1], row[x], row[x+3]
		}
	}

	size := SIZE{int32(w), int32(h)}
	srcPt := POINT{}
	blend := BLENDFUNCTION{AC_SRC_OVER, 0, 255, AC_SRC_ALPHA}
	var dstPt uintptr
	var p POINT
	if pos != nil {
		p = POINT{int32(pos.X), int32(pos.Y)}
		dstPt = uintptr(unsafe.Pointer(&p))
	}
	r := call(pUpdateLayeredWindow, hwnd, screen, dstPt, uintptr(unsafe.Pointer(&size)), mem,
		uintptr(unsafe.Pointer(&srcPt)), 0, uintptr(unsafe.Pointer(&blend)), ULW_ALPHA)
	return r != 0
}
