// Package tray is the notification-area icon: a status line, one entry per
// drive and per sync job, and the way into the settings window.
package tray

// Model is everything the menu shows.
type Model struct {
	Status     string
	Alert      bool
	Locked     bool
	FailedSeen bool
	Mounts     []MountItem
	Jobs       []JobItem
}

// MountItem is one drive.
type MountItem struct {
	ID      string
	Name    string
	Path    string
	Want    bool
	Mounted bool
	Problem bool
}

// JobItem is one sync job.
type JobItem struct {
	ID         string
	Name       string
	Running    bool
	Failed     bool
	NeedsDrive bool
}

// Actions are the menu's callbacks.
type Actions struct {
	Open     func()
	OpenPath func(string)
	Mount    func(id string, want bool)
	RunJob   func(id string)
	StopJob  func(id string)
	Seen     func() // the user opened the menu/UI; clear the failure badge
	Quit     func()
}
