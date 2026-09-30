package seshcli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/joshmedeski/sesh/v2/harpoon"
	"github.com/joshmedeski/sesh/v2/model"
)

func NewHarpoonCommand(base *BaseDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "harpoon",
		Short: "Pin sessions and jump to them by slot",
		Long: `Manage a tmux-backed list of pinned sessions. Slot numbers are 1-indexed
and stable: each keybind points at a fixed slot, so removing a session vacates
its slot without moving any other session. The list lives in the tmux server, so
it disappears with tmux kill-server.`,
	}
	cmd.AddCommand(newHarpoonAddCommand(base))
	cmd.AddCommand(newHarpoonRemoveCommand(base))
	cmd.AddCommand(newHarpoonListCommand(base))
	cmd.AddCommand(newHarpoonGoCommand(base))
	return cmd
}

func newHarpoonAddCommand(base *BaseDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "add <name> <position>",
		Short: "Pin a session at an explicit slot",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			position, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("position must be an integer: %q", args[1])
			}

			deps, err := buildDeps(cmd, base)
			if err != nil {
				return err
			}

			store := harpoon.NewHarpoon(deps.Tmux)
			// Read first so a rebind can be reported as such. A failure here
			// just means the slot was empty; Add surfaces any real read error.
			previous, getErr := store.Get(position)

			if _, err := store.Add(name, position); err != nil {
				return err
			}
			if getErr == nil {
				fmt.Printf("rebound slot %d: %q -> %q\n", position, previous, name)
			} else {
				fmt.Printf("pinned %q at slot %d\n", name, position)
			}
			return nil
		},
	}
}

func newHarpoonRemoveCommand(base *BaseDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <position>",
		Short: "Vacate the slot at a position, leaving a hole",
		Args:  cobra.RangeArgs(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			position, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("position must be an integer: %q", args[0])
			}

			deps, err := buildDeps(cmd, base)
			if err != nil {
				return err
			}

			if err := harpoon.NewHarpoon(deps.Tmux).Remove(position); err != nil {
				return err
			}
			fmt.Printf("vacated slot %d\n", position)
			return nil
		},
	}
}

func newHarpoonListCommand(base *BaseDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List occupied slots in position order",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, err := buildDeps(cmd, base)
			if err != nil {
				return err
			}

			slots, err := harpoon.NewHarpoon(deps.Tmux).List()
			if err != nil {
				return err
			}
			if len(slots) == 0 {
				fmt.Println("no pinned sessions")
				return nil
			}
			// Vacant slots are intentionally omitted: a hole is real but has
			// nothing to print, and placeholder rows would be noisy.
			for _, slot := range slots {
				fmt.Printf("%d  %s\n", slot.Position, slot.Name)
			}
			return nil
		},
	}
}

func newHarpoonGoCommand(base *BaseDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "go <position>",
		Short: "Connect to the session pinned at a position",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			position, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("position must be an integer: %q", args[0])
			}

			deps, err := buildDeps(cmd, base)
			if err != nil {
				return err
			}

			name, err := harpoon.NewHarpoon(deps.Tmux).Get(position)
			if err != nil {
				return err
			}
			// Full resolve chain: a session that was killed but is still
			// resolvable (dir/config/zoxide) gets re-created. An unresolvable
			// name errors and the slot stays as it is.
			if _, err := deps.Connector.Connect(name, model.ConnectOpts{}); err != nil {
				return err
			}
			return nil
		},
	}
}
