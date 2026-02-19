package injection

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// knownTerminalClasses maps window classes (lowercase) of common terminal emulators.
var knownTerminalClasses = map[string]bool{
	"kitty":                    true,
	"alacritty":                true,
	"foot":                     true,
	"footclient":               true,
	"wezterm":                  true,
	"ghostty":                  true,
	"org.wezfurlong.wezterm":   true,
	"com.mitchellh.ghostty":    true,
	"gnome-terminal":           true,
	"gnome-terminal-server":    true,
	"org.gnome.terminal":       true,
	"konsole":                  true,
	"org.kde.konsole":          true,
	"xfce4-terminal":           true,
	"terminator":               true,
	"tilix":                    true,
	"sakura":                   true,
	"st":                       true,
	"st-256color":              true,
	"urxvt":                    true,
	"xterm":                    true,
	"xterm-256color":           true,
	"rio":                      true,
	"contour":                  true,
	"blackbox":                 true,
	"com.raggesilver.blackbox": true,
}

// isTerminal checks whether the currently focused window is a terminal emulator.
// It tries compositor-specific methods to get the active window class:
//   - KDE Plasma: KWin script via DBus
//   - Hyprland: hyprctl activewindow -j
//
// Returns false if detection fails or the window is not a terminal.
func isTerminal() bool {
	class := activeWindowClass()
	if class == "" {
		return false
	}
	return knownTerminalClasses[strings.ToLower(class)]
}

// activeWindowClass returns the window class of the focused window, or "" on failure.
// Tries all known compositor detection methods without relying on env vars,
// since the process may be running under a systemd service with a minimal environment.
func activeWindowClass() string {
	if class := kwinActiveClass(); class != "" {
		return class
	}
	if class := hyprlandActiveClass(); class != "" {
		return class
	}
	return ""
}

// hyprlandActiveClass queries Hyprland via hyprctl for the active window class.
func hyprlandActiveClass() string {
	if _, err := exec.LookPath("hyprctl"); err != nil {
		return ""
	}

	out, err := exec.Command("hyprctl", "activewindow", "-j").Output()
	if err != nil {
		return ""
	}

	var win struct {
		Class        string `json:"class"`
		InitialClass string `json:"initialClass"`
	}
	if err := json.Unmarshal(out, &win); err != nil {
		return ""
	}

	if win.Class != "" {
		return win.Class
	}
	return win.InitialClass
}

// kwinActiveClass queries KDE KWin via a short-lived script that prints
// the active window's resourceClass. The script is loaded, executed, and
// unloaded via the KWin Scripting DBus interface. The output is captured
// from the script's stdout line (prefixed with a marker).
func kwinActiveClass() string {
	// Find qdbus binary
	var qdbus string
	for _, bin := range []string{"qdbus6", "qdbus"} {
		if p, err := exec.LookPath(bin); err == nil {
			qdbus = p
			break
		}
	}
	if qdbus == "" {
		return ""
	}

	// Verify KWin is reachable before creating temp files
	if err := exec.Command(qdbus, "org.kde.KWin", "/KWin", "org.kde.KWin.activeOutputName").Run(); err != nil {
		return ""
	}

	// Write the detection script to a temp file
	script, err := os.CreateTemp("", "hyprvoice_detect_*.js")
	if err != nil {
		return ""
	}
	scriptPath := script.Name()
	defer os.Remove(scriptPath)

	const pluginName = "hyprvoice_detect"
	fmt.Fprintf(script, "var w = workspace.activeWindow;\nif (w) { console.log('HYPRVOICE_WC:' + w.resourceClass); }\n")
	script.Close()

	// Ensure any stale instance is unloaded
	exec.Command(qdbus, "org.kde.KWin", "/Scripting", "org.kde.kwin.Scripting.unloadScript", pluginName).Run()

	// Load the script
	out, err := exec.Command(qdbus, "org.kde.KWin", "/Scripting", "org.kde.kwin.Scripting.loadScript", scriptPath, pluginName).Output()
	if err != nil {
		return ""
	}

	scriptID := strings.TrimSpace(string(out))
	if scriptID == "" || scriptID == "-1" {
		return ""
	}

	// Run it
	exec.Command(qdbus, "org.kde.KWin", "/Scripting", "org.kde.kwin.Scripting.start").Run()

	// Read the output from the journal (KWin script console.log goes to the journal)
	journalOut, err := exec.Command(
		"journalctl", "--user", "--no-pager", "--output=cat",
		"-n", "20", "-g", "HYPRVOICE_WC:",
	).Output()

	// Unload the script
	exec.Command(qdbus, "org.kde.KWin", "/Scripting", "org.kde.kwin.Scripting.unloadScript", pluginName).Run()

	if err != nil {
		return ""
	}

	// Parse the last matching line
	lines := strings.Split(strings.TrimSpace(string(journalOut)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if idx := strings.Index(line, "HYPRVOICE_WC:"); idx >= 0 {
			return strings.TrimSpace(line[idx+len("HYPRVOICE_WC:"):])
		}
	}

	return ""
}
