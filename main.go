// GLORP - Delete this code

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/denandz/glorp/modifier"
	"github.com/denandz/glorp/proxy"
	"github.com/denandz/glorp/views"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Window handles windows
type Window func() (title string, content tview.Primitive)

func main() {
	// process command line flags
	addr := flag.String("addr", "", "The bind address, default 0.0.0.0")
	cert := flag.String("cert", "", "Path to a CA Certificate")
	key := flag.String("key", "", "Path to the CA cert's private key")
	port := flag.Uint("port", 0, "Listen port for the proxy, default 8080")
	help := flag.Bool("help", false, "Show help")
	flag.Parse()

	if *help ||
		(*cert == "" && *key != "") ||
		(*key == "" && *cert != "") {
		flag.Usage()
		os.Exit(1)
	}

	app := tview.NewApplication()

	// create the replayview stuff
	replayview := new(views.ReplayView)
	replayview.Init(app)

	// start the Martian proxy
	config := &proxy.Config{
		Addr: *addr,
		Cert: *cert,
		Key:  *key,
		Port: *port,
	}

	proxychan := make(chan modifier.Notification, 1024)
	sitemapchan := make(chan modifier.Notification, 1024)
	logger := modifier.NewLogger(app, proxychan, sitemapchan)
	proxy.StartProxy(logger, config)

	// create the main proxy window
	proxyview := new(views.ProxyView)
	proxyview.Init(app, replayview, logger, proxychan)

	// View for the logs, create this now so we dont miss logs
	logText := tview.NewTextView()
	log.SetOutput(logText)
	log.Println("Logger started")

	Log := func() (title string, content tview.Primitive) { return "Log", logText }

	// sitemap view
	sitemapview := new(views.SiteMapView)
	sitemapview.Init(app, proxyview.Logger, sitemapchan)

	// Save/load view
	saveview := new(views.SaveRestoreView)
	saveview.Init(app, replayview, proxyview, sitemapview)

	// Pages
	pages := []Window{
		proxyview.GetView,
		sitemapview.GetView,
		replayview.GetView,
		Log,
		saveview.GetView,
	}

	// Main layout
	mainWindow := tview.NewPages()
	footer := tview.NewTextView().SetDynamicColors(true).SetRegions(true).SetWrap(false)
	footer.SetHighlightedFunc(func(added, removed, remaining []string) {
		mainWindow.SwitchToPage(added[0]) // switching to page does not SetFocus on the page, go figure...

		_, p := mainWindow.GetFrontPage()
		app.SetFocus(p)
	})

	// Create the pages for all slides.
	prevPage := func() {
		slide, _ := strconv.Atoi(footer.GetHighlights()[0])
		slide = (slide - 1 + len(pages)) % len(pages)
		footer.Highlight(strconv.Itoa(slide)).
			ScrollToHighlight()
	}
	nextPage := func() {
		slide, _ := strconv.Atoi(footer.GetHighlights()[0])
		slide = (slide + 1) % len(pages)
		footer.Highlight(strconv.Itoa(slide)).
			ScrollToHighlight()
	}
	for index, slide := range pages {
		title, primitive := slide()
		mainWindow.AddPage(strconv.Itoa(index), primitive, true, index == 0)
		fmt.Fprintf(footer, `%d ["%d"][mediumpurple]%s[white][""]  `, index+1, index, title)
	}
	footer.Highlight("0")

	// create the main layout
	layout := tview.NewFlex().SetDirection(tview.FlexRow)
	layout.AddItem(mainWindow, 0, 1, false)
	layout.AddItem(footer, 1, 1, false)

	// add a quit modal
	quitModal := tview.NewModal().
		SetText("Quit Glorp?").
		AddButtons([]string{"Quit", "Cancel"}).SetDoneFunc(func(buttonIndex int, buttonLabel string) {
		if buttonLabel == "Quit" {
			app.Stop()
		} else {
			mainWindow.HidePage("quitModal")
		}

	})

	mainWindow.AddPage("quitModal", quitModal, false, false)

	// Shortcuts to navigate the slides.
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyCtrlC:
			mainWindow.ShowPage("quitModal")
			app.SetFocus(quitModal)
			return nil
		case tcell.KeyCtrlN:
			nextPage()
		case tcell.KeyCtrlP:
			prevPage()
		}

		return event
	})

	// Start the application.
	if err := app.SetRoot(layout, true).EnableMouse(true).SetFocus(proxyview.Table).Run(); err != nil {
		panic(err)
	}
}
