package views

import (
	"testing"
)

func TestLoad(t *testing.T) {
	_, proxyview, sitemapview, replayview, _ := initializeTestApp()

	if Load("../tests/savev1.1.json", replayview, proxyview, sitemapview) == false {
		t.Errorf("TestLoad Load: got %v want %v", false, true)
	}

	if l := len(replayview.replays); l != 1 {
		t.Errorf("TestLoad unexpected number of replays: got %v want %v", l, 1)
	}

	if l := len(replayview.replays["1445586a66b80188"].elements); l != 2 {
		t.Errorf("TestLoad unexpected number of replays: got %v want %v", l, 1)
	}

	if l := len(proxyview.Logger.GetEntries()); l != 2 {
		t.Errorf("TestLoad unexpected number of proxy entires: got %v want %v", l, 2)
	}
}

func TestLegacyLoad(t *testing.T) {
	_, proxyview, sitemapview, replayview, _ := initializeTestApp()

	if Load("../tests/oldsave.json", replayview, proxyview, sitemapview) == false {
		t.Errorf("TestLegacyLoad Load: got %v want %v", false, true)
	}

	if l := len(replayview.replays); l != 1 {
		t.Errorf("TestLegacyLoad unexpected number of replays: got %v want %v", l, 1)
	}

	if l := len(proxyview.Logger.GetEntries()); l != 2 {
		t.Errorf("TestLegacyLoad unexpected number of proxy entires: got %v want %v", l, 2)
	}
}
