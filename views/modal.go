package views

import (
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
