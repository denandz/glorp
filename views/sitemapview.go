package views

import (
	"log"
	"net/url"
	"sort"
	"strings"
	"unicode"

	"github.com/denandz/glorp/modifier"
	"github.com/rivo/tview"
)

// SiteMapView - the main struct for the view
type SiteMapView struct {
	Layout   *tview.Pages
	treeView *tview.TreeView  // the sitemap tree
	treeRoot *tview.TreeNode  // root of the sitemap tree
	Logger   *modifier.Logger // the Martian logger
}

// GetView - should return a title and the top-level primitive
func (view *SiteMapView) GetView() (title string, content tview.Primitive) {
	return "Sitemap", view.Layout
}

// Init - Initialize the save view
func (view *SiteMapView) Init(app *tview.Application, logger *modifier.Logger, channel chan modifier.Notification) {
	view.Logger = logger
	view.Layout = tview.NewPages()
	mainLayout := tview.NewFlex()

	view.treeRoot = tview.NewTreeNode("")
	view.treeView = tview.NewTreeView()
	view.treeView.SetRoot(view.treeRoot).
		SetCurrentNode(view.treeRoot)
	view.proxyReceiver(app, channel)

	view.treeView.SetSelectedFunc(func(node *tview.TreeNode) {
		reference := node.GetReference()
		if reference == nil {
			return // Selecting the root node does nothing.
		}

		// Collapse if visible, expand if collapsed.
		node.SetExpanded(!node.IsExpanded())
	})

	mainLayout.AddItem(view.treeView, 0, 2, true)
	view.Layout.AddPage("tree", mainLayout, true, true)
}

// add a new top-level host to the tree, return a reference
// to the newly created node
func (view *SiteMapView) addHost(host string) *tview.TreeNode {
	node := tview.NewTreeNode(host).
		SetReference(host).
		SetSelectable(true).
		SetExpanded(false)
	view.treeRoot.AddChild(node)

	// sort it...
	chids := view.treeRoot.GetChildren()
	sort.Slice(chids, func(i, j int) bool {
		t := strings.Map(unicode.ToUpper, chids[i].GetText())
		u := strings.Map(unicode.ToUpper, chids[j].GetText())
		return t < u
		//	return planets[i].Axis < planets[j].Axis
	})

	return node
}

// add a child if it does not already exist
func addChild(parentNode *tview.TreeNode, chunk string) *tview.TreeNode {
	if parentNode == nil {
		return nil
	}

	for _, k := range parentNode.GetChildren() {
		if k.GetText() == chunk {
			return k
		}
	}

	node := tview.NewTreeNode(chunk).
		SetReference(chunk).
		SetSelectable(true).
		SetExpanded(false)
	parentNode.AddChild(node)

	return node
}

func addPath(hostNode *tview.TreeNode, url *url.URL) {
	if hostNode == nil {
		return
	}

	urlPart := strings.Split(url.EscapedPath(), "/")

	parent := hostNode
	for _, k := range urlPart {
		if k != "" && parent != nil {
			parent = addChild(parent, "/"+k)
		}
	}
}

func getSchemeHost(url *url.URL) string {
	return url.Scheme + "://" + url.Host
}

func (view *SiteMapView) addUrl(u string) {
	url, err := url.Parse(u)
	if err != nil {
		log.Printf("[!] Error SiteMapView - addUrl - url parse %s\n", err)
		return
	}

	exists := false
	var hostNode *tview.TreeNode
	for _, k := range view.treeRoot.GetChildren() {
		if k.GetText() == getSchemeHost(url) {
			exists = true
			hostNode = k
			break
		}
	}

	if !exists {
		hostNode = view.addHost(getSchemeHost(url))
	}

	addPath(hostNode, url)

}

func (view *SiteMapView) proxyReceiver(app *tview.Application, channel chan modifier.Notification) {
	// loop the proxy channel and add items to the main table as they arrive
	go func() {
		for elem := range channel {
			if view.treeView != nil && app != nil && elem.NotifType == 0 {
				entry := view.Logger.GetEntry(elem.ID)

				if entry != nil {
					view.addUrl(entry.Request.URL)
				}
				focus := app.GetFocus()
				if focus == view.treeView {
					app.Draw()
				}
			}
		}
	}()
}

// clear all data and reload from the logger entries
func (view *SiteMapView) reload() {
	view.treeRoot.ClearChildren()

	entries := view.Logger.GetEntries()

	for _, v := range entries {
		view.addUrl(v.Request.URL)
	}
}
