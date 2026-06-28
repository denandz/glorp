package views

import (
	"container/ring"
	"fmt"
	"sort"
	"strconv"

	"github.com/denandz/glorp/modifier"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// WebSocketView - struct that holds the WebSocket view elements
type WebSocketView struct {
	Layout     *tview.Pages
	Table      *tview.Table
	payloadBox *TextPrimitive
	Logger     *modifier.Logger
}

// GetView returns the title and top-level primitive
func (view *WebSocketView) GetView() (title string, content tview.Primitive) {
	return "WebSocket", view.Layout
}

// Init initializes the WebSocket view
func (view *WebSocketView) Init(app *tview.Application, logger *modifier.Logger, channel chan modifier.Notification) {
	var id string

	view.Logger = logger

	view.Layout = tview.NewPages()
	mainLayout := tview.NewFlex()
	mainLayout.SetDirection(tview.FlexRow)

	view.Table = tview.NewTable()
	view.Table.SetFixed(1, 1)
	view.Table.SetBorders(false).SetSeparator(tview.Borders.Vertical)
	view.Table.SetBorderPadding(0, 0, 0, 0)
	view.Table.SetSelectable(true, false)

	view.Table.SetCell(0, 1, tview.NewTableCell("ID").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))
	view.Table.SetCell(0, 2, tview.NewTableCell("IO").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))
	view.Table.SetCell(0, 3, tview.NewTableCell("URL").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false).SetAlign(tview.AlignCenter))
	view.Table.SetCell(0, 4, tview.NewTableCell("Opcode").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false).SetAlign(tview.AlignCenter))
	view.Table.SetCell(0, 5, tview.NewTableCell("Size").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))
	view.Table.SetCell(0, 6, tview.NewTableCell("Date").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))

	view.payloadBox = NewTextPrimitive()
	view.payloadBox.SetBorder(true)
	view.payloadBox.SetTitle("Payload")

	reqRespFlexView := tview.NewFlex()
	reqRespFlexView.AddItem(view.payloadBox, 0, 1, false)

	mainLayout.AddItem(view.Table, 0, 2, true)
	mainLayout.AddItem(reqRespFlexView, 0, 3, false)

	view.Layout.AddPage("mainlayout", mainLayout, true, true)

	// focus ring
	items := []tview.Primitive{view.Table, view.payloadBox}
	focusRing := ring.New(len(items))
	for i := range items {
		focusRing.Value = items[i]
		focusRing = focusRing.Next()
	}

	view.Table.SetSelectionChangedFunc(func(row int, column int) {
		if row > view.Table.GetRowCount() || row < 0 {
			return
		}

		view.payloadBox.Clear()

		id = view.Table.GetCell(row, 1).Text
		if entry := view.Logger.GetWebSocketEntry(id); entry != nil {
			fmt.Fprintf(view.payloadBox, "%s", entry.Payload)
			fmt.Fprint(view.payloadBox, "\u2800")
			view.payloadBox.ScrollToBeginning()
		}
	})

	view.Table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Rune() {
		case 'g':
			view.Table.ScrollToBeginning()
		case 'G':
			view.Table.ScrollToEnd()
		}
		return event
	})

	mainLayout.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTAB:
			focusRing = focusRing.Next()
			app.SetFocus(focusRing.Value.(tview.Primitive))
		case tcell.KeyBacktab:
			focusRing = focusRing.Prev()
			app.SetFocus(focusRing.Value.(tview.Primitive))
		}
		return event
	})

	view.wsReceiver(app, channel)
}

func (view *WebSocketView) wsReceiver(app *tview.Application, channel chan modifier.Notification) {
	go func() {
		for elem := range channel {
			entry := view.Logger.GetWebSocketEntry(elem.ID)
			if entry != nil && view.Table != nil {
				app.QueueUpdateDraw(func() {
					n := view.Table.GetRowCount()
					view.AddEntry(entry)

					// If we're on the last entry, automatically scroll
					if app.GetFocus() == view.Table {
						if r, _ := view.Table.GetSelection(); r == n-1 {
							view.Table.Select(n, 0)
						}
					}
				})
			}
		}
	}()
}

// AddEntry adds a WebSocket entry to the table
func (view *WebSocketView) AddEntry(e *modifier.WebSocketEntry) {
	n := view.Table.GetRowCount()

	url := e.URL
	if len(url) > 100 {
		url = string([]rune(e.URL)[0:100])
	}

	dir := "error"
	dirColor := tcell.ColorRed

	if e.Direction == "received" {
		dir = "<<<"
		dirColor = tcell.ColorGreen
	}

	if e.Direction == "sent" {
		dir = ">>>"
		dirColor = tcell.ColorYellow
	}

	opcode := modifier.OpcodeToString(e.Opcode)

	view.Table.SetCell(n, 1, tview.NewTableCell(e.ID).SetSelectable(false))
	view.Table.SetCell(n, 2, tview.NewTableCell(dir).SetTextColor(dirColor).SetAlign(tview.AlignCenter))
	view.Table.SetCell(n, 3, tview.NewTableCell(url).SetExpansion(1))
	view.Table.SetCell(n, 4, tview.NewTableCell(opcode))
	view.Table.SetCell(n, 5, tview.NewTableCell(strconv.Itoa(len(e.Payload))))
	view.Table.SetCell(n, 6, tview.NewTableCell(e.Timestamp.Format("02-01-2006 15:04:05")))
}

// ReloadTable clears the table and reloads all WS entries
func (view *WebSocketView) ReloadTable() {
	view.Table.Clear()

	view.Table.SetCell(0, 1, tview.NewTableCell("ID").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))
	view.Table.SetCell(0, 2, tview.NewTableCell("IO").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false).SetAlign(tview.AlignCenter))
	view.Table.SetCell(0, 3, tview.NewTableCell("URL").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false).SetAlign(tview.AlignCenter))
	view.Table.SetCell(0, 4, tview.NewTableCell("Opcode").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))
	view.Table.SetCell(0, 5, tview.NewTableCell("Size").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))
	view.Table.SetCell(0, 6, tview.NewTableCell("Date").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))

	var entries []*modifier.WebSocketEntry
	for _, entry := range view.Logger.GetWebSocketEntries() {
		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})

	for _, v := range entries {
		view.AddEntry(v)
	}
}
