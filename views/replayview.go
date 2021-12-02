package views

import (
	"container/ring"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/denandz/glorp/replay"

	"github.com/fsnotify/fsnotify"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// ReplayView - struct that holds the main replayview elements
type ReplayView struct {
	Layout       *tview.Pages    // The main replay view, all others should be underneath Layout
	Table        *tview.Table    // the main table that lists all the replay items
	request      *tview.TextView // http request box
	response     *tview.TextView // http response box
	responseMeta *tview.Table    // metadata for size recieved and time taken
	goButton     *tview.Button   // send button

	host                *tview.InputField // host field input
	port                *tview.InputField // port input
	tls                 *tview.Checkbox   // check box for whether or not to negotiate TLS when rep
	updateContentLength *tview.Checkbox   // check box for whether to attempt a content-length auto update
	externalEditor      *tview.Checkbox   // check box to use an external editor for the request, auto-fire on change
	autoSend            *tview.Checkbox   // check box to deermine if modified requests should be auto-sent

	id string // id of the currently selected replay item

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
	r.ID = newID

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

// DeleteItem - delete an entry
func (view *ReplayView) DeleteItem(r *replay.Request) {
	delete(view.entries, r.ID)
	n := view.Table.GetRowCount()
	for i := 0; i < n; i++ {
		if view.Table.GetCell(i, 0).Text == r.ID {
			view.Table.RemoveRow(i)
			break
		}
	}
}

// externalEditor - called when the user toggles the external editor checkbox
// If enabled, creates a goroutine to monitor an external file that is used
// to update the request as it's loaded
func (view *ReplayView) useExternalEditor(app *tview.Application, enable bool) {
	if req, ok := view.entries[view.id]; ok {
		if enable {
			file, err := ioutil.TempFile(os.TempDir(), "glorp")
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

								req.RawRequest = dat
								// if we are focused, redraw and fire
								if view.id == req.ID {
									view.refreshReplay(req)
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

		view.refreshReplay(req)
	}
}

// GetView - should return a title and the top-level primitive
func (view *ReplayView) GetView() (title string, content tview.Primitive) {
	return "Replay", view.Layout
}

// Init - Initialization method for the replayer view
func (view *ReplayView) Init(app *tview.Application) {
	view.entries = make(map[string]*replay.Request)
	view.responseMeta = tview.NewTable()
	view.responseMeta.SetCell(0, 0, tview.NewTableCell("Size:").SetTextColor(tcell.ColorMediumPurple))
	view.responseMeta.SetCell(0, 2, tview.NewTableCell("Time:").SetTextColor(tcell.ColorMediumPurple))

	//view.Layout = tview.NewFlex()
	view.Layout = tview.NewPages()
	mainLayout := tview.NewFlex()

	replayFlexView := tview.NewFlex()
	view.request = tview.NewTextView()
	view.request.SetWrap(false).SetBorder(true).SetTitle("Request")

	// go and cancel buttons
	view.goButton = tview.NewButton("Go")

	// Host, Port, TLS and Auto-content-length fields
	view.host = tview.NewInputField()
	view.host.SetLabelColor(tcell.ColorMediumPurple)
	view.host.SetLabel("Host ")
	view.host.SetChangedFunc(func(text string) {
		if req, ok := view.entries[view.id]; ok {
			req.Host = text
		}
	})

	view.port = tview.NewInputField()
	view.port.SetLabel("Port ").SetAcceptanceFunc(tview.InputFieldInteger)
	view.port.SetLabelColor(tcell.ColorMediumPurple)
	view.port.SetChangedFunc(func(text string) {
		if req, ok := view.entries[view.id]; ok {
			req.Port = text
		}
	})

	view.tls = tview.NewCheckbox()
	view.tls.SetLabelColor(tcell.ColorMediumPurple)
	view.tls.SetLabel("TLS ")
	view.tls.SetChangedFunc(func(checked bool) {
		if req, ok := view.entries[view.id]; ok {
			req.TLS = checked
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

	view.response = tview.NewTextView().SetWrap(false)
	view.response.SetBorder(true).SetTitle("Response")

	view.goButton.SetSelectedFunc(func() {
		view.sendRequest(app, view.id)
	})

	view.Table = tview.NewTable()
	view.Table.SetBorders(false).SetSeparator(tview.Borders.Vertical)

	view.Table.SetBorderPadding(0, 0, 0, 0)
	view.Table.SetSelectable(true, false)

	view.Table.SetSelectedFunc(func(row int, column int) {
		view.id = view.Table.GetCell(row, 0).Text
		if view.id != "" {
			view.refreshReplay(view.entries[view.id])
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
			view.refreshReplay(view.entries[view.id])
		}
	})

	view.request.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlE {
			if req, ok := view.entries[view.id]; ok && !view.externalEditor.IsChecked() {
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

					view.refreshReplay(req)
					if view.autoSend.IsChecked() {
						view.sendRequest(app, req.ID)
					}
				})

				app.EnableMouse(true)
			}
		} else if event.Key() == tcell.KeyCtrlS {
			if req, ok := view.entries[view.id]; ok {
				saveModal(app, view.Layout, req.RawRequest)
			}
		}

		return event
	})

	// ctrl-e on the respose box should drop into read-only VI displaying data
	view.response.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlE {
			if req, ok := view.entries[view.id]; ok {
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
		} else if event.Key() == tcell.KeyCtrlS {
			if req, ok := view.entries[view.id]; ok {
				saveModal(app, view.Layout, req.RawResponse)
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
	requestFlexView.AddItem(view.goButton, 1, 1, false)

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
			if _, ok := view.entries[view.id]; ok {
				stringModal(app, view.Layout, "Rename Replay", view.id, func(s string) {
					if r, ok := view.entries[view.id]; ok {
						view.RenameItem(r, s)
						view.id = r.ID
					}
				})
			}

		case tcell.KeyCtrlD:
			if entry, ok := view.entries[view.id]; ok {
				boolModal(app, view.Layout, "Delete "+entry.ID+"?", func(b bool) {
					if b {
						view.DeleteItem(entry)
					}
				})
			}

		case tcell.KeyCtrlR:
			if req, ok := view.entries[view.id]; ok {
				replayEntry := req.Copy()
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
func (view *ReplayView) refreshReplay(r *replay.Request) {
	view.request.Clear()
	view.response.Clear()

	view.host.SetText(r.Host)
	view.port.SetText(r.Port)
	view.tls.SetChecked(r.TLS)

	fmt.Fprint(view.request, string(r.RawRequest))
	fmt.Fprint(view.response, string(r.RawResponse))

	// if an external file exists, show it in the request title
	if r.ExternalFile != nil {
		view.request.SetTitle("Request - File " + r.ExternalFile.Name())
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

	view.responseMeta.SetCell(0, 1, tview.NewTableCell(strconv.Itoa(len(r.RawResponse))))
	view.responseMeta.SetCell(0, 3, tview.NewTableCell(r.ResponseTime))
}

// Send the request
func (view *ReplayView) sendRequest(app *tview.Application, id string) {
	if req, ok := view.entries[id]; ok {
		view.response.Clear()

		if view.updateContentLength.IsChecked() {
			req.UpdateContentLength()
		}

		c := false

		go func() {
			size, err := req.SendRequest()
			if req == view.entries[view.id] {
				if size > 0 {
					view.refreshReplay(req)
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
