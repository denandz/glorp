package views

import (
	"container/ring"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/denandz/glorp/replay"

	"github.com/fsnotify/fsnotify"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// ReplayView - struct that holds the main replayview elements
type ReplayView struct {
	Layout        *tview.Pages    // The main replay view, all others should be underneath Layout
	Table         *tview.Table    // the main table that lists all the replay items
	request       *TextPrimitive  // http request box
	response      *TextPrimitive  // http response box
	responseMeta  *tview.Table    // metadata for size recieved and time taken
	goButton      *tview.Button   // send button
	backButton    *tview.Button   // back history button
	forwardButton *tview.Button   // forward history button
	history       *tview.TextView // indicator for which item in the replay history we're lookingat

	host                *tview.InputField // host field input
	port                *tview.InputField // port input
	tls                 *tview.Checkbox   // check box for whether or not to negotiate TLS when rep
	updateContentLength *tview.Checkbox   // check box for whether to attempt a content-length auto update
	externalEditor      *tview.Checkbox   // check box to use an external editor for the request, auto-fire on change
	autoSend            *tview.Checkbox   // check box to deermine if modified requests should be auto-sent

	id string // id of the currently selected replay item

	replays map[string]*ReplayRequests // list of request in the replayer - could probably use the row identifier as the key, support renaming
}

// ReplayRequests - hold an array of requests for a replay item and the currently selected array ID
type ReplayRequests struct {
	ID       string            // The ID as displayed in the table
	elements []*replay.Request // an array of replay requests
	index    int               // the currently selected request
	mu       sync.Mutex
}

func (view *ReplayView) LoadReplays(rr *ReplayRequests) {
	newID := rr.ID

	if _, ok := view.replays[newID]; ok {
		i := 1
		newID = fmt.Sprintf("%s-%d", rr.ID, i)
		for _, ok := view.replays[newID]; ok; _, ok = view.replays[newID] {
			i++
			newID = fmt.Sprintf("%s-%d", rr.ID, i)
		}
	}
	log.Printf("[+] ReplayView - AddItem - Adding replay item with ID: %s\n", newID)
	view.replays[newID] = rr

	rows := view.Table.GetRowCount()
	view.Table.SetCell(rows, 0, tview.NewTableCell(newID))
}

// AddItem method - create a new replay entry
func (view *ReplayView) AddItem(r *replay.Request) {
	newID := r.ID
	if _, ok := view.replays[newID]; ok {
		i := 1
		newID = fmt.Sprintf("%s-%d", r.ID, i)
		for _, ok := view.replays[newID]; ok; _, ok = view.replays[newID] {
			i++
			newID = fmt.Sprintf("%s-%d", r.ID, i)
		}
	}
	log.Printf("[+] ReplayView - AddItem - Adding replay item with ID: %s\n", newID)
	view.replays[newID] = &ReplayRequests{
		ID:       newID,
		elements: make([]*replay.Request, 1),
		index:    0,
	}
	view.replays[newID].elements[0] = r
	r.ID = newID

	rows := view.Table.GetRowCount()
	view.Table.SetCell(rows, 0, tview.NewTableCell(newID))
}

// RenameItem - change the name of an entry
func (view *ReplayView) RenameItem(rr *ReplayRequests, newName string) {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	if _, ok := view.replays[newName]; ok {
		log.Println("[!] Replay entry with ID " + newName + " already exists")
		return
	}
	if _, ok := view.replays[rr.ID]; ok && newName != rr.ID {
		n := view.Table.GetRowCount()
		for i := 0; i < n; i++ {
			if view.Table.GetCell(i, 0).Text == rr.ID {
				originalID := rr.ID
				rr.ID = newName

				// updating table
				view.Table.SetCell(i, 0, tview.NewTableCell(rr.ID))

				// updating map
				view.replays[newName] = rr
				delete(view.replays, originalID)
				break
			}
		}
	}
}

// DeleteItem - delete an entry
func (view *ReplayView) DeleteItem(rr *ReplayRequests) {
	delete(view.replays, rr.ID)
	n := view.Table.GetRowCount()
	for i := 0; i < n; i++ {
		if view.Table.GetCell(i, 0).Text == rr.ID {
			view.Table.RemoveRow(i)
			break
		}
	}
}

// setRawRequest - sets the raw request bytes. If the replay element currently has
// a response, duplicate it into a fresh one so we dont lose data
func updateRawRequest(rr *ReplayRequests, r *replay.Request, data []byte) (req *replay.Request) {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	if len(r.RawResponse) > 0 {
		new_request := r.Copy()
		new_request.RawResponse = nil
		new_request.ResponseTime = ""
		new_request.RawRequest = data

		// Copy() ignores these pointers, manually move them across
		new_request.ExternalFile = r.ExternalFile
		new_request.Watcher = r.Watcher
		r.ExternalFile = nil
		r.Watcher = nil

		rr.elements = append(rr.elements, &new_request)
		rr.index = len(rr.elements) - 1
		req = &new_request
	} else {
		r.RawRequest = data
		req = r
	}

	return
}

// externalEditor - called when the user toggles the external editor checkbox
// If enabled, creates a goroutine to monitor an external file that is used
// to update the request as it's loaded
func (view *ReplayView) useExternalEditor(app *tview.Application, enable bool) {
	if rr, ok := view.replays[view.id]; ok {
		req := rr.elements[rr.index]
		if enable {
			file, err := os.CreateTemp(os.TempDir(), "glorp")
			if err != nil {
				log.Printf("[!] ReplayView - useExternalEditor - tempfile: %s\n", err)
				return
			}
			file.Write(req.RawRequest)
			req.ExternalFile = file

			req.Watcher, err = fsnotify.NewWatcher()
			if err != nil {
				log.Printf("[!] ReplayView - useExternalEditor - watcher: %s\n", err)
			}

			go func() {
				eventTime := time.Now()
				for {
					select {
					case event, ok := <-req.Watcher.Events:
						if !ok {
							return
						}
						if event.Op&fsnotify.Write == fsnotify.Write {
							// get the file size
							len, err := req.ExternalFile.Seek(0, 2)
							if err != nil {
								log.Printf("[!] ReplayView - useExternalEditor - watcher seek: %s\n", err)
							}

							// zero length file, occurs with some editors new-and-move save style. Ignore
							if len == 0 {
								continue
							}

							// Deal with some editors tripping two write events
							if time.Now().After(eventTime.Add(500 * time.Millisecond)) {
								eventTime = time.Now()

								_, err = req.ExternalFile.Seek(0, 0)
								if err != nil {
									log.Printf("[!] ReplayView - useExternalEditor - watcher seek: %s\n", err)
								}

								dat := make([]byte, len)
								_, err = req.ExternalFile.Read(dat)
								if err != nil {
									log.Printf("[!] ReplayView - useExternalEditor - watcher read: %s\n", err)
									return
								}

								req = updateRawRequest(rr, req, dat)
								// if we are focused, redraw and fire
								if view.id == req.ID {
									view.refreshReplay(rr)
									if view.autoSend.IsChecked() {
										view.sendRequest(app, req.ID)
									} else {
										app.Draw()
									}
								}
							}
						}
					case err, ok := <-req.Watcher.Errors:
						if !ok {
							return
						}
						log.Println("[!] watcher gofunc error:", err)
					}
				}

			}()

			err = req.Watcher.Add(req.ExternalFile.Name())
			if err != nil {
				log.Printf("[!] ReplayView - useExternalEditor - watcherAdd: %s\n", err)
			}

		} else {
			err := req.ExternalFile.Close()
			if err != nil {
				log.Printf("[!] ReplayView - useExternalEditor - ExternalFile.Close: %s\n", err)
			}

			err = req.Watcher.Close()
			if err != nil {
				log.Printf("[!] ReplayView - useExternalEditor - Watcher.Close: %s\n", err)
			}

			err = os.Remove(req.ExternalFile.Name())
			if err != nil {
				log.Printf("[!] ReplayView - useExternalEditor - Remove: %s\n", err)
			}

			req.ExternalFile = nil
		}

		view.refreshReplay(rr)
	}
}

// GetView - should return a title and the top-level primitive
func (view *ReplayView) GetView() (title string, content tview.Primitive) {
	return "Replay", view.Layout
}

// Init - Initialization method for the replayer view
func (view *ReplayView) Init(app *tview.Application) {
	view.replays = make(map[string]*ReplayRequests)
	view.responseMeta = tview.NewTable()
	view.responseMeta.SetCell(0, 0, tview.NewTableCell("Size:").SetTextColor(tcell.ColorMediumPurple))
	view.responseMeta.SetCell(0, 2, tview.NewTableCell("Time:").SetTextColor(tcell.ColorMediumPurple))

	//view.Layout = tview.NewFlex()
	view.Layout = tview.NewPages()
	mainLayout := tview.NewFlex()

	replayFlexView := tview.NewFlex()
	view.request = NewTextPrimitive()
	view.request.SetBorder(true).SetTitle("Request")

	// go and history buttons
	view.backButton = tview.NewButton("<")
	view.backButton.SetSelectedFunc(func() {
		if view.id != "" {
			view.replays[view.id].mu.Lock()
			defer view.replays[view.id].mu.Unlock()
			view.replays[view.id].index--
			if view.replays[view.id].index < 0 {
				view.replays[view.id].index = len(view.replays[view.id].elements) - 1
			}
			view.refreshReplay(view.replays[view.id])
		}
	})

	view.forwardButton = tview.NewButton(">")
	view.forwardButton.SetSelectedFunc(func() {
		if view.id != "" {
			view.replays[view.id].mu.Lock()
			defer view.replays[view.id].mu.Unlock()
			view.replays[view.id].index = (view.replays[view.id].index + 1) % len(view.replays[view.id].elements)
			view.refreshReplay(view.replays[view.id])
		}
	})

	view.goButton = tview.NewButton("Go")
	view.goButton.SetSelectedFunc(func() {
		view.sendRequest(app, view.id)
	})

	view.history = tview.NewTextView()
	view.history.SetTextAlign(tview.AlignCenter)

	// Host, Port, TLS and Auto-content-length fields
	view.host = tview.NewInputField()
	view.host.SetLabelColor(tcell.ColorMediumPurple)
	view.host.SetLabel("Host ")
	view.host.SetChangedFunc(func(text string) {
		if rr, ok := view.replays[view.id]; ok {
			rr.elements[rr.index].Host = text
		}
	})

	view.port = tview.NewInputField()
	view.port.SetLabel("Port ").SetAcceptanceFunc(tview.InputFieldInteger)
	view.port.SetLabelColor(tcell.ColorMediumPurple)
	view.port.SetChangedFunc(func(text string) {
		if rr, ok := view.replays[view.id]; ok {
			rr.elements[rr.index].Port = text
		}
	})

	view.tls = tview.NewCheckbox()
	view.tls.SetLabelColor(tcell.ColorMediumPurple)
	view.tls.SetLabel("TLS ")
	view.tls.SetChangedFunc(func(checked bool) {
		if rr, ok := view.replays[view.id]; ok {
			rr.elements[rr.index].TLS = checked
		}
	})

	view.updateContentLength = tview.NewCheckbox().SetChecked(true)
	view.updateContentLength.SetLabelColor(tcell.ColorMediumPurple)
	view.updateContentLength.SetLabel("Update CL")

	view.externalEditor = tview.NewCheckbox().SetChecked(false)
	view.externalEditor.SetLabelColor(tcell.ColorMediumPurple)
	view.externalEditor.SetLabel("Ext. Editor")
	view.externalEditor.SetChangedFunc(func(c bool) {
		view.useExternalEditor(app, c)
	})

	view.autoSend = tview.NewCheckbox().SetChecked(false)
	view.autoSend.SetLabelColor(tcell.ColorMediumPurple)
	view.autoSend.SetLabel("AutoSend")

	view.response = NewTextPrimitive()
	view.response.SetBorder(true).SetTitle("Response")

	view.Table = tview.NewTable()
	view.Table.SetBorders(false).SetSeparator(tview.Borders.Vertical)

	view.Table.SetBorderPadding(0, 0, 0, 0)
	view.Table.SetSelectable(true, false)

	view.Table.SetSelectedFunc(func(row int, column int) {
		view.id = view.Table.GetCell(row, 0).Text
		if view.id != "" {
			view.refreshReplay(view.replays[view.id])
		}
	})

	view.Table.SetSelectionChangedFunc(func(row int, column int) {
		// We need this check to fix an issue with mouse support. If you click somewhere in the
		// proxy view tab it'll fire this function even if there is no cell there yet. EG, user
		// clicks, this method gets fired with something like (-1, -1)
		if row > view.Table.GetRowCount() || row < 0 {
			return
		}

		view.id = view.Table.GetCell(row, 0).Text
		if view.id != "" {
			view.refreshReplay(view.replays[view.id])
		}
	})

	view.request.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyCtrlE:
			if rr, ok := view.replays[view.id]; ok && !view.externalEditor.IsChecked() {
				if runtime.GOOS == "windows" {
					log.Println("[!] Built-in editors are not supported under windows yet")
					return event
				}

				app.EnableMouse(false)
				app.Suspend(func() {
					req := rr.elements[rr.index]
					file, err := os.CreateTemp(os.TempDir(), "glorp")
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
					dat, err := os.ReadFile(file.Name())
					if err != nil {
						log.Println(err)
						return
					}

					//req.entries[req.index].RawRequest = dat
					updateRawRequest(rr, req, dat)
					view.refreshReplay(rr)
					if view.autoSend.IsChecked() {
						view.sendRequest(app, rr.ID)
					}
				})

				app.EnableMouse(true)
			}

		case tcell.KeyCtrlS:
			if req, ok := view.replays[view.id]; ok {
				saveModal(app, view.Layout, req.elements[req.index].RawRequest)
			}

		case tcell.KeyLeft:
			view.backButton.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 'q', 0), func(p tview.Primitive) {})

		case tcell.KeyRight:
			view.forwardButton.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 'q', 0), func(p tview.Primitive) {})

		}
		return event
	})

	// ctrl-e on the respose box should drop into read-only VI displaying data
	view.response.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlE {
			if req, ok := view.replays[view.id]; ok {
				if runtime.GOOS == "windows" {
					log.Println("[!] Built-in editors are not supported under windows yet")
					return event
				}

				app.EnableMouse(false)
				app.Suspend(func() {
					file, err := os.CreateTemp(os.TempDir(), "glorp")
					if err != nil {
						log.Println(err)
						return
					}
					defer os.Remove(file.Name())

					file.Write(req.elements[req.index].RawResponse)
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
		} else if event.Key() == tcell.KeyCtrlS {
			if req, ok := view.replays[view.id]; ok {
				saveModal(app, view.Layout, req.elements[req.index].RawResponse)
			}
		}

		return event
	})

	formTopRow := tview.NewFlex()
	formTopRow.AddItem(view.host, 0, 7, false).AddItem(view.port, 0, 2, false).AddItem(view.tls, 0, 1, false)
	formBottomRow := tview.NewFlex().AddItem(view.updateContentLength, 0, 1, false)
	formBottomRow.AddItem(view.externalEditor, 0, 1, false)
	formBottomRow.AddItem(view.autoSend, 0, 1, false)
	connectionForm := tview.NewFlex()
	connectionForm.SetDirection(tview.FlexRow)
	connectionForm.AddItem(formTopRow, 0, 1, false).AddItem(formBottomRow, 0, 1, false)

	requestFlexView := tview.NewFlex()
	requestFlexView.SetDirection(tview.FlexRow)
	requestFlexView.AddItem(connectionForm, 2, 1, false)
	requestFlexView.AddItem(view.request, 0, 8, false)

	bottomRow := tview.NewFlex()
	bottomRow.AddItem(view.goButton, 0, 7, false).AddItem(view.backButton, 0, 1, false).AddItem(view.history, 0, 1, false).AddItem(view.forwardButton, 0, 1, false)
	requestFlexView.AddItem(bottomRow, 1, 1, false)

	responseFlexView := tview.NewFlex()
	responseFlexView.SetDirection(tview.FlexRow)
	responseFlexView.AddItem(view.responseMeta, 2, 1, false)
	responseFlexView.AddItem(view.response, 0, 8, false)

	replayFlexView.AddItem(view.Table, 18, 4, true)
	replayFlexView.AddItem(requestFlexView, 0, 4, false)
	replayFlexView.AddItem(responseFlexView, 0, 4, false)

	items := []tview.Primitive{
		view.Table,
		view.host,
		view.port,
		view.tls,
		view.updateContentLength,
		view.externalEditor,
		view.autoSend,
		view.request,
		view.goButton,
		view.backButton,
		view.forwardButton,
		view.response,
	}

	focusRing := ring.New(len(items))
	for i := 0; i < len(items); i++ {
		focusRing.Value = items[i]
		focusRing = focusRing.Next()
	}

	mainLayout.AddItem(replayFlexView, 0, 1, true)

	view.Layout.AddPage("mainLayout", mainLayout, true, true)

	mainLayout.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			focusRing = focusRing.Next()
			app.SetFocus(focusRing.Value.(tview.Primitive))

		case tcell.KeyBacktab:
			focusRing = focusRing.Prev()
			app.SetFocus(focusRing.Value.(tview.Primitive))

		case tcell.KeyCtrlX:
			if _, ok := view.replays[view.id]; ok {
				stringModal(app, view.Layout, "Rename Replay", view.id, func(s string) {
					if r, ok := view.replays[view.id]; ok {
						view.RenameItem(r, s)
						view.id = r.ID
					}
				})
			}

		case tcell.KeyCtrlD:
			if entry, ok := view.replays[view.id]; ok {
				boolModal(app, view.Layout, "Delete "+entry.ID+"?", func(b bool) {
					if b {
						view.DeleteItem(entry)
					}
				})
			}

		case tcell.KeyCtrlR:
			if rr, ok := view.replays[view.id]; ok {
				replayEntry := rr.elements[rr.index].Copy()
				replayEntry.RawResponse = nil
				replayEntry.ResponseTime = ""
				view.AddItem(&replayEntry)
			}

		case tcell.KeyCtrlB:
			replayData := &replay.Request{}
			replayData.ID = "new"
			view.AddItem(replayData)

		case tcell.KeyCtrlG:
			view.sendRequest(app, view.id)
		}
		return event
	})

}

// refresh the replay view, loading a specific request
func (view *ReplayView) refreshReplay(rr *ReplayRequests) {
	view.request.Clear()
	view.response.Clear()

	view.host.SetText(rr.elements[rr.index].Host)
	view.port.SetText(rr.elements[rr.index].Port)
	view.tls.SetChecked(rr.elements[rr.index].TLS)
	view.history.SetText(strconv.Itoa(rr.index + 1))

	fmt.Fprint(view.request, string(rr.elements[rr.index].RawRequest))
	fmt.Fprint(view.response, string(rr.elements[rr.index].RawResponse))

	// if an external file exists, show it in the request title
	if rr.elements[rr.index].ExternalFile != nil {
		view.request.SetTitle("Request - File " + rr.elements[rr.index].ExternalFile.Name())
		view.request.SetBorderColor(tcell.ColorDarkSeaGreen)
		view.externalEditor.SetChecked(true)
	} else {
		view.request.SetTitle("Request")
		view.request.SetBorderColor(tcell.ColorDefault)
		view.externalEditor.SetChecked(false)
	}

	// We are appending a UTF8 braille pattern blank (U+2800)
	// to deal with the partial-trailing-utf8-rune logic
	// in tview (textview.go)
	fmt.Fprint(view.request, "\u2800")
	fmt.Fprint(view.response, "\u2800")

	view.response.ScrollToBeginning()

	view.responseMeta.SetCell(0, 1, tview.NewTableCell(strconv.Itoa(len(rr.elements[rr.index].RawResponse))))
	view.responseMeta.SetCell(0, 3, tview.NewTableCell(rr.elements[rr.index].ResponseTime))
}

// Send the request - save the response as a new entry
func (view *ReplayView) sendRequest(app *tview.Application, id string) {
	if rr, ok := view.replays[id]; ok {
		rr.mu.Lock()
		defer rr.mu.Unlock()

		if len(rr.elements[rr.index].RawResponse) > 0 {
			// A response already exists, create a new item to track history
			r := rr.elements[rr.index].Copy()
			rr.elements = append(rr.elements, &r)
			rr.index = len(rr.elements) - 1 // set the selected item to the last entry
		}

		req := rr.elements[rr.index]

		view.response.Clear()

		if view.updateContentLength.IsChecked() {
			req.UpdateContentLength()
		}

		c := false

		go func() {
			size, err := req.SendRequest()
			if req == view.replays[view.id].elements[view.replays[view.id].index] {
				if size > 0 {
					view.refreshReplay(rr)
				} else {
					view.responseMeta.SetCell(0, 1, tview.NewTableCell("ERROR"))
					view.responseMeta.SetCell(0, 3, tview.NewTableCell("ERROR"))
					view.response.Clear()
					fmt.Fprint(view.response, err)
				}
				app.Draw()
			}
			c = true
		}()

		spindex := 0
		spinner := []string{"|", "/", "-", "\\"}
		go func() {
			for !c {
				view.goButton.SetLabel("Go " + spinner[spindex])
				app.Draw()
				spindex++
				if spindex == 3 {
					spindex = 0
				}
				time.Sleep(100 * time.Millisecond)
			}
			view.goButton.SetLabel("Go")
		}()
	}
}
