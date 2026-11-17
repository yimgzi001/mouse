package vision

import "math"

// sampleRect resamples the rotated rectangle r of frame f into dst, an
// outW x outH x 3 float tensor (row-major, RGB, values 0..1).
//
// It follows MediaPipe's CPU path (OpenCV warp with bilinear interpolation):
// output pixel (0,0) maps to the rect's top-left corner and integer source
// coordinates are pixel centres. Pixels outside the frame are black, or the
// nearest edge pixel when replicate is true.
func sampleRect(f *Frame, r Rect, outW, outH int, dst []float32, replicate bool) {
	cos := float32(math.Cos(float64(r.Rot)))
	sin := float32(math.Sin(float64(r.Rot)))
	stepX := r.W / float32(outW)
	stepY := r.H / float32(outH)
	w, h := f.W, f.H
	pix := f.Pix
	const inv = 1.0 / 255.0

	idx := 0
	for j := 0; j < outH; j++ {
		dy := float32(j)*stepY - r.H/2
		for i := 0; i < outW; i++ {
			dx := float32(i)*stepX - r.W/2
			x := r.CX + dx*cos - dy*sin
			y := r.CY + dx*sin + dy*cos

			x0 := int(math.Floor(float64(x)))
			y0 := int(math.Floor(float64(y)))
			fx := x - float32(x0)
			fy := y - float32(y0)

			if x0 >= 0 && y0 >= 0 && x0+1 < w && y0+1 < h {
				p := (y0*w + x0) * 3
				q := p + w*3
				w00 := (1 - fx) * (1 - fy)
				w10 := fx * (1 - fy)
				w01 := (1 - fx) * fy
				w11 := fx * fy
				dst[idx] = (float32(pix[p])*w00 + float32(pix[p+3])*w10 + float32(pix[q])*w01 + float32(pix[q+3])*w11) * inv
				dst[idx+1] = (float32(pix[p+1])*w00 + float32(pix[p+4])*w10 + float32(pix[q+1])*w01 + float32(pix[q+4])*w11) * inv
				dst[idx+2] = (float32(pix[p+2])*w00 + float32(pix[p+5])*w10 + float32(pix[q+2])*w01 + float32(pix[q+5])*w11) * inv
				idx += 3
				continue
			}

			var acc [3]float32
			for k := 0; k < 4; k++ {
				px, py := x0+(k&1), y0+(k>>1)
				wx := 1 - fx
				if k&1 == 1 {
					wx = fx
				}
				wy := 1 - fy
				if k>>1 == 1 {
					wy = fy
				}
				if px < 0 || py < 0 || px >= w || py >= h {
					if !replicate {
						continue
					}
					px = clampInt(px, 0, w-1)
					py = clampInt(py, 0, h-1)
				}
				p := (py*w + px) * 3
				wt := wx * wy
				acc[0] += float32(pix[p]) * wt
				acc[1] += float32(pix[p+1]) * wt
				acc[2] += float32(pix[p+2]) * wt
			}
			dst[idx] = acc[0] * inv
			dst[idx+1] = acc[1] * inv
			dst[idx+2] = acc[2] * inv
			idx += 3
		}
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
