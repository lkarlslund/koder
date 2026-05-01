package app

import (
	"math"
	"math/rand"
	"time"

	"github.com/lkarlslund/koder/internal/ui"
)

const bouncyBallsTickInterval = 33 * time.Millisecond

type bouncyBall struct {
	x      float64
	y      float64
	vx     float64
	vy     float64
	radius float64
	color  ui.CellColor
}

type bouncyBallsOverlay struct {
	Enabled       bool
	balls         []bouncyBall
	dirtyRects    []ui.Rect
	cleanupRects  []ui.Rect
	lastStepAt    time.Time
	tickSeq       uint64
	tickPending   bool
	tickPendingAt time.Time
	viewportW     int
	viewportH     int
}

func (o *bouncyBallsOverlay) Toggle(width, height int) {
	if o.Enabled {
		o.Disable()
		return
	}
	o.Enable(width, height)
}

func (o *bouncyBallsOverlay) Enable(width, height int) {
	o.Enabled = true
	o.viewportW = max(0, width)
	o.viewportH = max(0, height)
	minDim := min(o.viewportW, o.viewportH)
	if minDim <= 0 {
		minDim = 24
	}
	o.balls = []bouncyBall{
		{radius: scaledBallRadius(minDim, 0.07, 2.8, 5.5), color: ui.NewCellColorRGBA(255, 96, 96, 144)},
		{radius: scaledBallRadius(minDim, 0.11, 3.8, 7.5), color: ui.NewCellColorRGBA(96, 255, 180, 132)},
		{radius: scaledBallRadius(minDim, 0.15, 5.0, 10.0), color: ui.NewCellColorRGBA(96, 180, 255, 120)},
		{radius: scaledBallRadius(minDim, 0.20, 6.4, 13.0), color: ui.NewCellColorRGBA(255, 208, 96, 112)},
	}
	o.randomizePositions()
	o.fitToBounds(o.viewportW, o.viewportH)
	o.setDirtyFromCurrent()
	o.lastStepAt = time.Time{}
}

func (o *bouncyBallsOverlay) Disable() {
	if !o.Enabled {
		return
	}
	o.cleanupRects = append(o.cleanupRects[:0], o.currentBallRects()...)
	o.Enabled = false
	o.balls = nil
	o.lastStepAt = time.Time{}
	o.tickSeq++
	o.tickPending = false
	o.tickPendingAt = time.Time{}
	o.dirtyRects = nil
}

func (o *bouncyBallsOverlay) Resize(width, height int) {
	o.viewportW = max(0, width)
	o.viewportH = max(0, height)
	if !o.Enabled {
		return
	}
	before := o.currentBallRects()
	o.fitToBounds(width, height)
	o.setDirtyUnion(before, o.currentBallRects())
}

func (o *bouncyBallsOverlay) TickCmd() ui.Cmd {
	if !o.Enabled {
		o.tickPending = false
		o.tickPendingAt = time.Time{}
		return nil
	}
	dueAt := time.Now().Add(bouncyBallsTickInterval)
	if o.tickPending && !o.tickPendingAt.IsZero() && !dueAt.Before(o.tickPendingAt) {
		return nil
	}
	o.tickSeq++
	o.tickPending = true
	o.tickPendingAt = dueAt
	seq := o.tickSeq
	return ui.Tick(bouncyBallsTickInterval, func(t time.Time) ui.Msg {
		return bouncyBallsTickMsg{At: t, Seq: seq}
	})
}

func (o *bouncyBallsOverlay) Step(width, height int) bool {
	return o.StepAt(time.Now(), width, height)
}

func (o *bouncyBallsOverlay) StepAt(now time.Time, width, height int) bool {
	if !o.Enabled {
		return false
	}
	before := o.currentBallRects()
	o.viewportW = max(0, width)
	o.viewportH = max(0, height)
	o.fitToBounds(width, height)
	if width <= 0 || height <= 0 {
		return false
	}
	dt := bouncyBallsTickInterval.Seconds()
	if !o.lastStepAt.IsZero() && !now.IsZero() {
		elapsed := now.Sub(o.lastStepAt).Seconds()
		if elapsed > 0 {
			dt = clampFloat(elapsed, 0.012, 0.090)
		}
	}
	if now.IsZero() {
		now = time.Now()
	}
	o.lastStepAt = now
	for idx := range o.balls {
		ball := &o.balls[idx]
		ball.x += ball.vx * dt
		ball.y += ball.vy * dt

		minX := ball.radius + 1
		maxX := float64(width) - ball.radius - 1
		minY := ball.radius + 1
		maxY := float64(height) - ball.radius - 1
		if maxX < minX {
			ball.x = float64(width) / 2
			ball.vx = -ball.vx
		} else {
			if ball.x < minX {
				ball.x = minX
				ball.vx = math.Abs(ball.vx)
			} else if ball.x > maxX {
				ball.x = maxX
				ball.vx = -math.Abs(ball.vx)
			}
		}
		if maxY < minY {
			ball.y = float64(height) / 2
			ball.vy = -ball.vy
		} else {
			if ball.y < minY {
				ball.y = minY
				ball.vy = math.Abs(ball.vy)
			} else if ball.y > maxY {
				ball.y = maxY
				ball.vy = -math.Abs(ball.vy)
			}
		}
	}
	o.setDirtyUnion(before, o.currentBallRects())
	return true
}

func (o *bouncyBallsOverlay) Apply(surface ui.Surface) ui.Surface {
	if !o.Enabled && len(o.cleanupRects) == 0 {
		return surface
	}
	out := surface
	rects := make([]ui.Rect, 0, len(o.dirtyRects)+len(o.cleanupRects))
	if existing, ok := out.DirtyRects(); ok {
		rects = append(rects, existing...)
	}
	if o.Enabled {
		for _, ball := range o.balls {
			paintBouncyBall(&out, ball)
		}
		rects = append(rects, o.dirtyRects...)
	} else {
		rects = append(rects, o.cleanupRects...)
	}
	damage := ui.DamageSet{}
	damage.AddAll(rects)
	out = out.WithDirtyRects(damage.Normalized(ui.Rect{W: out.SurfaceWidth(), H: out.SurfaceHeight()})...)
	o.dirtyRects = nil
	o.cleanupRects = nil
	return out
}

func (o *bouncyBallsOverlay) fitToBounds(width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	for idx := range o.balls {
		ball := &o.balls[idx]
		minX := ball.radius + 1
		maxX := float64(width) - ball.radius - 1
		minY := ball.radius + 1
		maxY := float64(height) - ball.radius - 1
		if maxX < minX {
			ball.x = float64(width) / 2
		} else if ball.x < minX || ball.x > maxX {
			ball.x = clampFloat(ball.x, minX, maxX)
		}
		if maxY < minY {
			ball.y = float64(height) / 2
		} else if ball.y < minY || ball.y > maxY {
			ball.y = clampFloat(ball.y, minY, maxY)
		}
	}
}

func (o *bouncyBallsOverlay) randomizePositions() {
	if len(o.balls) == 0 || o.viewportW <= 0 || o.viewportH <= 0 {
		return
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	minDim := min(o.viewportW, o.viewportH)
	if minDim <= 0 {
		minDim = 24
	}
	for idx := range o.balls {
		ball := &o.balls[idx]
		sizeRatio := clampFloat(ball.radius/float64(minDim), 0.04, 0.24)
		sizeFactor := clampFloat(2.35-(sizeRatio*7.0), 0.55, 2.0)
		speed := (6.0 + rng.Float64()*22.0) * sizeFactor
		angle := rng.Float64() * 2 * math.Pi
		ball.vx = math.Cos(angle) * speed
		ball.vy = math.Sin(angle) * speed
		minAxisSpeed := maxFloat(1.8, speed*0.18)
		if math.Abs(ball.vx) < minAxisSpeed {
			ball.vx = math.Copysign(minAxisSpeed, ball.vx+0.001)
		}
		if math.Abs(ball.vy) < minAxisSpeed {
			ball.vy = math.Copysign(minAxisSpeed, ball.vy+0.001)
		}
		minX := ball.radius + 1
		maxX := float64(o.viewportW) - ball.radius - 1
		minY := ball.radius + 1
		maxY := float64(o.viewportH) - ball.radius - 1
		if maxX < minX {
			ball.x = float64(o.viewportW) / 2
		} else {
			ball.x = minX + rng.Float64()*(maxX-minX)
		}
		if maxY < minY {
			ball.y = float64(o.viewportH) / 2
		} else {
			ball.y = minY + rng.Float64()*(maxY-minY)
		}
	}
}

func (o *bouncyBallsOverlay) currentBallRects() []ui.Rect {
	if len(o.balls) == 0 {
		return nil
	}
	rects := make([]ui.Rect, 0, len(o.balls))
	for _, ball := range o.balls {
		rects = append(rects, ballRect(ball))
	}
	return rects
}

func (o *bouncyBallsOverlay) setDirtyFromCurrent() {
	o.dirtyRects = append(o.dirtyRects[:0], o.currentBallRects()...)
}

func (o *bouncyBallsOverlay) setDirtyUnion(before, after []ui.Rect) {
	damage := ui.DamageSet{}
	for idx := range before {
		damage.Add(unionRect(before[idx], after[idx]))
	}
	o.dirtyRects = damage.Rects()
}

func ballRect(ball bouncyBall) ui.Rect {
	padding := 2
	minX := int(math.Floor(ball.x-ball.radius)) - padding
	minY := int(math.Floor(ball.y-ball.radius)) - padding
	maxX := int(math.Ceil(ball.x+ball.radius)) + padding
	maxY := int(math.Ceil(ball.y+ball.radius)) + padding
	return ui.Rect{X: minX, Y: minY, W: max(0, maxX-minX+1), H: max(0, maxY-minY+1)}
}

func unionRect(a, b ui.Rect) ui.Rect {
	if a.Empty() {
		return b
	}
	if b.Empty() {
		return a
	}
	left := min(a.X, b.X)
	top := min(a.Y, b.Y)
	right := max(a.X+a.W, b.X+b.W)
	bottom := max(a.Y+a.H, b.Y+b.H)
	return ui.Rect{X: left, Y: top, W: right - left, H: bottom - top}
}

func paintBouncyBall(surface *ui.Surface, ball bouncyBall) {
	if surface == nil {
		return
	}
	bounds := ballRect(ball)
	for y := max(0, bounds.Y); y < min(surface.SurfaceHeight(), bounds.Y+bounds.H); y++ {
		for x := max(0, bounds.X); x < min(surface.SurfaceWidth(), bounds.X+bounds.W); x++ {
			dx := (float64(x) + 0.5) - ball.x
			dy := (float64(y) + 0.5) - ball.y
			dist := math.Sqrt(dx*dx + dy*dy)
			if dist > ball.radius+0.6 {
				continue
			}
			falloff := 1 - dist/(ball.radius+0.6)
			if falloff <= 0 {
				continue
			}
			alpha := uint8(min(170, max(18, int(float64(ball.color.A())*falloff)+24)))
			style := ui.CellStyle{
				FG: ball.color.WithAlpha(alpha),
				BG: ball.color.WithAlpha(alpha),
			}
			surface.BlendStyleAt(x, y, style)
		}
	}
}

func (m *Model) toggleBouncyBalls() {
	m.bouncyBalls.Toggle(max(0, m.width), max(0, m.height))
	if m.bouncyBalls.Enabled {
		m.status = "Bouncy balls enabled"
	} else {
		m.status = "Bouncy balls disabled"
	}
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func clampFloat(value, minValue, maxValue float64) float64 {
	return minFloat(maxFloat(value, minValue), maxValue)
}

func scaledBallRadius(minDim int, ratio, minValue, maxValue float64) float64 {
	scaled := float64(minDim) * ratio
	return clampFloat(scaled, minValue, maxValue)
}
