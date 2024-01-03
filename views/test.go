package views

import (
	"github.com/denandz/glorp/modifier"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func initializeTestApp() (*tview.Application, *ProxyView, *SiteMapView, *ReplayView, *SaveRestoreView) {
	simScreen := tcell.NewSimulationScreen("UTF-8")
	simScreen.Init()
	simScreen.SetSize(10, 10)

	app := tview.NewApplication()
	app.SetScreen(simScreen)

	// create the replayview stuff
	replayview := new(ReplayView)
	replayview.Init(app)

	proxychan := make(chan modifier.Notification, 1024)
	sitemapchan := make(chan modifier.Notification, 1024)
	logger := modifier.NewLogger(app, proxychan, sitemapchan)
	//proxy.StartProxy(logger, config)

	// create the main proxy window
	proxyview := new(ProxyView)
	proxyview.Init(app, replayview, logger, proxychan)

	// sitemap view
	sitemapview := new(SiteMapView)
	sitemapview.Init(app, proxyview.Logger, sitemapchan)

	// save view
	saveview := new(SaveRestoreView)
	saveview.Init(app, replayview, proxyview, sitemapview)

	return app, proxyview, sitemapview, replayview, saveview
}
