package main

import (
	kolovideo "github.com/ciranda-dev/kolo-video"
	"github.com/ciranda-dev/kolo-video/vpx"
)

// The camera is asked for 1920x1080 at kolo-video's default rate, the largest picture the app takes from another
// participant. It is the ceiling of what is sent, not what is sent: the ladder starts at its lowest rung. The VP8
// encoder takes 4 ms for a 1080p frame of a textured picture (TestMeasureCodecs, Apple silicon, native engine), 12% of
// the 33 ms frame interval, under the 50% that makes 1080p the default. -video-size changes it.
const (
	defaultVideoWidth  = 1920
	defaultVideoHeight = 1080
)

// maxDefaultBitrate is the most bits a second the default rule gives a stream. kolo-video's 0.07 bits per pixel per
// frame gives 4.35 Mbit/s at 1080p and 30 fps; a larger picture needs fewer bits per pixel for the same quality, so
// the 1080p rung is held to 3.8 Mbit/s, inside the 3.5 to 4 Mbit/s that video calls use for it. The 720p and 360p
// rungs, 1.94 Mbit/s and 0.48 Mbit/s, are under it.
const maxDefaultBitrate = 3_800_000

// ladderHeights are the picture heights of the quality ladder, lowest first. A variable so a test can use small ones.
var ladderHeights = []int{360, 720, 1080}

// rung is one size of the quality ladder and its full bitrate, in bits a second.
type rung struct {
	w, h int
	rate float64
}

// videoLimits are the largest sizes the codec engine in use encodes and decodes in real time. A variable so a test
// can make them small.
var videoLimits = vpx.Capabilities

// tooBig reports whether a w x h stream, while others other people watch it, is larger than the engine encodes in real
// time or than its share of one real-time decoder at each receiver, which decodes the streams of all the others. Zero
// limits (no engine) leave nothing too big.
func tooBig(w, h int, fps float64, lim vpx.Limits, others int) bool {
	enc := lim.Encode.Width * lim.Encode.Height
	dec := float64(lim.Decode.Width*lim.Decode.Height) * lim.Decode.FrameRate
	px := w * h
	return enc > 0 && px > enc || dec > 0 && float64(max(1, others)*px)*fps > dec
}

// sendSize is the size a fw x fh camera frame is sent at when nothing is gained by a ladder, while others other people
// watch it: the frame halved while it is larger than the setting or too big (tooBig). It is the size of the lowest
// rung when even that is too big.
func sendSize(fw, fh int, v videoMode, lim vpx.Limits, others int) (w, h int) {
	w, h = fw, fh
	for w > 1 && h > 1 && (w*h > v.w*v.h || tooBig(w, h, v.fps, lim, others)) {
		w, h = w/2, h/2
	}
	return w, h
}

// ladderRungs lists the rungs for a fw x fh camera frame, lowest first, and the index of the highest one the engine and
// the receivers allow while others other people watch: the camera's size, scaled to fit the setting, is the top; below
// it come the heights of ladderHeights that are at least a quarter smaller, at the camera's shape. When even the lowest
// is too big it is halved until it is not.
func ladderRungs(v videoMode, fw, fh int, lim vpx.Limits, others int) (rs []rung, limit int) {
	tw, th := fw, fh
	if v.w < fw || v.h < fh {
		s := min(float64(v.w)/float64(fw), float64(v.h)/float64(fh))
		tw, th = max(2, int(float64(fw)*s)&^1), max(2, int(float64(fh)*s)&^1)
	}
	for _, h := range ladderHeights {
		if h*5 <= th*4 {
			rs = append(rs, rung{w: max(2, (tw*h/th+1)&^1), h: h})
		}
	}
	rs = append(rs, rung{w: tw, h: th})
	if tooBig(rs[0].w, rs[0].h, v.fps, lim, others) {
		rs[0].w, rs[0].h = sendSize(rs[0].w, rs[0].h, v, lim, others)
	}
	for i := range rs {
		rs[i].rate = float64(fullRate(v, rs[i].w, rs[i].h))
		if !tooBig(rs[i].w, rs[i].h, v.fps, lim, others) {
			limit = i
		}
	}
	return rs, limit
}

// halve writes src at half its size into dst, which is src.Width/2 by src.Height/2: each pixel is the mean of a 2x2
// block.
func halve(dst, src *kolovideo.Frame) {
	plane := func(d []byte, ds int, s []byte, ss, dw, dh, sw, sh int) {
		for y := range dh {
			r0, r1 := 2*y*ss, min(2*y+1, sh-1)*ss
			for x := range dw {
				x0, x1 := 2*x, min(2*x+1, sw-1)
				d[y*ds+x] = byte((int(s[r0+x0]) + int(s[r0+x1]) + int(s[r1+x0]) + int(s[r1+x1]) + 2) / 4)
			}
		}
	}
	plane(dst.Y, dst.YStride, src.Y, src.YStride, dst.Width, dst.Height, src.Width, src.Height)
	cw, ch, scw, sch := (dst.Width+1)/2, (dst.Height+1)/2, (src.Width+1)/2, (src.Height+1)/2
	plane(dst.U, dst.UVStride, src.U, src.UVStride, cw, ch, scw, sch)
	plane(dst.V, dst.UVStride, src.V, src.UVStride, cw, ch, scw, sch)
	dst.Timestamp, dst.Duration = src.Timestamp, src.Duration
}

// shrink writes src scaled down into dst, which is no larger than src: each pixel is the mean of the block of source
// pixels it covers.
func shrink(dst, src *kolovideo.Frame) {
	plane := func(d []byte, ds int, s []byte, ss, dw, dh, sw, sh int) {
		xs := make([]int, dw+1)
		for x := range xs {
			xs[x] = x * sw / dw
		}
		for y := range dh {
			y0 := y * sh / dh
			y1 := max((y+1)*sh/dh, y0+1)
			for x := range dw {
				x0, x1 := xs[x], max(xs[x+1], xs[x]+1)
				sum := 0
				for yy := y0; yy < y1; yy++ {
					for _, v := range s[yy*ss+x0 : yy*ss+x1] {
						sum += int(v)
					}
				}
				n := (y1 - y0) * (x1 - x0)
				d[y*ds+x] = byte((sum + n/2) / n)
			}
		}
	}
	plane(dst.Y, dst.YStride, src.Y, src.YStride, dst.Width, dst.Height, src.Width, src.Height)
	cw, ch, scw, sch := (dst.Width+1)/2, (dst.Height+1)/2, (src.Width+1)/2, (src.Height+1)/2
	plane(dst.U, dst.UVStride, src.U, src.UVStride, cw, ch, scw, sch)
	plane(dst.V, dst.UVStride, src.V, src.UVStride, cw, ch, scw, sch)
	dst.Timestamp, dst.Duration = src.Timestamp, src.Duration
}

// videoBitrate is the VP8 bitrate of a w x h stream: 0, kolo-video's rule (0.07 bits per pixel per frame), unless
// -video-bitrate set one for the setting's size, which then keeps its bits per pixel.
func videoBitrate(v videoMode, w, h int) int {
	if v.bitrate == 0 {
		return 0
	}
	return max(1, int(int64(v.bitrate)*int64(w*h)/int64(v.w*v.h)))
}
