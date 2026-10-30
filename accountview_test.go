package main

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/cookies"
	"github.com/elianiva/meiro/internal/settings"
	"github.com/elianiva/meiro/youtube"
)

func TestAccountChannelSelection(t *testing.T) {
	tests := []struct {
		name               string
		channels           []youtube.AccountChannel
		hasSelectedChannel bool
		current            string
		want               []bool
	}{
		{
			name: "YouTube marks the selected channel when channel IDs are missing",
			channels: []youtube.AccountChannel{
				{Name: "Personal", Selected: true},
				{Name: "Brand"},
				{Name: "Music"},
			},
			hasSelectedChannel: true,
			want:               []bool{true, false, false},
		},
		{
			name: "unknown empty channel IDs do not select every channel",
			channels: []youtube.AccountChannel{
				{Name: "Personal"},
				{Name: "Brand"},
			},
			want: []bool{false, false},
		},
		{
			name: "a known channel ID is the fallback when YouTube omits its selection",
			channels: []youtube.AccountChannel{
				{Name: "Personal", ChannelID: "UC-personal"},
				{Name: "Brand", ChannelID: "UC-brand"},
			},
			current: "UC-brand",
			want:    []bool{false, true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for i, channel := range test.channels {
				if got := accountChannelSelected(channel, test.hasSelectedChannel, test.current); got != test.want[i] {
					t.Errorf("channel %q selected = %t, want %t", channel.Name, got, test.want[i])
				}
			}
		})
	}
}

func TestChannelMenuSwitchesToBrandChannel(t *testing.T) {
	resetFakes()
	fakePageID.Store("")
	a := signedInApp(t)
	list, err := a.account.session.client.GetAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a.account.session.channels = list.Items
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	if err := tt.Click("Account"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, name := range []string{"Main channel", "Brand channel", "Music channel", "Studio channel"} {
		if !tt.HasText(name) {
			t.Fatalf("account menu omitted %q: %q", name, tt.Texts())
		}
	}
	if err := tt.Click("Brand channel"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := a.account.session.channel; got != "UC-brand" {
		t.Errorf("active channel = %q, want UC-brand", got)
	}
	if got, _ := fakePageID.Load().(string); got != "UC-brand" {
		t.Errorf("YouTube request used page ID %q, want UC-brand", got)
	}
}

func TestImportedAccountChoiceSurvivesRestartAndRefresh(t *testing.T) {
	for _, choice := range []struct{ name, channel string }{
		{"Music account", ""},
		{"Music brand", "UC-music-brand"},
	} {
		t.Run(choice.name, func(t *testing.T) {
			resetFakes()
			transport := &multiAccountMusic{fresh: "SAPISID=imported"}
			store := &memoryStore{}
			path := filepath.Join(t.TempDir(), "settings.json")
			makeApp := func() *app {
				a := newTestApp(t)
				a.saver.Path = path
				t.Cleanup(a.saver.Wait)
				a.account.creds.store = store
				a.account.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
					return youtube.NewClient(youtube.Options{APIKey: "test", CookieAuth: auth, HTTPClient: &http.Client{Transport: transport}})
				}
				a.account.sources = func() []cookies.Source {
					return []cookies.Source{{Label: "Helium", Read: func(context.Context) (string, error) {
						return transport.fresh, nil
					}}}
				}
				return a
			}
			a := makeApp()
			tt := ui.NewTester(a.view, 1000, 900)
			tt.Frame()
			click := func(label string) {
				t.Helper()
				if err := tt.Click(label); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
			}
			for _, label := range []string{"Account", "Sign in", "Advanced", "Import from a browser", "Helium"} {
				click(label)
			}
			if !tt.HasText("Choose your YouTube account") || !tt.HasText("Music account") || !tt.HasText("Personal account") {
				t.Fatalf("import did not ask which account to use: %q", tt.Texts())
			}
			if store.cookie != "" || a.signedIn() {
				t.Fatal("import saved or used an account before the user chose one")
			}
			click("Back")
			click("Import from a browser")
			click("Helium")
			click("Cancel")
			if store.cookie != "" || a.signedIn() || a.account.signIn.imported != "" {
				t.Fatal("cancelling kept the imported session")
			}
			for _, label := range []string{"Account", "Sign in", "Advanced", "Import from a browser", "Helium"} {
				click(label)
			}
			transport.omitEmail = true
			transport.omitSwitcherEmail = true
			click(choice.name)
			if !tt.HasText("YouTube returned no email address for the selected Google account") || strings.Contains(a.account.signIn.err, "expired") || a.signedIn() || store.cookie != "" {
				t.Fatalf("missing identity was reported as expiry or accepted: %q", a.account.signIn.err)
			}
			transport.omitSwitcherEmail, transport.swapped = false, true
			click(choice.name)
			if !tt.HasText("YouTube returned a different Google account") || strings.Contains(a.account.signIn.err, "expired") || a.signedIn() || store.cookie != "" {
				t.Fatalf("wrong identity was reported as expiry or accepted: %q", a.account.signIn.err)
			}
			transport.swapped = false
			// A valid switcher email verifies the selected index even when
			// Music omits it, including on restart and cookie renewal.
			click(choice.name)
			if !a.signedIn() || a.account.session.details.Email != "music@example.test" || transport.index != "1" || transport.pageID != choice.channel {
				t.Fatalf("wrong imported identity: index=%q channel=%q session=%v", transport.index, transport.pageID, a.signedIn())
			}
			a.saver.Wait()
			saved := settings.Load(path)
			if saved.AccountIndex != 1 || saved.Channel != choice.channel || saved.SignInSource != "Helium" || saved.AccountEmail != "music@example.test" {
				t.Fatalf("account choice was not saved: index=%d channel=%q browser=%q email=%q", saved.AccountIndex, saved.Channel, saved.SignInSource, saved.AccountEmail)
			}
			restored := makeApp()
			restored.settings = saved
			restored.restoreAccount()
			if !restored.signedIn() || restored.account.session.details.Email != "music@example.test" || transport.index != "1" || transport.pageID != choice.channel {
				t.Fatal("restart lost the selected account or channel")
			}
			transport.fresh = "SAPISID=refreshed"
			restored.checkSession()
			if !restored.signedIn() || restored.account.session.details.Email != "music@example.test" || store.cookie != transport.fresh || transport.index != "1" || transport.pageID != choice.channel {
				t.Fatal("refresh lost the selected account or channel")
			}
			if choice.channel == "" {
				tt = ui.NewTester(restored.view, 1000, 900)
				tt.Frame()
				click("Account")
				click("Music brand")
				if transport.index != "1" || transport.pageID != "UC-music-brand" {
					t.Fatalf("channel switch lost the Google account: index=%q channel=%q", transport.index, transport.pageID)
				}
				click("Account")
				click("Personal account")
				if transport.index != "0" || transport.pageID != "" || restored.account.session.details.Email != "personal@example.test" {
					t.Fatal("account menu could not switch back to the first Google account")
				}
			}
			// A saved index must not silently select another person if Google
			// changes the order of the accounts within the browser session.
			transport.swapped = true
			reordered := makeApp()
			reordered.settings = saved
			reordered.restoreAccount()
			if reordered.signedIn() {
				t.Fatal("reordered Google accounts silently restored the wrong person")
			}
		})
	}
}
