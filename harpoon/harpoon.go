package harpoon

import (
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/joshmedeski/sesh/v2/tmux"
)

// OptionName is the tmux global user option that backs the pinned list. Dies on tmux kill-server
const OptionName = "@sesh_harpoon"

// Slot is one numbered position in sesh-harpoon.
// Positions are never renumbered, so a slot keeps its identity for the
// lifetime of the tmux server.
type Slot struct {
	Position int
	Name     string
}

// Positions are 1-indexed everywhere
// Positions are stable: a keybind to slot N always triggers the session stored in slot N
type Harpoon interface {
	// List returns only occupied slots, sorted by position
	List() ([]Slot, error)
	// Add pins session name at the exact index position, returning pos. If the
	// position is already occupied it is rebound to the new name.
	Add(name string, pos int) (int, error)
	// Remove vacates a position. Other positions are untouched.
	Remove(position int) error
	// Get returns the session name at the position, or an error if the slot is vacant.
	Get(position int) (string, error)
}

type RealHarpoon struct {
	tmux tmux.Tmux
}

func NewHarpoon(t tmux.Tmux) Harpoon {
	return &RealHarpoon{tmux: t}
}

func (h *RealHarpoon) getSlots() ([]Slot, error) {
	slots, err := h.readSlots()
	if err != nil {
		return nil, fmt.Errorf("harpoon: %w", err)
	}
	return slots, nil
}

// parse decodes the option value into slots.
func parse(raw string) []Slot {
	if raw == "" {
		return []Slot{}
	}
	byPosition := make(map[int]string)
	for line := range strings.SplitSeq(raw, "\n") {
		if line == "" {
			continue
		}
		// Split on the first colon only
		posField, name, ok := strings.Cut(line, ":")
		if !ok {
			slog.Warn("harpoon: ignoring malformed slot line", "line", line)
			continue
		}
		position, err := strconv.Atoi(posField)
		if err != nil || position < 1 {
			slog.Warn("harpoon: ignoring slot line with invalid position", "line", line)
			continue
		}

		byPosition[position] = name
	}
	return sortedSlots(byPosition)
}

// sortedSlots sorts the session map into acseding order
func sortedSlots(byPosition map[int]string) []Slot {
	slots := make([]Slot, 0, len(byPosition))
	for position, name := range byPosition {
		slots = append(slots, Slot{Position: position, Name: name})
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i].Position < slots[j].Position })
	return slots
}

// serialize renders slots back into the "position:name" newline-delimited
// encoding. Positions are sorted so the stored value is deterministic.
func serialize(slots []Slot) string {
	ordered := make([]Slot, len(slots))
	copy(ordered, slots)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Position < ordered[j].Position })
	lines := make([]string, 0, len(ordered))
	for _, s := range ordered {
		lines = append(lines, strconv.Itoa(s.Position)+":"+s.Name)
	}
	return strings.Join(lines, "\n")
}

// maxPosition reports the highest occupied position, or 0 when the list is
// empty.
func maxPosition(slots []Slot) int {
	max := 0
	for _, s := range slots {
		if s.Position > max {
			max = s.Position
		}
	}
	return max
}

// readSlots returns a sorted list of []Slot (index : name)
func (h *RealHarpoon) readSlots() ([]Slot, error) {
	raw, err := h.tmux.GetOption(OptionName)
	if err != nil {
		return nil, fmt.Errorf("reading tmux option %s: %w", OptionName, err)
	}
	return parse(raw), nil
}

// write persists the complete list in one set-option call.
// An empty slice writes "", which reads back as zero slots.
func (h *RealHarpoon) write(slots []Slot) error {
	if _, err := h.tmux.SetOption(OptionName, serialize(slots)); err != nil {
		return fmt.Errorf("writing tmux option %s: %w", OptionName, err)
	}
	return nil
}

func (h *RealHarpoon) List() ([]Slot, error) {
	slots, _ := h.getSlots()
	return slots, nil
}

// Add pins the session at the exact index and returns pos. An occupied
// position is rebound in place: only that slot's name changes, every other slot keeps its position.
func (h *RealHarpoon) Add(name string, pos int) (int, error) {
	if pos < 1 {
		return 0, fmt.Errorf("harpoon add: position must be >= 1, got %d", pos)
	}
	if strings.TrimSpace(name) == "" {
		return 0, fmt.Errorf("harpoon add: session name must not be blank")
	}
	slots, _ := h.getSlots()
	rebound := false
	// overwrite session at the given index if position is already taken
	for i := range slots {
		if slots[i].Position == pos {
			slots[i].Name = name
			rebound = true
			break
		}
	}
	// if slot not already taken, just add to the list at the given position
	if !rebound {
		slots = append(slots, Slot{Position: pos, Name: name})
	}
	if err := h.write(slots); err != nil {
		return 0, fmt.Errorf("harpoon add: %w", err)
	}
	slog.Debug("harpoon add", "name", name, "position", pos, "rebound", rebound)
	return pos, nil
}

// Remove vacates position, leaving a hole; later positions are untouched.
// A slot that dies on its own does not trigger this. Nothing here prunes or
// compacts, so a killed session keeps its slot.
func (h *RealHarpoon) Remove(position int) error {
	slots, _ := h.getSlots()

	if position < 1 || position > maxPosition(slots) {
		return fmt.Errorf("harpoon remove: no slot at position %d", position)
	}
	remaining := make([]Slot, 0, len(slots))
	found := false
	for i := range slots {
		if slots[i].Position == position {
			found = true
			continue
		}
		remaining = append(remaining, slots[i])
	}
	if !found {
		return fmt.Errorf("harpoon remove: slot %d is vacant", position)
	}
	if err := h.write(remaining); err != nil {
		return fmt.Errorf("harpoon remove: %w", err)
	}
	slog.Debug("harpoon remove", "position", position, "remaining", len(remaining))
	return nil
}

// Get returns the session name at the index position.
func (h *RealHarpoon) Get(position int) (string, error) {
	slots, _ := h.getSlots()

	if position < 1 || position > maxPosition(slots) {
		return "", fmt.Errorf("harpoon get: no slot at position %d", position)
	}
	for _, s := range slots {
		if s.Position == position {
			return s.Name, nil
		}
	}
	return "", fmt.Errorf("harpoon get: slot %d is vacant", position)
}
