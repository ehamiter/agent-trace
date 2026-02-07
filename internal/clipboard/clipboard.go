package clipboard

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
)

var ErrToolNotFound = errors.New("clipboard tool not found")

type Command struct {
	Path string
	Args []string
}

func SelectCommand(goos string, lookPath func(string) (string, error)) (Command, error) {
	switch goos {
	case "darwin":
		path, err := lookPath("pbcopy")
		if err != nil {
			return Command{}, ErrToolNotFound
		}
		return Command{Path: path}, nil
	case "linux":
		if path, err := lookPath("wl-copy"); err == nil {
			return Command{Path: path}, nil
		}
		if path, err := lookPath("xclip"); err == nil {
			return Command{Path: path, Args: []string{"-selection", "clipboard"}}, nil
		}
		return Command{}, ErrToolNotFound
	default:
		return Command{}, ErrToolNotFound
	}
}

func Copy(ctx context.Context, text string) error {
	cmdDef, err := SelectCommand(runtime.GOOS, exec.LookPath)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, cmdDef.Path, cmdDef.Args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("clipboard stdin: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return fmt.Errorf("start clipboard command: %w", err)
	}

	if _, err := stdin.Write([]byte(text)); err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		return fmt.Errorf("write clipboard data: %w", err)
	}
	_ = stdin.Close()

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("clipboard command failed: %w", err)
	}
	return nil
}
