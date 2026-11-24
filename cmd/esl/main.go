// Command esl is Enter Space Light: rclone remotes as native drives, plus
// scheduled syncs, for Windows and Linux.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/blaineam/enter-space-light/internal/client"
	"github.com/blaineam/enter-space-light/internal/daemon"
	"github.com/blaineam/enter-space-light/internal/desktop"
	"github.com/blaineam/enter-space-light/internal/paths"
	"github.com/blaineam/enter-space-light/internal/rclonecli"
	"github.com/blaineam/enter-space-light/internal/version"
)

const usage = `Enter Space Light %s — rclone remotes as native drives, plus scheduled syncs.

Usage:
  esl                      open the window (starting Enter Space Light if needed)
  esl run [--no-tray] [--open]
                           run in the foreground (this is what starts at sign-in)
  esl open                 open the settings window
  esl status [--json]      show drives and syncs
  esl connect <drive>      mount a drive (by name or id)
  esl disconnect <drive>   unmount a drive
  esl sync <job>           run a sync job now
  esl stop <job>           stop a running sync job
  esl autostart on|off [--headless]
                           start at sign-in (--headless: systemd user service, no tray)
  esl quit                 disconnect drives and exit
  esl rclone <args…>       run any rclone command with the same rclone.conf
                           (e.g. "esl rclone config" to add or edit remotes)
  esl version

Settings: %s
Logs:     %s
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"open"}
	}
	switch args[0] {
	case "rclone":
		rclonecli.Run(args[1:], paths.RcloneConfig())
	case "run", "daemon":
		os.Exit(cmdRun(args[1:]))
	case "open":
		os.Exit(cmdOpen())
	case "status":
		os.Exit(cmdStatus(args[1:]))
	case "connect", "mount":
		os.Exit(cmdMount(args[1:], true))
	case "disconnect", "unmount":
		os.Exit(cmdMount(args[1:], false))
	case "sync":
		os.Exit(cmdJob(args[1:], "run"))
	case "stop":
		os.Exit(cmdJob(args[1:], "stop"))
	case "autostart":
		os.Exit(cmdAutostart(args[1:]))
	case "quit":
		os.Exit(cmdQuit())
	case "version", "--version", "-V":
		fmt.Printf("esl %s\n", version.Version)
	case "help", "-h", "--help":
		fmt.Printf(usage, version.Version, paths.SettingsFile(), paths.LogFile())
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		fmt.Fprintf(os.Stderr, usage, version.Version, paths.SettingsFile(), paths.LogFile())
		os.Exit(2)
	}
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "esl:", err)
	return 1
}

func cmdRun(args []string) int {
	fl := flag.NewFlagSet("run", flag.ContinueOnError)
	noTray := fl.Bool("no-tray", false, "don't show a tray icon (servers, systemd)")
	open := fl.Bool("open", false, "open the settings window once started")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	err := daemon.Run(daemon.Options{Tray: !*noTray, OpenUI: *open})
	if errors.Is(err, daemon.ErrAlreadyRunning) {
		// A second launch (double-click, autostart racing) just shows the
		// running instance.
		if *open || !*noTray {
			return cmdOpen()
		}
		fmt.Fprintln(os.Stderr, "esl: already running")
		return 0
	}
	if err != nil {
		return fail(err)
	}
	return 0
}

// cmdOpen opens the UI of the running daemon, starting one if needed.
func cmdOpen() int {
	c, err := client.Connect()
	if err == nil {
		u, err := c.LoginURL()
		if err != nil {
			return fail(err)
		}
		if err := desktop.OpenURL(u); err != nil {
			return fail(err)
		}
		return 0
	}
	exe := desktop.Executable()
	if runtime.GOOS == "windows" {
		if gui := filepath.Join(filepath.Dir(exe), "EnterSpaceLight.exe"); fileExists(gui) {
			exe = gui
		}
	}
	cmd := exec.Command(exe, "run", "--open")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	detachProcess(cmd)
	if err := cmd.Start(); err != nil {
		return fail(err)
	}
	_ = cmd.Process.Release()
	fmt.Println("Starting Enter Space Light…")
	return 0
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

type statusDoc struct {
	Version string `json:"version"`
	Rclone  string `json:"rclone"`
	Config  struct {
		Path   string `json:"path"`
		Locked bool   `json:"locked"`
	} `json:"config"`
	WinFsp struct {
		Required  bool `json:"required"`
		Installed bool `json:"installed"`
	} `json:"winfsp"`
	Mounts []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Remote string `json:"remote"`
		State  struct {
			Status     string `json:"status"`
			MountPoint string `json:"mountPoint"`
			Error      string `json:"error"`
		} `json:"state"`
	} `json:"mounts"`
	Jobs []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		Source  string `json:"source"`
		Dest    string `json:"dest"`
		Enabled bool   `json:"enabled"`
		State   struct {
			Running *struct {
				Reason string `json:"reason"`
			} `json:"running"`
			Last *struct {
				OK       bool      `json:"ok"`
				Error    string    `json:"error"`
				Finished time.Time `json:"finished"`
			} `json:"last"`
		} `json:"state"`
	} `json:"jobs"`
}

func cmdStatus(args []string) int {
	c, err := client.Connect()
	if err != nil {
		return fail(err)
	}
	if len(args) > 0 && args[0] == "--json" {
		var raw json.RawMessage
		if err := c.Do(http.MethodGet, "/api/status", nil, &raw); err != nil {
			return fail(err)
		}
		fmt.Println(string(raw))
		return 0
	}
	var s statusDoc
	if err := c.Do(http.MethodGet, "/api/status", nil, &s); err != nil {
		return fail(err)
	}
	fmt.Printf("Enter Space Light %s (rclone %s)\nrclone.conf: %s", s.Version, s.Rclone, s.Config.Path)
	if s.Config.Locked {
		fmt.Print("  [LOCKED — run `esl open` to unlock]")
	}
	fmt.Println()
	if s.WinFsp.Required && !s.WinFsp.Installed {
		fmt.Println("WinFsp is not installed — run `esl open` and install it.")
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "\nDRIVE\tREMOTE\tSTATUS\tWHERE")
	for _, m := range s.Mounts {
		st := m.State.Status
		if m.State.Error != "" {
			st += " (" + m.State.Error + ")"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", m.Name, m.Remote, st, m.State.MountPoint)
	}
	fmt.Fprintln(w, "\nSYNC\tKIND\tFROM → TO\tLAST")
	for _, j := range s.Jobs {
		last := "never"
		switch {
		case j.State.Running != nil:
			last = "running (" + j.State.Running.Reason + ")"
		case j.State.Last != nil && j.State.Last.OK:
			last = "ok " + j.State.Last.Finished.Local().Format("Jan 2 15:04")
		case j.State.Last != nil:
			last = "FAILED: " + j.State.Last.Error
		}
		if !j.Enabled {
			last += " [paused]"
		}
		fmt.Fprintf(w, "%s\t%s\t%s → %s\t%s\n", j.Name, j.Kind, j.Source, j.Dest, last)
	}
	_ = w.Flush()
	return 0
}

// find resolves a name or id among mounts or jobs.
func find(c *client.Client, kind, key string) (string, error) {
	var s statusDoc
	if err := c.Do(http.MethodGet, "/api/status", nil, &s); err != nil {
		return "", err
	}
	var matches []string
	if kind == "mount" {
		for _, m := range s.Mounts {
			if m.ID == key || strings.EqualFold(m.Name, key) || m.Remote == key {
				matches = append(matches, m.ID)
			}
		}
	} else {
		for _, j := range s.Jobs {
			if j.ID == key || strings.EqualFold(j.Name, key) {
				matches = append(matches, j.ID)
			}
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no %s called %q (see `esl status`)", map[string]string{"mount": "drive", "job": "sync"}[kind], key)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("%q matches more than one; use the id from `esl status --json`", key)
	}
}

func cmdMount(args []string, connect bool) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: esl connect|disconnect <drive>")
		return 2
	}
	c, err := client.Connect()
	if err != nil {
		return fail(err)
	}
	id, err := find(c, "mount", args[0])
	if err != nil {
		return fail(err)
	}
	action := "disconnect"
	if connect {
		action = "connect"
	}
	if err := c.Do(http.MethodPost, "/api/mounts/"+id+"/"+action, nil, nil); err != nil {
		return fail(err)
	}
	if !connect {
		return 0
	}
	// Wait (briefly) for the outcome so scripts can rely on the exit code.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var s statusDoc
		if err := c.Do(http.MethodGet, "/api/status", nil, &s); err != nil {
			return fail(err)
		}
		for _, m := range s.Mounts {
			if m.ID != id {
				continue
			}
			switch m.State.Status {
			case "mounted":
				fmt.Printf("%s mounted at %s\n", m.Name, m.State.MountPoint)
				return 0
			case "retrying", "locked":
				return fail(fmt.Errorf("%s: %s (still retrying in the background)", m.Name, m.State.Error))
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fail(errors.New("still mounting; check `esl status`"))
}

func cmdJob(args []string, action string) int {
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "usage: esl %s <job>\n", map[string]string{"run": "sync", "stop": "stop"}[action])
		return 2
	}
	c, err := client.Connect()
	if err != nil {
		return fail(err)
	}
	id, err := find(c, "job", args[0])
	if err != nil {
		return fail(err)
	}
	if err := c.Do(http.MethodPost, "/api/jobs/"+id+"/"+action, nil, nil); err != nil {
		return fail(err)
	}
	return 0
}

func cmdAutostart(args []string) int {
	if len(args) < 1 || (args[0] != "on" && args[0] != "off") {
		fmt.Fprintln(os.Stderr, "usage: esl autostart on|off [--headless]")
		return 2
	}
	on := args[0] == "on"
	headless := len(args) > 1 && args[1] == "--headless"
	if err := desktop.SetAutostart(on, headless); err != nil {
		return fail(err)
	}
	return 0
}

func cmdQuit() int {
	c, err := client.Connect()
	if err != nil {
		return fail(err)
	}
	if err := c.Do(http.MethodPost, "/api/quit", nil, nil); err != nil {
		return fail(err)
	}
	// Return only once drives are unmounted and the process is gone, so
	// `esl quit && esl run` (or an upgrade script) never races the lock.
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(paths.APIFile()); os.IsNotExist(err) {
			return 0
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fail(errors.New("still shutting down after 45s (a drive may be busy); check `esl status`"))
}
