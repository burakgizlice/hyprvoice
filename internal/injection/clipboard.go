package injection

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

type clipboardBackend struct{}

func NewClipboardBackend() Backend {
	return &clipboardBackend{}
}

func (c *clipboardBackend) Name() string {
	return "clipboard"
}

func (c *clipboardBackend) Available() error {
	if _, err := exec.LookPath("wl-copy"); err != nil {
		return fmt.Errorf("wl-copy not found: %w (install wl-clipboard)", err)
	}

	if os.Getenv("WAYLAND_DISPLAY") == "" {
		return fmt.Errorf("WAYLAND_DISPLAY not set - clipboard operations require Wayland session")
	}

	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		return fmt.Errorf("XDG_RUNTIME_DIR not set - clipboard operations require proper session environment")
	}

	return nil
}

func (c *clipboardBackend) Inject(ctx context.Context, text string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := c.Available(); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "wl-copy")
	cmd.Stdin = strings.NewReader(text)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("wl-copy failed: %w", err)
	}

	// Best-effort auto-paste: if wtype is available, simulate Ctrl+V (or Ctrl+Shift+V for terminals).
	// If this fails the text is still in the clipboard, so we only log warnings.
	c.autoPaste(ctx)

	return nil
}

// autoPaste simulates a paste keystroke (Ctrl+V or Ctrl+Shift+V for terminals).
// Tries wtype first (Wayland-native), then ydotool (kernel-level via uinput).
func (c *clipboardBackend) autoPaste(ctx context.Context) {
	// Small delay so the compositor registers the new clipboard content
	// before we simulate the paste keystroke.
	select {
	case <-time.After(50 * time.Millisecond):
	case <-ctx.Done():
		log.Printf("clipboard: context cancelled before auto-paste")
		return
	}

	terminal := isTerminal()

	if c.pasteWithWtype(ctx, terminal) {
		return
	}
	if c.pasteWithYdotool(ctx, terminal) {
		return
	}

	log.Printf("clipboard: auto-paste unavailable (neither wtype nor ydotool could paste), text is in clipboard")
}

func (c *clipboardBackend) pasteWithWtype(ctx context.Context, terminal bool) bool {
	if _, err := exec.LookPath("wtype"); err != nil {
		return false
	}

	var cmd *exec.Cmd
	if terminal {
		cmd = exec.CommandContext(ctx, "wtype", "-M", "ctrl", "-M", "shift", "-k", "v")
	} else {
		cmd = exec.CommandContext(ctx, "wtype", "-M", "ctrl", "-k", "v")
	}

	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		log.Printf("clipboard: wtype paste failed: %v, stderr: %s", err, strings.TrimSpace(stderr.String()))
		return false
	}
	return true
}

// pasteWithYdotool simulates paste via ydotool using raw keycodes.
// KEY_LEFTCTRL=29, KEY_LEFTSHIFT=42, KEY_V=47
func (c *clipboardBackend) pasteWithYdotool(ctx context.Context, terminal bool) bool {
	if _, err := exec.LookPath("ydotool"); err != nil {
		return false
	}

	var args []string
	if terminal {
		// Ctrl+Shift+V: press ctrl, press shift, press v, release v, release shift, release ctrl
		args = []string{"key", "29:1", "42:1", "47:1", "47:0", "42:0", "29:0"}
	} else {
		// Ctrl+V: press ctrl, press v, release v, release ctrl
		args = []string{"key", "29:1", "47:1", "47:0", "29:0"}
	}

	cmd := exec.CommandContext(ctx, "ydotool", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		log.Printf("clipboard: ydotool paste failed: %v, stderr: %s", err, strings.TrimSpace(stderr.String()))
		return false
	}
	return true
}
