package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elianiva/meiro/internal/cookies"
	"github.com/elianiva/meiro/youtube"
)

// session is a signed-in account: the client that speaks for it, the cookie
// it signs in with, and whom it signs in as. The app is signed in exactly
// when it holds one, so no part of the account can be set without the rest.
type session struct {
	client  *youtube.Client
	cookie  string
	details youtube.AccountDetails
	// channel is the channel the session acts as, or "" for the account's
	// default one.
	channel string
	// channels are the channels the account can act as, once they are read.
	channels []youtube.AccountChannel
}

// name is what the account is called, for its menu.
func (s *session) name() string {
	if s.details.Name != "" {
		return s.details.Name
	}
	return "Signed in"
}

// accountState is the account in use, and the work that signs in, restores,
// checks and switches it.
type accountState struct {
	// public browses without an account. newClient makes the client that
	// carries an account's cookie, and sources lists the browsers a session
	// can be imported from; tests replace both.
	public    *youtube.Client
	newClient func(*youtube.CookieAuth) *youtube.Client
	sources   func() []cookies.Source

	// session is the account in use, nil while signed out.
	session *session
	// expired is why the session ended when YouTube stopped accepting it, ""
	// otherwise. Pages that need an account say it in place of the plain
	// invitation to sign in, until a session is in use again or the user
	// signs out.
	expired string
	creds   credentials
	signIn  signInState

	// restore, check and switching read or change the account in the
	// background. Every change of account stops them, so none of them can
	// put back an account the user has left.
	restore   task
	check     task
	switching task
}

func newAccountState(newClient func(*youtube.CookieAuth) *youtube.Client, sources func() []cookies.Source) accountState {
	return accountState{public: youtube.NewClient(youtube.Options{}), newClient: newClient, sources: sources}
}

// cookie is the cookie of the session in use, or "" while signed out.
func (s *accountState) cookie() string {
	if s.session != nil {
		return s.session.cookie
	}
	return ""
}

// credentials is the saved sign-in, and the rules for writing it. Writes run
// off the main thread, and a write already on its way must never undo a
// dismissed sign-in or a sign-out, so every write checks, under mu, that the
// attempt it was asked for is still current.
type credentials struct {
	// store keeps the cookie between runs; nil keeps nothing.
	store cookies.Store
	mu    sync.Mutex
	// attempt is advanced by every sign-in dialog, its dismissal and every
	// sign-out.
	attempt atomic.Uint64
	// owner is the dialog attempt whose cookie the store holds, or 0 when no
	// dialog owns it. Guarded by mu.
	owner uint64
	// deletePending is set while a sign-out removes the saved cookie, so a
	// new sign-in waits for it. It belongs to the main thread.
	deletePending bool
}

// credentialTimeout bounds one use of the credential store.
const credentialTimeout = 30 * time.Second

// load reads the saved cookie.
func (c *credentials) load(ctx context.Context) (string, error) {
	if c.store == nil {
		return "", cookies.ErrNotSaved
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.Load(ctx)
}

// saveForDialog keeps a cookie a sign-in dialog validated, only while that
// dialog's attempt is current. When the dialog was dismissed during the
// write, the cookie is removed again before a newer write may proceed.
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
	// The write's own context may be what ended; the cleanup must not be.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), credentialTimeout)
	defer cancel()
	if err := c.store.Delete(cleanup); err != nil {
		return errors.Join(context.Canceled, err)
	}
	c.owner = 0
	return context.Canceled
}

// deleteForDialog removes the saved cookie only if a dismissed dialog's
// attempt still owns it, so a newer sign-in keeps its own.
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

// saveRestored keeps a session read from the browser again, in place of one
// YouTube stopped accepting, unless a sign-in or a sign-out began while it
// was being read: those are newer, and the cookie of a session the user
// ended must not come back.
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
	// No dialog owns this cookie, so a dismissed attempt must not remove it.
	c.owner = 0
	return nil
}

// delete removes the saved cookie, for a sign-out.
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

// errSessionGone is YouTube answering that a cookie signs in to nothing. The
// session that cookie held is over: YouTube retires the cookie of a browser
// session as the browser goes on using it, so the browser the session came
// from is where a replacement comes from.
var errSessionGone = errors.New("YouTube did not recognise the cookie as a signed-in session; it may have expired")

// openSession signs in with cookie as channel, and falls back to the
// account's default channel when that one is gone. It runs off the main
// thread.
func (s *accountState) openSession(ctx context.Context, cookie, channel string) (*session, error) {
	open := func(channel string) (*session, error) {
		auth, err := youtube.NewCookieAuth(cookie, youtube.CookieOptions{OnBehalfOfUser: channel})
		if err != nil {
			return nil, err
		}
		client := s.newClient(auth)
		details, err := client.GetAccountDetails(ctx)
		if errors.Is(err, youtube.ErrUnauthorized) {
			return nil, errSessionGone
		}
		if err != nil {
			return nil, err
		}
		// YouTube answers a retired session with signed-out content rather
		// than an error: an account without a name or a channel is none.
		if details == nil || details.Name == "" && details.ChannelID == "" {
			return nil, errSessionGone
		}
		return &session{client: client, cookie: cookie, details: *details, channel: channel}, nil
	}
	opened, err := open(channel)
	if err != nil && channel != "" && !errors.Is(err, errSessionGone) {
		return open("")
	}
	return opened, err
}

// sessionSource is what a sign-in remembers to sign in again with: the
// channel it acts as and the browser it came from, "" for a pasted one.
type sessionSource struct {
	channel string
	browser string
}

// signInTimeout bounds reading a browser's session and asking YouTube about
// it, with room for a keyring prompt and a slow profile.
const signInTimeout = 90 * time.Second

// renewSession signs in with a saved cookie. When YouTube no longer
// recognises it, the session is read from the browser it was imported from,
// which is the one place that still holds a session YouTube accepts, and that
// session is kept instead. It runs off the main thread.
func (s *accountState) renewSession(ctx context.Context, cookie string, from sessionSource, attempt uint64) (*session, error) {
	opened, err := s.openSession(ctx, cookie, from.channel)
	if err == nil || !errors.Is(err, errSessionGone) {
		return opened, err
	}
	browser, ok := cookies.Find(s.sources(), from.browser)
	if !ok {
		return nil, err
	}
	// Reading a browser runs a subprocess with a budget of its own, within
	// the caller's, which leaves room for it.
	readCtx, cancel := context.WithTimeout(ctx, signInTimeout)
	defer cancel()
	fresh, readErr := browser.Read(readCtx)
	if readErr == nil {
		opened, readErr = s.openSession(readCtx, fresh, from.channel)
	}
	if readErr != nil {
		// What the browser ran into is worth adding, unless it is the same
		// answer the saved cookie already got.
		if errors.Is(readErr, errSessionGone) {
			return nil, err
		}
		return nil, errors.Join(err, readErr)
	}
	// A session that works is worth using whether or not the store takes it:
	// one that refuses to save is a problem the next run reports.
	_ = s.creds.saveRestored(readCtx, fresh, attempt)
	return opened, nil
}

// useSession puts a session that works into use, keeps where it came from,
// and reloads the page the change of account leaves stale.
func (a *app) useSession(s *session, from sessionSource) {
	a.stopAccountWork()
	a.account.session = s
	a.account.expired = ""
	a.account.signIn.err = ""
	if a.settings.Channel != s.channel || a.settings.SignInSource != from.browser {
		a.settings.Channel, a.settings.SignInSource = s.channel, from.browser
		a.saveSettings()
	}
	a.accountChanged()
	// The channel switcher fills in after the account is in use, so a slow
	// channel list does not hold up the sign-in. The list is filed with the
	// session it was read for, wherever the account has gone since.
	spawn(a, func() []youtube.AccountChannel {
		ctx, cancel := context.WithTimeout(context.Background(), credentialTimeout)
		defer cancel()
		list, err := s.client.GetAccounts(ctx)
		if err != nil || list == nil {
			return nil
		}
		return list.Items
	}, func(channels []youtube.AccountChannel) { s.channels = channels })
}

// endSession forgets the account in use, with why when there is a reason
// the user should see.
func (a *app) endSession(reason string) {
	a.stopAccountWork()
	had := a.account.session != nil
	a.account.session = nil
	a.account.expired = ""
	a.account.signIn.err = reason
	if reason != "" {
		a.notice = reason
	}
	if had {
		a.accountChanged()
	}
}

// sessionExpiredNotice is what the user is told when YouTube stops accepting
// their session and the browser it came from cannot replace it.
const sessionExpiredNotice = "Your session expired. Sign in again."

// expireSession ends a session YouTube no longer accepts, and makes the pages
// that need an account ask the user to sign in again.
func (a *app) expireSession() {
	a.endSession(sessionExpiredNotice)
	a.account.expired = sessionExpiredNotice
}

// stopAccountWork drops the background work of the account being left.
func (a *app) stopAccountWork() {
	a.account.restore.stop()
	a.account.check.stop()
	a.account.switching.stop()
}

// accountChanged reloads the page when it is one the account changes. The
// home page is loaded at launch, before the saved sign-in is back, so it
// needs the reload as much as the library does.
func (a *app) accountChanged() {
	a.cancelSuggestions()
	a.pages.Clear()
	if r, _ := a.route(); r.load != nil {
		a.reloadPage()
	}
}

// accountTimeout bounds asking YouTube about an account.
const accountTimeout = time.Minute

// restoreAccount signs in with the cookie an earlier run saved.
func (a *app) restoreAccount() {
	acc := &a.account
	if acc.creds.store == nil {
		return
	}
	from := sessionSource{channel: a.settings.Channel, browser: a.settings.SignInSource}
	attempt := acc.creds.attempt.Load()
	startTask(a, &acc.restore, accountTimeout+signInTimeout, func(ctx context.Context) (*session, error) {
		cookie, err := acc.creds.load(ctx)
		if err != nil {
			return nil, err
		}
		return acc.renewSession(ctx, cookie, from, attempt)
	}, func(s *session, err error) {
		switch {
		case errors.Is(err, cookies.ErrNotSaved):
		case errors.Is(err, errSessionGone):
			a.expireSession()
		case err != nil:
			a.endSession("Could not restore your YouTube Music session: " + err.Error())
		default:
			a.useSession(s, from)
		}
	})
}

// sessionCheckEvery is how often a running app asks YouTube whether the
// session it holds is still taken. YouTube retires the cookies of a browser
// session as the browser goes on using them, and answers the requests of a
// retired one with signed-out content rather than an error, so a page the app
// shows says nothing about whether the account is still there.
const sessionCheckEvery = time.Minute

// watchSession checks the session for as long as the app runs.
func (a *app) watchSession() {
	a.every(sessionCheckEvery, func() { a.update(a.checkSession) })
}

// checkSession asks YouTube whether the session in use is still taken, and
// reads the browser it came from when it is not.
func (a *app) checkSession() {
	acc := &a.account
	current := acc.session
	if current == nil || acc.check.busy() {
		return
	}
	from := sessionSource{channel: a.settings.Channel, browser: a.settings.SignInSource}
	attempt := acc.creds.attempt.Load()
	startTask(a, &acc.check, accountTimeout+signInTimeout, func(ctx context.Context) (*session, error) {
		return acc.renewSession(ctx, current.cookie, from, attempt)
	}, func(s *session, err error) {
		switch {
		case err == nil && s.cookie == current.cookie:
			// YouTube still takes the session: there is nothing to change.
		case err == nil:
			// The browser replaced a session YouTube had retired.
			a.useSession(s, from)
		case errors.Is(err, errSessionGone):
			// YouTube no longer takes the session, and the browser it came
			// from could not replace it: say so rather than show pages the
			// account no longer has.
			a.expireSession()
		}
		// A request that failed for another reason says nothing about the
		// session; the next check tries again.
	})
}

// switchChannel makes the session act as another of the account's channels
// and reloads the page, so it shows that channel's account.
func (a *app) switchChannel(channel string) {
	current := a.account.session
	if current == nil || channel == current.channel {
		return
	}
	from := sessionSource{channel: channel, browser: a.settings.SignInSource}
	startTask(a, &a.account.switching, accountTimeout, func(ctx context.Context) (*session, error) {
		auth, err := youtube.NewCookieAuth(current.cookie, youtube.CookieOptions{OnBehalfOfUser: channel})
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
		return &session{client: client, cookie: current.cookie, details: *details, channel: channel}, nil
	}, func(s *session, err error) {
		if err != nil {
			a.notice = "Could not switch channel: " + err.Error()
			return
		}
		a.useSession(s, from)
	})
}

// signOut forgets the account and removes its saved cookie.
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

// signInState is the sign-in dialog, while it is open.
type signInState struct {
	open bool
	err  string
	// attempt is the credential attempt this dialog owns.
	attempt uint64
	// cookie is the text the user pasted.
	cookie string
	// browsers is the menu of browsers to import from.
	browsers bool
	// try checks a pasted or imported session; it is busy while it does.
	try task
}

// reset empties the dialog. Its task is the caller's to stop.
func (s *signInState) reset() {
	s.open, s.err, s.attempt, s.cookie, s.browsers = false, "", 0, "", false
}

// openSignIn opens the dialog that takes the cookie of the user's browser
// session. YouTube Music does not let another app sign in to it: the
// account's home page and library are only served to a request that carries
// the cookie of a signed-in browser.
func (a *app) openSignIn() {
	acc := &a.account
	if acc.signIn.open {
		return
	}
	if acc.creds.deletePending {
		a.notice = "Wait for sign-out to finish before signing in again."
		return
	}
	acc.signIn.reset()
	acc.signIn.open, acc.signIn.attempt = true, acc.creds.attempt.Add(1)
}

// syncSignInDialog notices a dialog the user closed, by its scrim or the
// Escape key, and dismisses what it left behind.
func (a *app) syncSignInDialog() {
	s := &a.account.signIn
	if !s.open && (s.attempt != 0 || s.try.busy() || s.cookie != "") {
		a.dismissSignIn()
	}
}

// dismissSignIn closes the dialog, stops its check and drops the pasted
// cookie. A session saved by a check that lost this race is removed in the
// background, without touching a newer sign-in.
func (a *app) dismissSignIn() {
	acc := &a.account
	attempt := acc.signIn.attempt
	acc.creds.attempt.Add(1)
	acc.signIn.try.stop()
	acc.signIn.reset()
	if attempt == 0 || acc.creds.store == nil {
		return
	}
	spawn(a, func() error { return acc.creds.deleteForDialog(attempt) }, func(err error) {
		if err != nil {
			a.notice = "The cancelled sign-in could not be removed from storage: " + err.Error()
		}
	})
}

// submitSignIn tries the pasted cookie.
func (a *app) submitSignIn() {
	s := &a.account.signIn
	if s.try.busy() {
		return
	}
	cookie := cookies.Header(s.cookie)
	if _, err := youtube.NewCookieAuth(cookie, youtube.CookieOptions{}); err != nil {
		s.err = "That is not the cookie of a signed-in session: it has no SAPISID. Copy the whole value of the Cookie header."
		return
	}
	// A pasted session came from no browser, so it names none.
	a.trySignIn(sessionSource{channel: a.settings.Channel}, func(context.Context) (string, error) { return cookie, nil })
}

// importSignIn takes the session of a browser the user is signed in with.
func (a *app) importSignIn(browser cookies.Source) {
	if a.account.signIn.try.busy() {
		return
	}
	a.trySignIn(sessionSource{channel: a.settings.Channel, browser: browser.Label}, browser.Read)
}

// trySignIn reads a cookie, asks YouTube whom it signs in, and keeps it as
// the sign-in when YouTube takes it.
func (a *app) trySignIn(from sessionSource, read func(context.Context) (string, error)) {
	acc := &a.account
	s := &acc.signIn
	s.err = ""
	attempt := s.attempt
	startTask(a, &s.try, signInTimeout, func(ctx context.Context) (*session, error) {
		cookie, err := read(ctx)
		if err != nil {
			return nil, err
		}
		opened, err := acc.openSession(ctx, cookie, from.channel)
		if err != nil {
			return nil, err
		}
		return opened, acc.creds.saveForDialog(ctx, cookie, attempt)
	}, func(opened *session, err error) {
		if err != nil {
			s.err = err.Error()
			return
		}
		s.reset()
		// The browser the session came from is where it is read again once
		// YouTube retires its cookie. A pasted session names none, which is
		// also what forgets a browser the user has moved on from.
		a.useSession(opened, sessionSource{channel: opened.channel, browser: from.browser})
	})
}
