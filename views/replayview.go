package views

import (
	"fmt"
	"glorp/replay"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/gdamore/tcell"
	"github.com/rivo/tview"
)

// ReplayView - struct that holds the main replayview elements
type ReplayView struct {
	Layout   *tview.Pages    // The main replay view, all others should be underneath Layout
	Table    *tview.Table    // the main table that lists all the replay items
	request  *tview.TextView // http request box
	response *tview.TextView // http response box

	host                *tview.InputField // host field input
	port                *tview.InputField // port input
	tls                 *tview.Checkbox   // check box for whether or not to negotiate TLS when rep
	updateContentLength *tview.Checkbox   // check box for whether to attempt a content-length auto update

	entries map[string]*replay.Request // list of request in the replayer - could probably use the row identifier as the key, support renaming
}

// AddItem method - can be called to add a replay.Request entry object
func (view *ReplayView) AddItem(r *replay.Request) {
	newID := r.ID
	if _, ok := view.entries[newID]; ok {
		i := 1
		newID = fmt.Sprintf("%s-%d", r.ID, i)
		for _, ok := view.entries[newID]; ok; _, ok = view.entries[newID] {
			i++
			newID = fmt.Sprintf("%s-%d", r.ID, i)
		}
	}
	log.Printf("[+] ReplayView - AddItem - Adding replay item with ID: %s\n", newID)
	view.entries[newID] = r

	rows := view.Table.GetRowCount()
	view.Table.SetCell(rows, 0, tview.NewTableCell(newID))
}

// RenameItem - change the name of an entry
func (view *ReplayView) RenameItem(r *replay.Request, newName string) {
	if _, ok := view.entries[newName]; ok {
		log.Println("[!] Replay entry with ID " + newName + " already exists")
		return
	}
	if _, ok := view.entries[r.ID]; ok && newName != r.ID {
		n := view.Table.GetRowCount()
		for i := 0; i < n; i++ {
			if view.Table.GetCell(i, 0).Text == r.ID {
				originalID := r.ID
				r.ID = newName

				// updating table
				view.Table.SetCell(i, 0, tview.NewTableCell(r.ID))

				// updating map
				view.entries[newName] = r
				delete(view.entries, originalID)
				break
			}
		}
	}
}

// GetView - should return a title and the top-level primitive
func (view *ReplayView) GetView() (title string, content tview.Primitive) {
	return "Replay", view.Layout
}

// Init - Initialization method for the replayer view
func (view *ReplayView) Init(app *tview.Application) {
	view.entries = make(map[string]*replay.Request)
	var id string // the currently selected replay item
	responseMeta := tview.NewTable()
	responseMeta.SetCell(0, 0, tview.NewTableCell("Size:").SetTextColor(tcell.ColorMediumPurple))
	responseMeta.SetCell(0, 2, tview.NewTableCell("Time:").SetTextColor(tcell.ColorMediumPurple))

	//view.Layout = tview.NewFlex()
	view.Layout = tview.NewPages()
	mainLayout := tview.NewFlex()

	replayFlexView := tview.NewFlex()
	view.request = tview.NewTextView()
	view.request.SetWrap(false).SetBorder(true).SetTitle("Request")

	// go and cancel buttons
	goButton := tview.NewButton("Go")

	// Host, Port, TLS and Auto-content-length fields
	view.host = tview.NewInputField()
	view.host.SetLabelColor(tcell.ColorMediumPurple)
	view.host.SetLabel("Host ")
	view.host.SetChangedFunc(func(text string) {
		if req, ok := view.entries[id]; ok {
			req.Host = text
		}
	})

	view.port = tview.NewInputField()
	view.port.SetLabel("Port ").SetAcceptanceFunc(tview.InputFieldInteger)
	view.port.SetLabelColor(tcell.ColorMediumPurple)
	view.port.SetChangedFunc(func(text string) {
		if req, ok := view.entries[id]; ok {
			req.Port = text
		}
	})

	view.tls = tview.NewCheckbox()
	view.tls.SetLabelColor(tcell.ColorMediumPurple)
	view.tls.SetLabel("TLS ")
	view.tls.SetChangedFunc(func(checked bool) {
		if req, ok := view.entries[id]; ok {
			req.TLS = checked
		}
	})

	view.updateContentLength = tview.NewCheckbox().SetChecked(true)
	view.updateContentLength.SetLabelColor(tcell.ColorMediumPurple)
	view.updateContentLength.SetLabel("Update Content-Length ")

	view.response = tview.NewTextView().SetWrap(false)
	view.response.SetBorder(true).SetTitle("Response")

	goButton.SetSelectedFunc(func() {
		if req, ok := view.entries[id]; ok {
			view.response.Clear()
			c := false

			go func() {
				if ok, err := req.SendRequest(); ok {
					view.refreshReplay(req)
					responseMeta.SetCell(0, 1, tview.NewTableCell(strconv.Itoa(len(req.RawResponse))))
					responseMeta.SetCell(0, 3, tview.NewTableCell(req.ResponseTime))
				} else {
					if req == view.entries[id] {
						responseMeta.SetCell(0, 1, tview.NewTableCell("ERROR"))
						responseMeta.SetCell(0, 3, tview.NewTableCell("ERROR"))
						view.response.Clear()
						fmt.Fprint(view.response, err)
					}
				}
				app.Draw()
				c = true
			}()

			spindex := 0
			spinner := []string{"|", "/", "-", "\\"}
			go func() {
				for !c {
					goButton.SetLabel("Go " + spinner[spindex])
					app.Draw()
					spindex++
					if spindex == 3 {
						spindex = 0
					}
					time.Sleep(100 * time.Millisecond)
				}
				goButton.SetLabel("Go")
			}()
		}
	})

	view.Table = tview.NewTable()
	view.Table.SetBorders(false).SetSeparator(tview.Borders.Vertical)

	view.Table.SetBorderPadding(0, 0, 0, 0)
	view.Table.SetSelectable(true, false)

	view.Table.SetSelectedFunc(func(row int, column int) {
		id = view.Table.GetCell(row, 0).Text
		if id != "" {
			view.refreshReplay(view.entries[id])
		}
	})

	view.Table.SetSelectionChangedFunc(func(row int, column int) {
		// We need this check to fix an issue with mouse support. If you click somewhere in the
		// proxy view tab it'll fire this function even if there is no cell there yet. EG, user
		// clicks, this method gets fired with something like (-1, -1)
		if row > view.Table.GetRowCount() || row < 0 {
			return
		}

		id = view.Table.GetCell(row, 0).Text
		if id != "" {
			view.refreshReplay(view.entries[id])
		}
	})

	view.request.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlE {
			if req, ok := view.entries[id]; ok {
				app.EnableMouse(false)
				app.Suspend(func() {
					file, err := ioutil.TempFile(os.TempDir(), "glorp")
					if err != nil {
						log.Println(err)
						return
					}
					defer os.Remove(file.Name())

					file.Write(req.RawRequest)
					file.Close()
					cmd := exec.Command("/usr/bin/vi", "-b", file.Name())
					cmd.Stdout = os.Stdout
					cmd.Stdin = os.Stdin
					cmd.Stderr = os.Stderr
					if err := cmd.Run(); err != nil {
						log.Printf("failed to start editor: %v\n", err)
					}

					// load the tmp file back into the request buffer
					dat, err := ioutil.ReadFile(file.Name())
					if err != nil {
						log.Println(err)
						return
					}

					req.RawRequest = dat

					if view.updateContentLength.IsChecked() {
						req.UpdateContentLength()
					}

					view.refreshReplay(req)
				})

				app.EnableMouse(true)
			}
		}
		return event
	})

	// ctrl-e on the respose box should drop into read-only VI displaying data
	view.response.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlE {
			if req, ok := view.entries[id]; ok {
				app.EnableMouse(false)
				app.Suspend(func() {
					file, err := ioutil.TempFile(os.TempDir(), "glorp")
					if err != nil {
						log.Println(err)
						return
					}
					defer os.Remove(file.Name())

					file.Write(req.RawResponse)
					file.Close()
					cmd := exec.Command("/usr/bin/view", "-b", file.Name())
					cmd.Stdout = os.Stdout
					cmd.Stdin = os.Stdin
					cmd.Stderr = os.Stderr
					if err := cmd.Run(); err != nil {
						log.Printf("failed to start editor: %v\n", err)
					}
				})

				app.EnableMouse(true)
			}
		}
		return event
	})

	formTopRow := tview.NewFlex()
	formTopRow.AddItem(view.host, 0, 7, false).AddItem(view.port, 0, 2, false).AddItem(view.tls, 0, 1, false)
	formBottomRow := tview.NewFlex().AddItem(view.updateContentLength, 0, 1, false)
	connectionForm := tview.NewFlex()
	connectionForm.SetDirection(tview.FlexRow)
	connectionForm.AddItem(formTopRow, 0, 1, false).AddItem(formBottomRow, 0, 1, false)

	requestFlexView := tview.NewFlex()
	requestFlexView.SetDirection(tview.FlexRow)
	requestFlexView.AddItem(connectionForm, 2, 1, false)
	requestFlexView.AddItem(view.request, 0, 8, false)
	requestFlexView.AddItem(goButton, 1, 1, false)

	responseFlexView := tview.NewFlex()
	responseFlexView.SetDirection(tview.FlexRow)
	responseFlexView.AddItem(responseMeta, 2, 1, false)
	responseFlexView.AddItem(view.response, 0, 8, false)

	replayFlexView.AddItem(view.Table, 25, 4, true)
	replayFlexView.AddItem(requestFlexView, 0, 4, false)
	replayFlexView.AddItem(responseFlexView, 0, 4, false)

	items := []tview.Primitive{view.Table, view.host, view.port, view.tls, view.updateContentLength, view.request, goButton, view.response}
	mainLayout.AddItem(replayFlexView, 0, 1, true)

	view.Layout.AddPage("mainLayout", mainLayout, true, true)

	mainLayout.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			// find out which item has focus, go to the next one
			for index, primitive := range items {
				if primitive == app.GetFocus() {
					app.SetFocus(items[(index+1)%len(items)])
					return event
				}
			}

			// nothing that we'd want to focus on is focused, yet input is still sinking here...
			// focus on the first item
			app.SetFocus(items[0])

		case tcell.KeyBacktab:
			// find out which item has focus, go to the previous one
			for index, primitive := range items {
				if primitive == app.GetFocus() {
					app.SetFocus(items[(index-1+len(items))%len(items)])
					return event
				}
			}

			app.SetFocus(items[0])

		case tcell.KeyCtrlX:
			if _, ok := view.entries[id]; ok {
				stringModal(app, view.Layout, "Rename Replay", id, func(s string) {
					if r, ok := view.entries[id]; ok {
						view.RenameItem(r, s)
						id = r.ID
					}
				})
			}

		case tcell.KeyCtrlR:
			if req, ok := view.entries[id]; ok {
				view.AddItem(req)
			}

		case tcell.KeyCtrlB:
			replayData := &replay.Request{}
			replayData.ID = "new"
			view.AddItem(replayData)

		}
		return event
	})

}

// refresh the replay view, loading a specific request
func (view *ReplayView) refreshReplay(r *replay.Request) {
	view.request.Clear()
	view.response.Clear()

	view.host.SetText(r.Host)
	view.port.SetText(r.Port)
	view.tls.SetChecked(r.TLS)

	fmt.Fprint(view.request, string(r.RawRequest))
	fmt.Fprint(view.response, string(r.RawResponse))

	// We are appending a UTF8 braille pattern blank (U+2800)
	// to deal with the partial-trailing-utf8-rune logic
	// in tview (textview.go)
	fmt.Fprint(view.request, "\u2800")
	fmt.Fprint(view.response, "\u2800")

	view.response.ScrollToBeginning()
}
