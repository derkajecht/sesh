package harpoon

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/joshmedeski/sesh/v2/tmux"
)

// sequence returns a fake GetOption implementation that hands back values in
// order, one per call. It lets a single test model the read-modify-write cycle
// of several Add/Remove calls without re-stubbing the same argument each time.
func sequence(values ...string) func(string) (string, error) {
	i := 0
	return func(string) (string, error) {
		value := values[i]
		i++
		return value, nil
	}
}

func TestAddPinsAtExactPositionsAndListReturnsInOrder(t *testing.T) {
	tm := tmux.NewMockTmux(t)
	tm.EXPECT().GetOption(OptionName).RunAndReturn(sequence(
		"",
		"1:alpha",
		"1:alpha\n2:beta",
		"1:alpha\n2:beta\n3:gamma",
	))
	tm.EXPECT().SetOption(OptionName, "1:alpha").Return("", nil)
	tm.EXPECT().SetOption(OptionName, "1:alpha\n2:beta").Return("", nil)
	tm.EXPECT().SetOption(OptionName, "1:alpha\n2:beta\n3:gamma").Return("", nil)

	h := NewHarpoon(tm)
	pos, err := h.Add("alpha", 1)
	require.NoError(t, err)
	assert.Equal(t, 1, pos)
	pos, err = h.Add("beta", 2)
	require.NoError(t, err)
	assert.Equal(t, 2, pos)
	pos, err = h.Add("gamma", 3)
	require.NoError(t, err)
	assert.Equal(t, 3, pos)

	slots, err := h.List()
	require.NoError(t, err)
	assert.Equal(t, []Slot{{1, "alpha"}, {2, "beta"}, {3, "gamma"}}, slots)
}

// TestRemoveLeavesHoleAndDoesNotShiftNeighbours is THE invariant: removing slot
// 2 must leave "gamma" at slot 3, so the keybind for slot 3 keeps triggering
// "gamma" unless changed by the user.
func TestRemoveLeavesHoleAndDoesNotShiftNeighbours(t *testing.T) {
	tm := tmux.NewMockTmux(t)
	tm.EXPECT().GetOption(OptionName).RunAndReturn(sequence(
		"1:alpha\n2:beta\n3:gamma", // Remove reads
		"1:alpha\n3:gamma",         // List reads back
	))
	tm.EXPECT().SetOption(OptionName, "1:alpha\n3:gamma").Return("", nil)

	h := NewHarpoon(tm)
	require.NoError(t, h.Remove(2))

	slots, err := h.List()
	require.NoError(t, err)
	assert.Equal(t, []Slot{{1, "alpha"}, {3, "gamma"}}, slots)
}

func TestAddRebindsOccupiedSlotAndLeavesOthersAlone(t *testing.T) {
	tm := tmux.NewMockTmux(t)
	tm.EXPECT().GetOption(OptionName).RunAndReturn(sequence(
		"1:alpha\n2:beta",
		"1:alpha\n2:new",
	))
	tm.EXPECT().SetOption(OptionName, "1:alpha\n2:new").Return("", nil)

	h := NewHarpoon(tm)
	pos, err := h.Add("new", 2)
	require.NoError(t, err)
	assert.Equal(t, 2, pos)

	slots, err := h.List()
	require.NoError(t, err)
	assert.Equal(t, []Slot{{1, "alpha"}, {2, "new"}}, slots)
}

// TestVacantSlotEncodingRoundTrips proves the position:name format survives a
// write/read cycle with a gap
func TestVacantSlotEncodingRoundTrips(t *testing.T) {
	raw := "1:alpha\n3:gamma"
	slots := parse(raw)
	assert.Equal(t, []Slot{{1, "alpha"}, {3, "gamma"}}, slots)
	assert.Equal(t, raw, serialize(slots))
}

func TestParsePreservesNamesWithColons(t *testing.T) {
	slots := parse("2:some:name:with:colons")
	require.Len(t, slots, 1)
	assert.Equal(t, Slot{Position: 2, Name: "some:name:with:colons"}, slots[0])
}

func TestListUnsetOptionIsEmpty(t *testing.T) {
	tm := tmux.NewMockTmux(t)
	// A missing option (and a dead server) both come back as ("", nil).
	tm.EXPECT().GetOption(OptionName).Return("", nil)

	h := NewHarpoon(tm)
	slots, err := h.List()
	require.NoError(t, err)
	assert.Empty(t, slots)
	assert.NotNil(t, slots)
}

func TestListSkipsBlankLines(t *testing.T) {
	tm := tmux.NewMockTmux(t)
	tm.EXPECT().GetOption(OptionName).Return("\n1:alpha\n\n3:beta\n", nil)

	h := NewHarpoon(tm)
	slots, err := h.List()
	require.NoError(t, err)
	assert.Equal(t, []Slot{{1, "alpha"}, {3, "beta"}}, slots)
}

func TestListSortsByPosition(t *testing.T) {
	tm := tmux.NewMockTmux(t)
	tm.EXPECT().GetOption(OptionName).Return("5:five\n1:one\n3:three", nil)

	h := NewHarpoon(tm)
	slots, err := h.List()
	require.NoError(t, err)
	assert.Equal(t, []Slot{{1, "one"}, {3, "three"}, {5, "five"}}, slots)
}

func TestListPropagatesError(t *testing.T) {
	tm := tmux.NewMockTmux(t)
	tm.EXPECT().GetOption(OptionName).Return("", assert.AnError)

	h := NewHarpoon(tm)
	_, err := h.List()
	require.ErrorIs(t, err, assert.AnError)
	require.ErrorContains(t, err, "harpoon list")
}

func TestAddRejectsNonPositivePosition(t *testing.T) {
	tm := tmux.NewMockTmux(t) // no tmux calls expected

	h := NewHarpoon(tm)
	_, err := h.Add("alpha", 0)
	require.ErrorContains(t, err, "position must be >= 1")
	_, err = h.Add("alpha", -3)
	require.ErrorContains(t, err, "position must be >= 1")
}

func TestAddRejectsBlankName(t *testing.T) {
	tm := tmux.NewMockTmux(t) // no tmux calls expected

	h := NewHarpoon(tm)
	_, err := h.Add("   ", 1)
	require.ErrorContains(t, err, "must not be blank")
	_, err = h.Add("", 1)
	require.ErrorContains(t, err, "must not be blank")
}

func TestRemoveVacantVsNonexistent(t *testing.T) {
	t.Run("vacant hole inside the list", func(t *testing.T) {
		tm := tmux.NewMockTmux(t)
		tm.EXPECT().GetOption(OptionName).Return("1:alpha\n3:gamma", nil)

		h := NewHarpoon(tm)
		err := h.Remove(2)
		require.ErrorContains(t, err, "slot 2 is vacant")
	})

	t.Run("position never used", func(t *testing.T) {
		tm := tmux.NewMockTmux(t)
		tm.EXPECT().GetOption(OptionName).Return("1:alpha\n3:gamma", nil)

		h := NewHarpoon(tm)
		err := h.Remove(5)
		require.ErrorContains(t, err, "no slot at position 5")
	})

	t.Run("zero position", func(t *testing.T) {
		tm := tmux.NewMockTmux(t)
		tm.EXPECT().GetOption(OptionName).Return("1:alpha", nil)

		h := NewHarpoon(tm)
		err := h.Remove(0)
		require.ErrorContains(t, err, "no slot at position 0")
	})
}

func TestGetVacantVsNonexistent(t *testing.T) {
	t.Run("vacant hole inside the list", func(t *testing.T) {
		tm := tmux.NewMockTmux(t)
		tm.EXPECT().GetOption(OptionName).Return("1:alpha\n3:gamma", nil)

		h := NewHarpoon(tm)
		_, err := h.Get(2)
		require.ErrorContains(t, err, "slot 2 is vacant")
	})

	t.Run("position never used", func(t *testing.T) {
		tm := tmux.NewMockTmux(t)
		tm.EXPECT().GetOption(OptionName).Return("1:alpha\n3:gamma", nil)

		h := NewHarpoon(tm)
		_, err := h.Get(5)
		require.ErrorContains(t, err, "no slot at position 5")
	})

	t.Run("zero position", func(t *testing.T) {
		tm := tmux.NewMockTmux(t)
		tm.EXPECT().GetOption(OptionName).Return("1:alpha", nil)

		h := NewHarpoon(tm)
		_, err := h.Get(0)
		require.ErrorContains(t, err, "no slot at position 0")
	})

	t.Run("occupied", func(t *testing.T) {
		tm := tmux.NewMockTmux(t)
		tm.EXPECT().GetOption(OptionName).Return("3:gamma", nil)

		h := NewHarpoon(tm)
		name, err := h.Get(3)
		require.NoError(t, err)
		assert.Equal(t, "gamma", name)
	})
}

// Removing the only slot writes "", which reads back as zero slots. That is the
// intended round-trip (an empty option is equivalent to unset).
func TestRemoveLastSlotWritesEmptyValue(t *testing.T) {
	tm := tmux.NewMockTmux(t)
	tm.EXPECT().GetOption(OptionName).RunAndReturn(sequence(
		"1:alpha",
		"",
	))
	tm.EXPECT().SetOption(OptionName, "").Return("", nil)

	h := NewHarpoon(tm)
	require.NoError(t, h.Remove(1))

	slots, err := h.List()
	require.NoError(t, err)
	assert.Empty(t, slots)
}

func TestAddPropagatesWriteError(t *testing.T) {
	tm := tmux.NewMockTmux(t)
	tm.EXPECT().GetOption(OptionName).Return("", nil)
	tm.EXPECT().SetOption(OptionName, "1:alpha").Return("", assert.AnError)

	h := NewHarpoon(tm)
	_, err := h.Add("alpha", 1)
	require.ErrorIs(t, err, assert.AnError)
	require.ErrorContains(t, err, "harpoon add")
}
