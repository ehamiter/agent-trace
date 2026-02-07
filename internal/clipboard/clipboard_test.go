package clipboard

import (
	"errors"
	"testing"
)

func TestSelectCommandDarwin(t *testing.T) {
	cmd, err := SelectCommand("darwin", func(name string) (string, error) {
		if name == "pbcopy" {
			return "/usr/bin/pbcopy", nil
		}
		return "", errors.New("not found")
	})
	if err != nil {
		t.Fatalf("expected command, got error: %v", err)
	}
	if cmd.Path != "/usr/bin/pbcopy" {
		t.Fatalf("unexpected path: %s", cmd.Path)
	}
	if len(cmd.Args) != 0 {
		t.Fatalf("did not expect args for pbcopy: %#v", cmd.Args)
	}
}

func TestSelectCommandLinuxPrefersWlCopy(t *testing.T) {
	cmd, err := SelectCommand("linux", func(name string) (string, error) {
		switch name {
		case "wl-copy":
			return "/usr/bin/wl-copy", nil
		case "xclip":
			return "/usr/bin/xclip", nil
		default:
			return "", errors.New("not found")
		}
	})
	if err != nil {
		t.Fatalf("expected command, got error: %v", err)
	}
	if cmd.Path != "/usr/bin/wl-copy" {
		t.Fatalf("expected wl-copy, got %q", cmd.Path)
	}
}

func TestSelectCommandLinuxFallsBackToXclip(t *testing.T) {
	cmd, err := SelectCommand("linux", func(name string) (string, error) {
		if name == "xclip" {
			return "/usr/bin/xclip", nil
		}
		return "", errors.New("not found")
	})
	if err != nil {
		t.Fatalf("expected command, got error: %v", err)
	}
	if cmd.Path != "/usr/bin/xclip" {
		t.Fatalf("expected xclip, got %q", cmd.Path)
	}
	if len(cmd.Args) != 2 || cmd.Args[0] != "-selection" || cmd.Args[1] != "clipboard" {
		t.Fatalf("unexpected xclip args: %#v", cmd.Args)
	}
}

func TestSelectCommandUnavailable(t *testing.T) {
	_, err := SelectCommand("linux", func(string) (string, error) {
		return "", errors.New("not found")
	})
	if !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("expected ErrToolNotFound, got %v", err)
	}
}
