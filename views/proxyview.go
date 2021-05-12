package views

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"sync"

	"github.com/denandz/glorp/modifier"
	"github.com/denandz/glorp/replay"

	"github.com/gdamore/tcell/v2"
	"github.com/google/martian/messageview"
	"github.com/rivo/tview"
)

// ProxyView - struct that holds the main prox elements
type ProxyView struct {
	Layout      *tview.Pages     // The main replay view, all others should be underneath Layout
	Table       *tview.Table     // the proxy history table
	requestBox  *tview.TextView  // request text box
	responseBox *tview.TextView  // response text box
	Logger      *modifier.Logger // the Martian logger
	proxychan   chan modifier.Notification

	filter ViewFilter // filter for the proxy view
}

// ViewFilter - specify a match pattern and whether to include or exclude the pattern
type ViewFilter struct {
	pattern   string // regex pattern string
	condition string // exclude or include
	mutex     sync.Mutex
}

// GetView - should return a title and the top-level primitive
func (view *ProxyView) GetView() (title string, content tview.Primitive) {
	return "Proxy", view.Layout
}

// Init - Main initialization method for the proxy view
func (view *ProxyView) Init(app *tview.Application, replayview *ReplayView) {
	var id string
	var saveBuffer []byte

	view.Layout = tview.NewPages()
	mainLayout := tview.NewFlex()
	mainLayout.SetDirection(tview.FlexRow)

	view.Table = tview.NewTable()
	view.Table.SetFixed(1, 1)
	view.Table.SetBorders(false).SetSeparator(tview.Borders.Vertical)

	view.Table.SetBorderPadding(0, 0, 0, 0)
	view.Table.SetSelectable(true, false)

	// Set up the table headers
	view.Table.SetCell(0, 1, tview.NewTableCell("ID").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false).SetAlign(tview.AlignCenter))
	view.Table.SetCell(0, 2, tview.NewTableCell("URL").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false).SetAlign(tview.AlignCenter))
	view.Table.SetCell(0, 3, tview.NewTableCell("Status").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))
	view.Table.SetCell(0, 4, tview.NewTableCell("Size").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))
	view.Table.SetCell(0, 5, tview.NewTableCell("Time").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))
	view.Table.SetCell(0, 6, tview.NewTableCell("Date").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))
	view.Table.SetCell(0, 7, tview.NewTableCell("Method").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))

	reqRespFlexView := tview.NewFlex()
	view.requestBox = tview.NewTextView().SetWrap(false).SetDynamicColors(true)
	view.requestBox.SetBorder(true)
	view.requestBox.SetTitle("Request")
	view.requestBox.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlS {
			if entry := view.Logger.GetEntry(id); entry != nil {
				saveModal(app, view.Layout, entry.Request.Raw)
			}
		} else if event.Key() == tcell.KeyCtrlE {
			if entry := view.Logger.GetEntry(id); entry != nil {
				app.EnableMouse(false)
				app.Suspend(func() {
					file, err := ioutil.TempFile(os.TempDir(), "glorp")
					if err != nil {
						log.Println(err)
						return
					}
					defer os.Remove(file.Name())

					file.Write(entry.Request.Raw)
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

	view.responseBox = tview.NewTextView().SetWrap(false).SetDynamicColors(true)
	view.responseBox.SetBorder(true)
	view.responseBox.SetTitle("Response")
	view.responseBox.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlS {
			if entry := view.Logger.GetEntry(id); entry != nil {
				saveModal(app, view.Layout, entry.Response.Raw)
			}
		} else if event.Key() == tcell.KeyCtrlE {
			if entry := view.Logger.GetEntry(id); entry != nil {
				app.EnableMouse(false)
				app.Suspend(func() {
					file, err := ioutil.TempFile(os.TempDir(), "glorp")
					if err != nil {
						log.Println(err)
						return
					}
					defer os.Remove(file.Name())

					file.Write(entry.Response.Raw)
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

	reqRespFlexView.AddItem(view.requestBox, 0, 1, false)
	reqRespFlexView.AddItem(view.responseBox, 0, 1, false)

	mainLayout.AddItem(view.Table, 0, 2, true)
	mainLayout.AddItem(reqRespFlexView, 0, 3, false)

	// save modal
	filenameInput := tview.NewInputField()
	saveModal := tview.NewFlex().AddItem(filenameInput, 0, 1, true)
	saveButton := tview.NewButton("OK").SetSelectedFunc(func() {
		_, err := os.Stat(filenameInput.GetText())
		if os.IsNotExist(err) {
			err = ioutil.WriteFile(filenameInput.GetText(), saveBuffer, 0644)
			if err != nil {
				log.Println(err)
			}
			saveModal.SetTitle("Save")
			view.Layout.HidePage("saveModal")
		} else {
			saveModal.SetTitle("File exists")
		}
	})

	saveModal.SetBorder(true)
	saveModal.SetDirection(tview.FlexRow)
	saveModal.SetTitle("Save")
	saveModal.AddItem(saveButton, 0, 1, false)
	saveModal.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyTab {
			if filenameInput == app.GetFocus() {
				app.SetFocus(saveButton)
			} else {
				app.SetFocus(filenameInput)
			}
		}

		if event.Key() == tcell.KeyESC {
			view.Layout.HidePage("saveModal")
		}

		return event
	})

	// hacky way to deal with item focus...
	items := []tview.Primitive{view.Table, view.requestBox, view.responseBox}

	view.Table.SetSelectionChangedFunc(func(row int, column int) {
		// We need this check to fix an issue with mouse support. If you click somewhere in the
		// proxy view tab it'll fire this function even if there is no cell there yet. EG, user
		// clicks, this method gets fired with something like (-1, -1)
		if row > view.Table.GetRowCount() || row < 0 {
			return
		}

		view.requestBox.Clear()
		view.responseBox.Clear()

		// get the ID from the table
		id = view.Table.GetCell(row, 1).Text
		if entry := view.Logger.GetEntry(id); entry != nil {
			if entry.Request != nil {
				// Appending a UTF8 braille pattern blank (U+2800)
				// to deal with the partial-trailing-utf8-rune logic
				// in tview (textview.go)

				// this technique seems to make weird artifecats happen depending on the terminal
				// some sensible mechanism forResponse escaping data would probably be better...
				//fmt.Fprint(view.requestBox, "\u2800")
				view.writeRequest(entry.Request)
				view.requestBox.ScrollToBeginning()
			}
			if entry.Response != nil {
				view.writeResponse(entry)
				view.responseBox.ScrollToBeginning()
			}
		}
	})

	// input captures
	view.Table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyCtrlR:
			if entry := view.Logger.GetEntry(id); entry != nil {
				replayData := &replay.Request{}

				URL, err := url.Parse(entry.Request.URL)
				if err != nil {
					log.Printf("Error: Could not parse URL for request %s: %s\n", id, err)
					return event
				}

				// Parse the raw request and add a Connection: close header.
				// We do this here instead of on request launch so that the user is
				// free to edit the request in the replayer and remove the header
				reader := bytes.NewReader(entry.Request.Raw)
				req, err := http.ReadRequest(bufio.NewReader(reader))

				if err != nil {
					log.Printf("Error: Issue in ReadRequest for request %s: %s\n", id, err)
					replayData.RawRequest = make([]byte, len(entry.Request.Raw))
					copy(replayData.RawRequest, entry.Request.Raw)
				} else {
					req.Header.Set("Connection", "close")
					replayData.RawRequest, err = httputil.DumpRequest(req, true)

					if err != nil {
						// fallback to the original request
						log.Printf("Error: Issue in DumpRequest for request %s: %s\n", id, err)
						replayData.RawRequest = make([]byte, len(entry.Request.Raw))
						copy(replayData.RawRequest, entry.Request.Raw)
					}
				}

				replayData.Host = URL.Hostname()
				replayData.Port = URL.Port()
				if URL.Scheme == "https" {
					replayData.TLS = true

					if replayData.Port == "" {
						replayData.Port = "443"
					}
				} else if replayData.Port == "" {
					replayData.Port = "80"
				}
				replayData.ID = id

				replayview.AddItem(replayData)
			}

		}

		switch event.Rune() {
		case '/':
			stringModal(app, view.Layout, "Set URL Display Filter", view.filter.condition, func(filter string) {
				view.filter.mutex.Lock()
				view.filter.condition = filter
				view.filter.mutex.Unlock()

				view.reloadtable()
			})
		case 'g':
			view.Table.ScrollToBeginning()

		case 'G':
			view.Table.ScrollToEnd()
		}

		return event
	})

	mainLayout.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyTab {
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
		}
		if event.Key() == tcell.KeyBacktab {
			// find out which item has focus, go to the previous one
			for index, primitive := range items {
				if primitive == app.GetFocus() {
					app.SetFocus(items[(index-1+len(items))%len(items)])
					return event
				}
			}

			app.SetFocus(items[0])
		}

		return event
	})

	view.Layout.AddPage("mainlayout", mainLayout, true, true)
	view.Layout.AddPage("saveModal", newmodal(saveModal, 40, 5), true, false)

	view.createProxy(app)

}

func (view *ProxyView) createProxy(app *tview.Application) {
	// Create the martian logger and a channel for new item notification

	view.proxychan = make(chan modifier.Notification, 1024)
	go func() {
		for elem := range view.proxychan {
			if view.Table != nil && app != nil {
				entry := view.Logger.GetEntry(elem.ID)
				if entry != nil {
					n := view.Table.GetRowCount()
					view.AddEntry(entry, elem.NotifType)

					// if the table is focused, and the cursor is on the last entry, then update it to the new entry
					if app.GetFocus() == view.Table && view.proxyfilter(entry.Request.URL) {
						if r, _ := view.Table.GetSelection(); r == n-1 {
							if elem.NotifType == 0 {
								view.Table.Select(n, 0)
							} else {
								view.Table.Select(n-1, 0)
							}
						}
					}

					// redraw when adding, if the proxy view is in focus right now
					focus := app.GetFocus()
					if focus == view.Table || focus == view.requestBox || focus == view.responseBox {
						app.Draw()
					}
				}
			}
		}
	}()

	view.Logger = modifier.NewLogger(app, view.proxychan, view.Table)
}

// AddEntry - add a modifier entry to the proxy table, t indicates request, response or save/load
func (view *ProxyView) AddEntry(e *modifier.Entry, t int) {
	n := view.Table.GetRowCount()
	if e.Request != nil {
		url := e.Request.URL

		if len(url) > 100 {
			url = string([]rune(e.Request.URL)[0:100])
		}

		if view.proxyfilter(e.Request.URL) {

			switch t {
			case 0: // request
				view.Table.SetCell(n, 1, tview.NewTableCell(e.ID))
				view.Table.SetCell(n, 2, tview.NewTableCell(url).SetExpansion(1))
				view.Table.SetCell(n, 6, tview.NewTableCell(""))
				view.Table.SetCell(n, 7, tview.NewTableCell(e.Request.Method))

			case 1: // response
				// find the table row with the corresponding request. I expect responses to arrive relatively soon after the request
				// is sent, so using a reverse-search here
				if e.Response != nil {
					for i := n; i > 0; i-- {
						if i < n && view.Table.GetCell(i, 1).Text == e.ID {
							view.Table.SetCell(i, 3, tview.NewTableCell(strconv.Itoa(e.Response.Status)))
							view.Table.SetCell(i, 4, tview.NewTableCell(strconv.Itoa(len(e.Response.Raw))))
							view.Table.SetCell(i, 5, tview.NewTableCell(strconv.FormatInt(e.Time, 10)))
							view.Table.SetCell(i, 6, tview.NewTableCell(e.StartedDateTime.Format("02-01-2006 15:04:05")).SetAlign(tview.AlignRight))
						}
					}
				}
			case 2: // save/load
				view.Table.SetCell(n, 1, tview.NewTableCell(e.ID))
				view.Table.SetCell(n, 2, tview.NewTableCell(url).SetExpansion(1))
				view.Table.SetCell(n, 6, tview.NewTableCell(""))
				view.Table.SetCell(n, 7, tview.NewTableCell(e.Request.Method))
				if e.Response != nil {
					view.Table.SetCell(n, 3, tview.NewTableCell(strconv.Itoa(e.Response.Status)))
					view.Table.SetCell(n, 4, tview.NewTableCell(strconv.Itoa(len(e.Response.Raw))))
					view.Table.SetCell(n, 5, tview.NewTableCell(strconv.FormatInt(e.Time, 10)))
					view.Table.SetCell(n, 6, tview.NewTableCell(e.StartedDateTime.Format("02-01-2006 15:04:05")).SetAlign(tview.AlignRight))
				}
			}
		}
	}
}

// proxyfilter should take a URL, evaluate the filters and return true if the proxy entry should be displayed
// or false if the response entry should not be displayed
func (view *ProxyView) proxyfilter(url string) bool {
	view.filter.mutex.Lock()
	defer view.filter.mutex.Unlock()

	match, err := regexp.MatchString(view.filter.condition, url)
	if err != nil {
		log.Printf("[!] Error proxyfilter %s\n", err)
		return true // something went wrong, default to display
	}

	return match
}

// reloadtable clears the proxy table an redraws, happens when changing the filter regex
func (view *ProxyView) reloadtable() {
	view.Table.Clear()

	view.Table.SetCell(0, 1, tview.NewTableCell("ID").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false).SetAlign(tview.AlignCenter))
	view.Table.SetCell(0, 2, tview.NewTableCell("URL").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false).SetAlign(tview.AlignCenter))
	view.Table.SetCell(0, 3, tview.NewTableCell("Status").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))
	view.Table.SetCell(0, 4, tview.NewTableCell("Size").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))
	view.Table.SetCell(0, 5, tview.NewTableCell("Time").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))
	view.Table.SetCell(0, 6, tview.NewTableCell("Date").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))
	view.Table.SetCell(0, 7, tview.NewTableCell("Method").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))

	var proxyentries []*modifier.Entry
	for _, value := range view.Logger.GetEntries() {
		proxyentries = append(proxyentries, value)
	}

	// sort proxyentries by date
	sort.Slice(proxyentries, func(i, j int) bool {
		return proxyentries[i].StartedDateTime.Before(proxyentries[j].StartedDateTime)
	})

	for _, v := range proxyentries {
		view.AddEntry(v, 2)
	}
}

func (view *ProxyView) writeRequest(r *modifier.Request) {
	reader := bytes.NewReader(r.Raw)
	req, err := http.ReadRequest(bufio.NewReader(reader))

	if err != nil {
		log.Printf("[!] Error writeRequest %s\n", err)
		return
	}

	mv := messageview.New()
	if err := mv.SnapshotRequest(req); err != nil {
		log.Printf("[!] Error writeRequest %s\n", err)
		return
	}

	br, err := mv.Reader(messageview.Decode())
	if err != nil {
		log.Printf("[!] Error writeRequest %s\n", err)
		return
	}

	body, err := ioutil.ReadAll(br)
	if err != nil {
		log.Printf("[!] Error writeRequest %s\n", err)
		return
	}

	fmt.Fprint(view.requestBox, string(body))
	fmt.Fprint(view.requestBox, "\u2800")
}

func (view *ProxyView) writeResponse(e *modifier.Entry) {
	if e.Request.Method == "HEAD" {
		fmt.Fprint(view.responseBox, string(e.Response.Raw))
		fmt.Fprint(view.responseBox, "\u2800")
		return
	}

	// if the response greater than 5 megabytes, just display the headers
	if len(e.Response.Raw) > 5*1024*1024 {
		fmt.Fprint(view.responseBox, string(e.Response.Raw[0:len(e.Response.Raw)-int(e.Response.BodySize)]))
		fmt.Fprint(view.responseBox, "\r\n\r\nResponse too large to display - Replay or CTRL-S")
		fmt.Fprint(view.responseBox, "\u2800")
		return
	}

	reader := bytes.NewReader(e.Response.Raw)
	resp, err := http.ReadResponse(bufio.NewReader(reader), nil)

	if err != nil {
		log.Printf("[!] Error writeResponse - http.ReadResponse %s\n", err)
		return
	}

	mv := messageview.New()
	if err := mv.SnapshotResponse(resp); err != nil {
		log.Printf("[!] Error writeResponse - mv.SnapshotResponse %s\n", err)
		return
	}

	br, err := mv.Reader(messageview.Decode())
	if err != nil {
		log.Printf("[!] Error writeResponse - mv.Reader %s\n", err)
		return
	}

	body, err := ioutil.ReadAll(br)
	if err != nil {
		log.Printf("[!] Error writeResponse - ioutil.ReadAll %s\n", err)
		return
	}

	fmt.Fprint(view.responseBox, string(body))
	fmt.Fprint(view.responseBox, "\u2800")
}
