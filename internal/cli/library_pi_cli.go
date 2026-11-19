package cli

import (
	"fmt"
	"strings"

	"github.com/yetone/magpie/internal/library"
)

func libraryUserPiCmd(args []string) error {
	if len(args) == 0 || len(args) == 1 && args[0] == "--json" {
		entries, err := library.UserPiEntries()
		if err != nil {
			return err
		}
		if len(args) == 1 {
			return printJSON(entries)
		}
		for _, e := range entries {
			state := "off"
			if e.On {
				state = "on"
			}
			fmt.Printf("  %s  %s  %s\n", e.Kind, state, e.Source)
		}
		return nil
	}
	const usage = "magpie library pi [--json]\nmagpie library pi add|rm|off|on package|extension <source>"
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		fmt.Println(usage)
		return nil
	}
	if len(args) != 3 || strings.HasPrefix(args[2], "-") {
		return fmt.Errorf("usage: %s", usage)
	}
	mode := library.PiOn
	switch args[0] {
	case "add", "on":
	case "off":
		mode = library.PiOff
	case "rm", "remove":
		mode = library.PiRemove
	default:
		return fmt.Errorf("usage: %s", usage)
	}
	if err := library.ChangeUserPi(args[1], args[2], mode, args[0] == "add"); err != nil {
		return err
	}
	fmt.Println(green.Render("✓"), args[0], args[1], args[2])
	return nil
}
