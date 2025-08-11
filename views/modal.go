package views

import (
	"log"
	"os"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func newmodal(p tview.Primitive, width, height int) tview.Primitive {
	modal := tview.NewFlex()
	modal.AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(p, height, 1, false).
			AddItem(nil, 0, 1, false), width, 1, false).
		AddItem(nil, 0, 1, false)

	return modal
}

func notifModal(app *tview.Application, page *tview.Pages, message string) {
	modal := tview.NewFlex()
	okButton := tview.NewButton("OK").SetSelectedFunc(func() {
		page.HidePage("notifmodal")
		page.RemovePage("notifmodal")
	})

	modal.SetBorder(true)
	modal.SetDirection(tview.FlexRow)
	modal.SetTitle(message)
	modal.AddItem(okButton, 0, 1, false)

	page.AddPage("notifmodal", newmodal(modal, 40, 5), true, false)
	page.ShowPage("notifmodal")
	app.SetFocus(okButton)
}

func boolModal(app *tview.Application, page *tview.Pages, message string, cb func(bool)) {
	modal := tview.NewFlex()
	okButton := tview.NewButton("OK").SetSelectedFunc(func() {
		page.HidePage("boolmodal")
		cb(true)
		page.RemovePage("boolmodal")
	})
	cancelButton := tview.NewButton("Cancel").SetSelectedFunc(func() {
		page.HidePage("boolmodal")
		cb(false)
		page.RemovePage("boolmodal")
	})

	modal.SetBorder(true)
	modal.SetDirection(tview.FlexRow)
	modal.SetTitle(message)
	modal.AddItem(okButton, 0, 1, false)
	modal.AddItem(cancelButton, 0, 1, false)
	modal.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyTab {
			if okButton == app.GetFocus() {
				app.SetFocus(cancelButton)
			} else {
				app.SetFocus(okButton)
			}
		}
		if event.Key() == tcell.KeyBacktab {
			if cancelButton == app.GetFocus() {
				app.SetFocus(okButton)
			} else {
				app.SetFocus(cancelButton)
			}
		}

		return event
	})

	page.AddPage("boolmodal", newmodal(modal, 40, 5), true, false)
	page.ShowPage("boolmodal")
	app.SetFocus(okButton)
}

func stringModal(app *tview.Application, page *tview.Pages, message string, defaultText string, cb func(string)) {
	textInput := tview.NewInputField()
	textInput.SetText(defaultText)
	modal := tview.NewFlex().AddItem(textInput, 0, 1, true)
	okButton := tview.NewButton("OK").SetSelectedFunc(func() {
		page.HidePage("stringmodal")
		cb(textInput.GetText())
		page.RemovePage("stringmodal")
	})

	modal.SetBorder(true)
	modal.SetDirection(tview.FlexRow)
	modal.SetTitle(message)
	modal.AddItem(okButton, 0, 1, false)
	modal.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyTab {
			if textInput == app.GetFocus() {
				app.SetFocus(okButton)
			} else {
				app.SetFocus(textInput)
			}
		}
		if event.Key() == tcell.KeyBacktab {
			if textInput == app.GetFocus() {
				app.SetFocus(okButton)
			} else {
				app.SetFocus(textInput)
			}
		}

		if event.Key() == tcell.KeyEnter {
			page.HidePage("stringmodal")
			cb(textInput.GetText())
			page.RemovePage("stringmodal")
		}

		return event
	})

	page.AddPage("stringmodal", newmodal(modal, 40, 5), true, false)
	page.ShowPage("stringmodal")
	app.SetFocus(textInput)
}

func saveModal(app *tview.Application, page *tview.Pages, buf []byte) {
	filenameInput := tview.NewInputField()
	modal := tview.NewFlex().AddItem(filenameInput, 0, 1, true)
	saveButton := tview.NewButton("OK").SetSelectedFunc(func() {
		_, err := os.Stat(filenameInput.GetText())
		if os.IsNotExist(err) {
			err = os.WriteFile(filenameInput.GetText(), buf, 0644)
			if err != nil {
				log.Println(err)
			}
			modal.SetTitle("Save")
			page.HidePage("savemodal")
			page.RemovePage("savemodal")
		} else {
			modal.SetTitle("File exists")
		}
	})

	modal.SetBorder(true)
	modal.SetDirection(tview.FlexRow)
	modal.SetTitle("Save")
	modal.AddItem(saveButton, 0, 1, false)
	modal.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyTab || event.Key() == tcell.KeyBacktab {
			if filenameInput == app.GetFocus() {
				app.SetFocus(saveButton)
			} else {
				app.SetFocus(filenameInput)
			}
		} else if event.Key() == tcell.KeyESC {
			page.HidePage("savemodal")
			page.RemovePage("savemodal")
		}

		return event
	})

	page.AddPage("savemodal", newmodal(modal, 40, 5), true, false)
	page.ShowPage("savemodal")
	app.SetFocus(filenameInput)
}
