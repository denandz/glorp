package views

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"os"
	"sort"

	"github.com/denandz/glorp/modifier"
	"github.com/denandz/glorp/replay"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// SaveRestoreView - the main struct for the view
type SaveRestoreView struct {
	Layout *tview.Pages
}

type savefile struct {
	Replays      []replay.Request
	Proxyentries []modifier.Entry
}

// GetView - should return a title and the top-level primitive
func (view *SaveRestoreView) GetView() (title string, content tview.Primitive) {
	return "Save/Load", view.Layout
}

// Init - Initialize the save view
func (view *SaveRestoreView) Init(app *tview.Application, replays *ReplayView, proxy *ProxyView) {
	view.Layout = tview.NewPages()
	var msg string

	form := tview.NewForm()
	form.SetBorder(true).SetTitle("Save/Load").SetTitleAlign(tview.AlignLeft)
	filename := tview.NewInputField()
	filename.SetLabel("Filename")
	filename.SetPlaceholder("./glorp.json")

	form.AddFormItem(filename)
	form.AddButton("Save", func() {
		_, err := os.Stat(filename.GetText())
		if os.IsNotExist(err) { // need to check if dir
			if Save(filename.GetText(), replays, proxy) {
				msg = "Save Complete"
			} else {
				msg = "Save Failed"
			}
			notifModal(app, view.Layout, msg)
		} else {
			boolModal(app, view.Layout, "File exists - overwrite?", func(b bool) {
				if b == true {
					if !Save(filename.GetText(), replays, proxy) {
						log.Println("[!] Error: Save failed")
					}
				}
			})
		}
	})
	form.AddButton("Load", func() {
		if Load(filename.GetText(), replays, proxy) {
			msg = "Loaded"
		} else {
			msg = "Load failed"
		}
		notifModal(app, view.Layout, msg)
	})

	view.Layout.AddPage("form", form, true, true)
}

// Save - spool the replay and proxy state off to a file
func Save(filename string, replays *ReplayView, proxy *ProxyView) bool {
	if filename == "" {
		return false
	}

	var replayentries []replay.Request

	for _, value := range replays.entries {
		replayentries = append(replayentries, *value)
	}

	var proxyentries []modifier.Entry
	for _, value := range proxy.Logger.GetEntries() {
		proxyentries = append(proxyentries, *value)
	}

	// sort proxyentries by date
	// We do this here to make inevitable processing of the save file with JQ, grep, sed, awk,
	// and other such command line voodoo more pallatable
	sort.Slice(proxyentries, func(i, j int) bool {
		return proxyentries[i].StartedDateTime.Before(proxyentries[j].StartedDateTime)
	})

	s := &savefile{
		Replays:      replayentries,
		Proxyentries: proxyentries,
	}

	var jsonData []byte

	jsonData, err := json.Marshal(s)
	if err != nil {
		log.Println(err)
		return false
	}

	err = ioutil.WriteFile(filename, jsonData, 0644)
	if err != nil {
		log.Println(err)
		return false
	}

	log.Println("[+] SaveView - Save - Saved file: " + filename)
	return true
}

// Load - needs to read a json file, clear out the proxy and replay ables and repopulate them
func Load(filename string, replays *ReplayView, prox *ProxyView) bool {
	f, err := os.Open(filename)
	if err != nil {
		log.Println(err)
		return false
	}
	defer f.Close()

	fileBytes, err := ioutil.ReadAll(f)
	if err != nil {
		log.Println(err)
		return false
	}

	s := new(savefile)
	err = json.Unmarshal(fileBytes, &s)
	if err != nil {
		log.Println(err)
		return false
	}

	prox.Table.Clear()
	replays.Table.Clear()

	prox.Table.SetCell(0, 1, tview.NewTableCell("ID").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false).SetAlign(tview.AlignCenter))
	prox.Table.SetCell(0, 2, tview.NewTableCell("URL").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false).SetAlign(tview.AlignCenter))
	prox.Table.SetCell(0, 3, tview.NewTableCell("Status").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))
	prox.Table.SetCell(0, 4, tview.NewTableCell("Size").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))
	prox.Table.SetCell(0, 5, tview.NewTableCell("Time").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))
	prox.Table.SetCell(0, 6, tview.NewTableCell("Date").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))
	prox.Table.SetCell(0, 7, tview.NewTableCell("Method").SetTextColor(tcell.ColorMediumPurple).SetSelectable(false))

	for _, v := range s.Proxyentries {
		prox.Logger.AddEntry(v)
	}

	for i := range s.Replays {
		replays.AddItem(&s.Replays[i])
	}

	return true
}
