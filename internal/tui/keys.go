package tui

import "charm.land/bubbles/v2/key"

// keyMap is the one definition of every key: the handler matches against it and the
// footer and the help overlay read their text from it. A binding without help text
// is a second key for the one documented before it.
type keyMap struct {
	Interrupt, Quit, Help                   key.Binding
	Next, Prev, Dash, Sess, Agents, Factory key.Binding
	Down, Up, Top, Bottom, PageDown, PageUp key.Binding
	Open, Back                              key.Binding
	Range, Follow, Sort, Filter             key.Binding
	FilterKeep, FilterClear, Erase, Wipe    key.Binding
	CapEdit, CapSave, CapCancel             key.Binding
}

var keys = keyMap{
	Interrupt: key.NewBinding(key.WithKeys("ctrl+c")),
	Quit:      key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
	Help:      key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),

	Next:    key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next view")),
	Prev:    key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "previous view")),
	Dash:    key.NewBinding(key.WithKeys("1"), key.WithHelp("1 2 3 4", "dashboard · session · agents · factory")),
	Sess:    key.NewBinding(key.WithKeys("2")),
	Agents:  key.NewBinding(key.WithKeys("3")),
	Factory: key.NewBinding(key.WithKeys("4")),

	Down:     key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("↑↓/jk", "move")),
	Up:       key.NewBinding(key.WithKeys("k", "up")),
	Top:      key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g G", "first · last")),
	Bottom:   key.NewBinding(key.WithKeys("G", "end")),
	PageDown: key.NewBinding(key.WithKeys("pgdown", "ctrl+d"), key.WithHelp("pgdn pgup", "page")),
	PageUp:   key.NewBinding(key.WithKeys("pgup", "ctrl+u")),

	Open: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
	Back: key.NewBinding(key.WithKeys("esc", "backspace"), key.WithHelp("esc", "back")),

	Range:  key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "range")),
	Follow: key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "follow")),
	Sort:   key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort")),
	Filter: key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),

	FilterKeep:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "keep")),
	FilterClear: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear")),
	Erase:       key.NewBinding(key.WithKeys("backspace")),
	Wipe:        key.NewBinding(key.WithKeys("ctrl+u")),

	CapEdit:   key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "edit cap")),
	CapSave:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "save")),
	CapCancel: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
}

// footerKeys is what the footer advertises, most useful first; the footer drops from
// the middle when the terminal is narrow.
func (m *model) footerKeys() []key.Binding {
	switch {
	case m.filterIn:
		return []key.Binding{keys.FilterKeep, keys.FilterClear}
	case m.fac.editing:
		return []key.Binding{keys.CapSave, keys.CapCancel}
	case m.view == viewFactory:
		return []key.Binding{keys.Down, keys.CapEdit, keys.Range, keys.Next, keys.Help, keys.Quit}
	case m.view == viewSession:
		return []key.Binding{keys.Down, keys.Open, keys.Back, keys.Follow, keys.Next, keys.Help, keys.Quit}
	case m.view == viewAgents:
		return []key.Binding{keys.Down, keys.Range, keys.Next, keys.Back, keys.Help, keys.Quit}
	}
	return []key.Binding{keys.Down, keys.Open, keys.Filter, keys.Sort, keys.Range, keys.Follow, keys.Next, keys.Help, keys.Quit}
}

// helpGroups is the help overlay: every binding that has help text.
func helpGroups() [][]key.Binding {
	return [][]key.Binding{
		{keys.Down, keys.Top, keys.PageDown, keys.Open, keys.Back},
		{keys.Next, keys.Prev, keys.Dash},
		{keys.Range, keys.Sort, keys.Filter, keys.Follow, keys.CapEdit},
		{keys.Help, keys.Quit},
	}
}
