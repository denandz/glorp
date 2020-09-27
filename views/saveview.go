package views

import (
	"encoding/json"
	"glorp/modifier"
	"glorp/replay"
	"io/ioutil"
	"log"
	"os"

	"github.com/gdamore/tcell"
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
func (view *SaveRestoreView) Init(replays *ReplayView, proxy *ProxyView) {
	view.Layout = tview.NewPages()

	modal := tview.NewModal().
		SetText("Done!").
		AddButtons([]string{"Ok"}).SetDoneFunc(func(buttonIndex int, buttonLabel string) {
		view.Layout.HidePage("modal")
	})

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
				modal.SetText("Saved")
			} else {
				modal.SetText("Save Failed")
			}

		} else {
			modal.SetText("File exists")
		}

		view.Layout.ShowPage("modal")

	}).AddButton("Load", func() {
		if Load(filename.GetText(), replays, proxy) {
			modal.SetText("Loaded")
		} else {
			modal.SetText("Load failed")
		}
		view.Layout.ShowPage("modal")
	})

	view.Layout.AddPage("form", form, true, true).
		AddPage("modal", modal, false, false)
}

// Save - spool the replay and proxy state off to a file
func Save(filename string, replays *ReplayView, proxy *ProxyView) bool {
	if filename == "" {
		return false
	}

	n := replays.Table.GetRowCount()
	var replayentries []replay.Request

	for i := 0; i < n; i++ {
		id := replays.Table.GetCell(i, 0).Text
		if req, ok := replays.entries[id]; ok {
			replayentries = append(replayentries, *req)
		}
	}

	n = proxy.Table.GetRowCount()
	var proxyentries []modifier.Entry
	for i := 1; i < n; i++ {
		id := proxy.Table.GetCell(i, 1).Text
		if req := proxy.Logger.GetEntry(id); req != nil {
			proxyentries = append(proxyentries, *req)
		}
	}

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
