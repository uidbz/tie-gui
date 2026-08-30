package ui

import (
	"fmt"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/fs"
)

// refreshInterval is how often the ops view resamples speed and repaints while
// operations are active. 200ms is smooth to the eye without churning the UI.
const refreshInterval = 200 * time.Millisecond

// opView renders one progress row per active file operation. A single ticker
// goroutine (not one-per-cell) samples transfer speed and repaints, so the
// bars advance smoothly instead of jittering under a swarm of pollers.
type opView struct {
	ops     *fs.Operations
	list    *widget.List
	mu      sync.Mutex
	samples map[*fs.Op]*sample
}

// sample tracks the previous byte count and a smoothed transfer rate for one op.
type sample struct {
	lastBytes int64
	lastAt    time.Time
	rate      float64 // bytes/sec, exponentially smoothed
}

// NewFileOpView returns a list widget that shows a labelled progress bar per
// active file operation, with a live byte count and transfer speed.
func NewFileOpView(ops *fs.Operations) *widget.List {
	v := &opView{ops: ops, samples: map[*fs.Op]*sample{}}
	v.list = widget.NewList(
		func() int { return len(v.ops.Active()) },
		func() fyne.CanvasObject {
			pause := widget.NewButtonWithIcon("", theme.MediaPauseIcon(), nil)
			stop := widget.NewButtonWithIcon("", theme.MediaStopIcon(), nil)
			buttons := container.NewHBox(pause, stop)
			return container.NewBorder(widget.NewLabel("template"), nil, nil, buttons, widget.NewProgressBar())
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			active := v.ops.Active()
			if i >= len(active) {
				return
			}
			op := active[i]
			// NewBorder does not preserve the argument order in Objects, so match
			// each child by type rather than by index.
			var label *widget.Label
			var bar *widget.ProgressBar
			var buttons *fyne.Container
			for _, child := range o.(*fyne.Container).Objects {
				switch w := child.(type) {
				case *widget.Label:
					label = w
				case *widget.ProgressBar:
					bar = w
				case *fyne.Container:
					buttons = w
				}
			}
			label.SetText(v.rowText(op))
			bar.SetValue(float64(op.PctComplete()))

			pause := buttons.Objects[0].(*widget.Button)
			stop := buttons.Objects[1].(*widget.Button)
			if op.Pausable() {
				pause.Enable()
				if op.IsPaused() {
					pause.SetIcon(theme.MediaPlayIcon())
					pause.OnTapped = func() { op.Resume(); v.list.Refresh() }
				} else {
					pause.SetIcon(theme.MediaPauseIcon())
					pause.OnTapped = func() { op.Pause(); v.list.Refresh() }
				}
			} else {
				pause.SetIcon(theme.MediaPauseIcon())
				pause.OnTapped = nil
				pause.Disable()
			}
			stop.OnTapped = func() { op.Cancel() }
		})
	go v.tick()
	return v.list
}

// tick periodically resamples per-op speed and repaints while any op is active.
func (v *opView) tick() {
	t := time.NewTicker(refreshInterval)
	defer t.Stop()
	for range t.C {
		active := v.ops.Active()
		if len(active) == 0 {
			continue
		}
		v.updateRates(active)
		fyne.Do(v.list.Refresh)
	}
}

// updateRates refreshes the smoothed transfer rate for each active op and drops
// samples for ops that have finished.
func (v *opView) updateRates(active []*fs.Op) {
	now := time.Now()
	v.mu.Lock()
	defer v.mu.Unlock()
	live := make(map[*fs.Op]bool, len(active))
	for _, op := range active {
		live[op] = true
		s := v.samples[op]
		if s == nil {
			v.samples[op] = &sample{lastBytes: op.TotalBytesRead, lastAt: now}
			continue
		}
		dt := now.Sub(s.lastAt).Seconds()
		if dt <= 0 {
			continue
		}
		inst := float64(op.TotalBytesRead-s.lastBytes) / dt
		if s.rate == 0 {
			s.rate = inst
		} else {
			s.rate = 0.6*s.rate + 0.4*inst // EWMA smoothing
		}
		s.lastBytes = op.TotalBytesRead
		s.lastAt = now
	}
	for op := range v.samples {
		if !live[op] {
			delete(v.samples, op)
		}
	}
}

// rowText builds the per-op status line: filename, byte progress, and speed.
func (v *opView) rowText(op *fs.Op) string {
	v.mu.Lock()
	var rate float64
	if s := v.samples[op]; s != nil {
		rate = s.rate
	}
	v.mu.Unlock()

	name := op.A.Name
	if op.TotalSize > 0 {
		line := fmt.Sprintf("%s  —  %s / %s", name, humanBytes(op.TotalBytesRead), humanBytes(op.TotalSize))
		if op.IsPaused() {
			line += "  ·  paused"
		} else if rate > 0 && op.Status == fs.StatusRunning {
			line += "  ·  " + humanRate(rate)
		}
		return line
	}
	return fmt.Sprintf("%s  —  %s", name, humanBytes(op.TotalBytesRead))
}

// humanBytes formats a byte count with a binary (KiB-scale) unit.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// humanRate formats a bytes-per-second rate.
func humanRate(bytesPerSec float64) string {
	return humanBytes(int64(bytesPerSec)) + "/s"
}
