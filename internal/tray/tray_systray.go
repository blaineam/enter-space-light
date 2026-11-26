//go:build windows || (linux && !android)

package tray

import (
	_ "embed"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"fyne.io/systray"
)

//go:embed icon.ico
var iconICO []byte

//go:embed icon.png
var iconPNG []byte

//go:embed icon-alert.ico
var alertICO []byte

//go:embed icon-alert.png
var alertPNG []byte

func icon(alert bool) []byte {
	if runtime.GOOS == "windows" {
		if alert {
			return alertICO
		}
		return iconICO
	}
	if alert {
		return alertPNG
	}
	return iconPNG
}

var (
	runMu   sync.Mutex
	running bool
)

// Run shows the tray icon and blocks until Quit. model is re-read whenever
// changes fires. The menu is rebuilt only when its shape changes (drives or
// jobs added/removed/renamed); otherwise titles update in place.
func Run(model func() Model, changes <-chan struct{}, a Actions, quit <-chan struct{}) {
	runMu.Lock()
	running = true
	runMu.Unlock()
	systray.Run(func() { onReady(model, changes, a, quit) }, func() {})
	runMu.Lock()
	running = false
	runMu.Unlock()
}

// Quit removes the icon and makes Run return.
func Quit() {
	runMu.Lock()
	r := running
	runMu.Unlock()
	if r {
		systray.Quit()
	}
}

type built struct {
	shape  string
	status *systray.MenuItem
	mounts map[string]*systray.MenuItem
	jobs   map[string]*systray.MenuItem
}

func shape(m Model) string {
	var b strings.Builder
	for _, x := range m.Mounts {
		fmt.Fprintf(&b, "m%s/%s/%v;", x.ID, x.Name, x.Mounted)
	}
	for _, x := range m.Jobs {
		fmt.Fprintf(&b, "j%s/%s/%v/%v;", x.ID, x.Name, x.Running, x.NeedsDrive)
	}
	fmt.Fprintf(&b, "l%v", m.Locked)
	return b.String()
}

func onReady(model func() Model, changes <-chan struct{}, a Actions, quit <-chan struct{}) {
	systray.SetTitle("Enter Space Light")
	systray.SetOnTapped(func() { // left click opens the window
		if a.Seen != nil {
			a.Seen()
		}
		a.Open()
	})
	var cur *built
	refresh := func() {
		m := model()
		systray.SetIcon(icon(m.Alert))
		systray.SetTooltip("Enter Space Light — " + m.Status)
		if cur == nil || cur.shape != shape(m) {
			systray.ResetMenu()
			cur = build(m, a)
			return
		}
		cur.status.SetTitle(m.Status)
		for _, x := range m.Mounts {
			if it := cur.mounts[x.ID]; it != nil {
				it.SetTitle(mountTitle(x))
			}
		}
		for _, x := range m.Jobs {
			if it := cur.jobs[x.ID]; it != nil {
				it.SetTitle(jobTitle(x))
			}
		}
	}
	refresh()
	go func() {
		for {
			select {
			case <-quit:
				return
			case <-changes:
				refresh()
			}
		}
	}()
}

func mountTitle(x MountItem) string {
	switch {
	case x.Problem:
		return "⚠ " + x.Name
	case x.Mounted:
		return "● " + x.Name
	default:
		return "○ " + x.Name
	}
}

func jobTitle(x JobItem) string {
	switch {
	case x.Running:
		return "⟳ " + x.Name
	case x.Failed:
		return "⚠ " + x.Name
	default:
		return x.Name
	}
}

func on(item *systray.MenuItem, fn func()) {
	go func() {
		for range item.ClickedCh { // closed by ResetMenu → goroutine exits
			fn()
		}
	}()
}

func build(m Model, a Actions) *built {
	b := &built{shape: shape(m), mounts: map[string]*systray.MenuItem{}, jobs: map[string]*systray.MenuItem{}}
	b.status = systray.AddMenuItem(m.Status, "")
	b.status.Disable()
	open := systray.AddMenuItem("Open Enter Space Light…", "Drives, syncs and settings")
	on(open, func() {
		if a.Seen != nil {
			a.Seen()
		}
		a.Open()
	})

	if len(m.Mounts) > 0 {
		systray.AddSeparator()
		for _, x := range m.Mounts {
			x := x
			it := systray.AddMenuItem(mountTitle(x), "")
			b.mounts[x.ID] = it
			if x.Mounted {
				show := it.AddSubMenuItem("Open in file manager", "")
				on(show, func() { a.OpenPath(x.Path) })
				un := it.AddSubMenuItem("Disconnect", "")
				on(un, func() { a.Mount(x.ID, false) })
			} else {
				mt := it.AddSubMenuItem("Connect", "")
				on(mt, func() { a.Mount(x.ID, true) })
			}
		}
	}
	if len(m.Jobs) > 0 {
		systray.AddSeparator()
		syncs := systray.AddMenuItem("Syncs", "")
		for _, x := range m.Jobs {
			x := x
			it := syncs.AddSubMenuItem(jobTitle(x), "")
			b.jobs[x.ID] = it
			switch {
			case x.Running:
				stop := it.AddSubMenuItem("Stop", "")
				on(stop, func() { a.StopJob(x.ID) })
			case x.NeedsDrive:
				w := it.AddSubMenuItem("Runs when its drive is connected", "")
				w.Disable()
			default:
				run := it.AddSubMenuItem("Run now", "")
				on(run, func() { a.RunJob(x.ID) })
			}
		}
	}
	systray.AddSeparator()
	q := systray.AddMenuItem("Quit (disconnects drives)", "")
	on(q, a.Quit)
	return b
}
