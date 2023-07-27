package views

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"os"
	"sort"

	"github.com/denandz/glorp/modifier"
	"github.com/denandz/glorp/replay"

	"github.com/rivo/tview"
)

// SaveRestoreView - the main struct for the view
type SaveRestoreView struct {
	Layout *tview.Pages
}

type savefile struct {
	Replays      []ReplaySaves
	Proxyentries []modifier.Entry
}

// Like ReplayRequests, but we want to store each replay directly in here
type ReplaySaves struct {
	ID       string           // The ID as displayed in the table
	Entries  []replay.Request // an array of replay requests
	Selected int              // the currently selected request
}

// GetView - should return a title and the top-level primitive
func (view *SaveRestoreView) GetView() (title string, content tview.Primitive) {
	return "Save/Load", view.Layout
}

// Init - Initialize the save view
func (view *SaveRestoreView) Init(app *tview.Application, replays *ReplayView, proxy *ProxyView, sitemap *SiteMapView) {
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
				if b {
					if !Save(filename.GetText(), replays, proxy) {
						log.Println("[!] Error: Save failed")
					}
				}
			})
		}
	})
	form.AddButton("Load", func() {
		if Load(filename.GetText(), replays, proxy, sitemap) {
			msg = "Loaded"
		} else {
			msg = "Load failed"
		}
		notifModal(app, view.Layout, msg)
	})

	view.Layout.AddPage("form", form, true, true)
}

// Save - spool the replay and proxy state off to a file
func Save(filename string, replayview *ReplayView, proxy *ProxyView) bool {
	if filename == "" {
		return false
	}

	//var replayentries []replay.Request
	var replays []ReplaySaves

	for _, v := range replayview.replays {
		rs := ReplaySaves{
			ID:       v.ID,
			Selected: v.index,
			Entries:  make([]replay.Request, len(v.elements)),
		}

		for i, v := range v.elements {
			rs.Entries[i] = v.Copy()
		}

		replays = append(replays, rs)
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
		Replays:      replays,
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

// Load - needs to read a json file, clear out the proxy and replay tables and repopulate them
func Load(filename string, replayview *ReplayView, prox *ProxyView, sitemap *SiteMapView) bool {
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

	replayview.Table.Clear()

	prox.Logger.Reset()
	for _, v := range s.Proxyentries {
		prox.Logger.AddEntry(v)
	}
	prox.reloadtable()
	sitemap.reload()

	for _, v := range s.Replays {
		rr := ReplayRequests{
			ID:       v.ID,
			index:    v.Selected,
			elements: make([]*replay.Request, len(v.Entries)),
		}

		log.Printf("[+] Loaded %d replay entries for id %s", len(v.Entries), v.ID)

		for i, request := range v.Entries {
			r := request.Copy()
			rr.elements[i] = &r
		}

		replayview.LoadReplays(&rr)
	}

	return true
}
