package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elianiva/meiro/internal/cookies"
	"github.com/elianiva/meiro/internal/secretfile"
	"github.com/elianiva/meiro/youtube"
)

type session struct {
	client       *youtube.Client
	cookie       string
	details      youtube.AccountDetails
	channel      string
	accountIndex int
	channels     []youtube.AccountChannel
}

func (s *session) name() string {
	if s.details.Name != "" {
		return s.details.Name
	}
	return "Signed in"
}

type accountState struct {
	public    *youtube.Client
	newClient func(*youtube.CookieAuth) *youtube.Client
	sources   func() []cookies.Source
	window    func(ctx context.Context) (string, error)

	session *session
	expired string
	creds   credentials
	signIn  signInState

	restore   task
	check     task
	switching task
}

func newAccountState(newClient func(*youtube.CookieAuth) *youtube.Client, sources func() []cookies.Source) accountState {
	return accountState{
		public: youtube.NewClient(youtube.Options{}), newClient: newClient, sources: sources,
		window: func(ctx context.Context) (string, error) {
			return cookies.SignInWindow(ctx, cookies.WindowOptions{})
		},
	}
}

type credentials struct {
	store         cookies.Store
	mu            sync.Mutex
	attempt       atomic.Uint64
	owner         uint64
	deletePending bool
}

const credentialTimeout = 30 * time.Second

func (c *credentials) load(ctx context.Context) (string, error) {
	if c.store == nil {
		return "", secretfile.ErrNotSaved
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.Load(ctx)
}

func (c *credentials) saveForDialog(ctx context.Context, cookie string, attempt uint64) error {
	if c.store == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil || c.attempt.Load() != attempt {
		return context.Canceled
	}
	if err := c.store.Save(ctx, cookie); err != nil {
		return err
	}
	c.owner = attempt
	if ctx.Err() == nil && c.attempt.Load() == attempt {
		return nil
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), credentialTimeout)
	defer cancel()
	if err := c.store.Delete(cleanup); err != nil {
		return errors.Join(context.Canceled, err)
	}
	c.owner = 0
	return context.Canceled
}

func (c *credentials) deleteForDialog(attempt uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store == nil || c.owner != attempt {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), credentialTimeout)
	defer cancel()
	if err := c.store.Delete(ctx); err != nil {
		return err
	}
	c.owner = 0
	return nil
}

func (c *credentials) saveRestored(ctx context.Context, cookie string, attempt uint64) error {
	if c.store == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.attempt.Load() != attempt {
		return nil
	}
	if err := c.store.Save(ctx, cookie); err != nil {
		return err
	}
	c.owner = 0
	return nil
}

func (c *credentials) delete(ctx context.Context) error {
	if c.store == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.store.Delete(ctx); err != nil {
		return err
	}
	c.owner = 0
	return nil
}

var errSessionGone = errors.New("YouTube did not recognise the cookie as a signed-in session; it may have expired")

func (s *accountState) openSession(ctx context.Context, cookie string, from sessionSource) (*session, error) {
	open := func(channel string) (*session, error) {
		auth, err := youtube.NewCookieAuth(cookie, youtube.CookieOptions{AccountIndex: from.accountIndex, OnBehalfOfUser: channel})
		if err != nil {
			return nil, err
		}
		client := s.newClient(auth)
		details, err := client.GetAccountDetails(ctx)
		if errors.Is(err, youtube.ErrUnauthorized) {
			return nil, fmt.Errorf("YouTube rejected the session for Google account %d: %w", from.accountIndex, errSessionGone)
		}
		if err != nil {
			return nil, err
		}
		if details == nil || details.Name == "" && details.ChannelID == "" {
			return nil, fmt.Errorf("YouTube returned no account name or channel for Google account %d: %w", from.accountIndex, errSessionGone)
		}
		if from.email != "" {
			if details.Email == "" {
				return nil, errors.New("YouTube returned no email address for the selected Google account; Meiro could not verify your selection")
			}
			if !strings.EqualFold(details.Email, from.email) {
				return nil, errors.New("YouTube returned a different Google account than the one you selected; Meiro did not save the session")
			}
		}
		return &session{client: client, cookie: cookie, details: *details, channel: channel, accountIndex: from.accountIndex}, nil
	}
	opened, err := open(from.channel)
	if err != nil && from.channel != "" && !errors.Is(err, errSessionGone) {
		return open("")
	}
	return opened, err
}

type sessionSource struct {
	channel      string
	browser      string
	accountIndex int
	email        string
}

const signInTimeout = 90 * time.Second

func (s *accountState) renewSession(ctx context.Context, cookie string, from sessionSource, attempt uint64) (*session, error) {
	opened, err := s.openSession(ctx, cookie, from)
	if err == nil || !errors.Is(err, errSessionGone) {
		return opened, err
	}
	browser, ok := cookies.Find(s.sources(), from.browser)
	if !ok {
		return nil, err
	}
	readCtx, cancel := context.WithTimeout(ctx, signInTimeout)
	defer cancel()
	fresh, readErr := browser.Read(readCtx)
	if readErr == nil {
		opened, readErr = s.openSession(readCtx, fresh, from)
	}
	if readErr != nil {
		if errors.Is(readErr, errSessionGone) {
			return nil, err
		}
		return nil, errors.Join(err, readErr)
	}
	_ = s.creds.saveRestored(readCtx, fresh, attempt)
	return opened, nil
}

func (a *app) useSession(s *session, from sessionSource) {
	a.stopAccountWork()
	first := a.account.session == nil
	if a.account.session != s {
		a.finishEpisodeProgress(false)
	}
	a.account.session = s
	a.syncAudioAuth()
	a.account.expired = ""
	a.account.signIn.err = ""
	if a.settings.Channel != s.channel || a.settings.SignInSource != from.browser ||
		a.settings.AccountIndex != s.accountIndex || a.settings.AccountEmail != s.details.Email {
		a.settings.Channel, a.settings.SignInSource = s.channel, from.browser
		a.settings.AccountIndex, a.settings.AccountEmail = s.accountIndex, s.details.Email
		a.saveSettings()
	}
	a.accountChanged(!first)
	a.offerPremium()
	spawn(a, func() []youtube.AccountChannel {
		ctx, cancel := context.WithTimeout(context.Background(), credentialTimeout)
		defer cancel()
		list, err := s.client.GetSignInAccounts(ctx)
		if err != nil || list == nil {
			return nil
		}
		return list.Items
	}, func(channels []youtube.AccountChannel) { s.channels = channels })
}

func (a *app) endSession(reason string) {
	a.stopAccountWork()
	had := a.account.session != nil
	a.finishEpisodeProgress(false)
	a.account.session = nil
	a.syncAudioAuth()
	a.account.expired = ""
	a.account.signIn.err = reason
	if reason != "" {
		a.notice = reason
	}
	if had {
		a.accountChanged(true)
	}
}

const sessionExpiredNotice = "Your session expired. Sign in again."

func (a *app) expireSession() {
	a.endSession(sessionExpiredNotice)
	a.account.expired = sessionExpiredNotice
}

func (a *app) stopAccountWork() {
	a.account.restore.stop()
	a.account.check.stop()
	a.account.switching.stop()
}

// accountChanged resets what the app learned from the previous account. The
// first session after a start keeps the ratings restored with the queue, since
// signing in again is not a change of account.
func (a *app) accountChanged(forgetRatings bool) {
	a.cancelSuggestions()
	a.pages.Clear()
	if forgetRatings {
		a.forgetAccountState()
	}
	if r, _ := a.route(); r.load != nil {
		a.reloadPage()
	}
}

const accountTimeout = time.Minute

func (a *app) restoreAccount() {
	acc := &a.account
	if acc.creds.store == nil {
		return
	}
	from := sessionSource{channel: a.settings.Channel, browser: a.settings.SignInSource, accountIndex: a.settings.AccountIndex, email: a.settings.AccountEmail}
	attempt := acc.creds.attempt.Load()
	startTask(a, &acc.restore, accountTimeout+signInTimeout, func(ctx context.Context) (*session, error) {
		cookie, err := acc.creds.load(ctx)
		if err != nil {
			return nil, err
		}
		return acc.renewSession(ctx, cookie, from, attempt)
	}, func(s *session, err error) {
		switch {
		case errors.Is(err, secretfile.ErrNotSaved):
		case errors.Is(err, errSessionGone):
			a.expireSession()
		case err != nil:
			a.endSession("Could not restore your YouTube Music session: " + err.Error())
		default:
			a.useSession(s, from)
		}
	})
}

const sessionCheckEvery = time.Minute

func (a *app) watchSession() {
	a.every(sessionCheckEvery, func() { a.update(a.checkSession) })
}

func (a *app) checkSession() {
	acc := &a.account
	current := acc.session
	if current == nil || acc.check.busy() {
		return
	}
	from := sessionSource{channel: a.settings.Channel, browser: a.settings.SignInSource, accountIndex: a.settings.AccountIndex, email: a.settings.AccountEmail}
	attempt := acc.creds.attempt.Load()
	startTask(a, &acc.check, accountTimeout+signInTimeout, func(ctx context.Context) (*session, error) {
		return acc.renewSession(ctx, current.cookie, from, attempt)
	}, func(s *session, err error) {
		switch {
		case err == nil && s.cookie == current.cookie:
		case err == nil:
			a.useSession(s, from)
		case errors.Is(err, errSessionGone):
			a.expireSession()
		}
	})
}

func (a *app) switchChannel(channel youtube.AccountChannel, marked bool) {
	current := a.account.session
	if current == nil {
		return
	}
	if channel.ChannelID == current.channel && channel.AccountIndex == current.accountIndex {
		if marked {
			a.notice = "Could not switch to “" + channelName(channel) + "”: YouTube did not name that channel."
		}
		return
	}
	from := sessionSource{channel: channel.ChannelID, browser: a.settings.SignInSource, accountIndex: channel.AccountIndex}
	startTask(a, &a.account.switching, accountTimeout, func(ctx context.Context) (*session, error) {
		auth, err := youtube.NewCookieAuth(current.cookie, youtube.CookieOptions{AccountIndex: channel.AccountIndex, OnBehalfOfUser: channel.ChannelID})
		if err != nil {
			return nil, err
		}
		client := a.account.newClient(auth)
		details, err := client.GetAccountDetails(ctx)
		if err == nil && (details == nil || details.Name == "" && details.ChannelID == "") {
			err = errSessionGone
		}
		if err != nil {
			return nil, err
		}
		return &session{client: client, cookie: current.cookie, details: *details, channel: channel.ChannelID, accountIndex: channel.AccountIndex}, nil
	}, func(s *session, err error) {
		if err != nil {
			a.notice = "Could not switch channel: " + err.Error()
			return
		}
		a.useSession(s, from)
	})
}

func (a *app) signOut() {
	acc := &a.account
	acc.creds.attempt.Add(1)
	acc.signIn.try.stop()
	acc.signIn.reset()
	a.endSession("")
	if acc.creds.store == nil {
		return
	}
	acc.creds.deletePending = true
	spawn(a, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), credentialTimeout)
		defer cancel()
		return acc.creds.delete(ctx)
	}, func(err error) {
		acc.creds.deletePending = false
		if err != nil {
			a.notice = "Signed out, but the saved session could not be removed: " + err.Error()
		}
	})
}
