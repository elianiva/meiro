//go:build linux

package systemmedia

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

const (
	busNamePrefix   = "org.mpris.MediaPlayer2.meiro"
	objectPath      = dbus.ObjectPath("/org/mpris/MediaPlayer2")
	rootInterface   = "org.mpris.MediaPlayer2"
	playerInterface = "org.mpris.MediaPlayer2.Player"
	volumeInterval  = 50 * time.Millisecond
)

type linuxSession struct {
	conn        *dbus.Conn
	busName     string
	props       *prop.Properties
	mu          sync.Mutex
	state       State
	hasState    bool
	lastPos     time.Time
	volumeTimer *time.Timer
	closed      bool
	err         error
}

// New registers Meiro as an MPRIS player on the current user's D-Bus session.
func New(controls Controls) (Session, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("connect to the D-Bus session: %w", err)
	}
	closeOnError := func(err error) (Session, error) {
		_ = conn.Close()
		return nil, err
	}
	name := fmt.Sprintf("%s.instance%d", busNamePrefix, os.Getpid())
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil {
		return closeOnError(fmt.Errorf("request MPRIS bus name: %w", err))
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return closeOnError(errors.New("another Meiro instance already owns the MPRIS bus name"))
	}

	s := &linuxSession{conn: conn, busName: name}
	root := &mprisRoot{}
	api := &mprisPlayer{
		controls: controls,
		trackID: func() dbus.ObjectPath {
			s.mu.Lock()
			defer s.mu.Unlock()
			return trackPath(s.state)
		},
	}
	if err := conn.Export(root, objectPath, rootInterface); err != nil {
		return closeOnError(fmt.Errorf("export MPRIS root interface: %w", err))
	}
	if err := conn.ExportWithMap(api, map[string]string{"SeekBy": "Seek"}, objectPath, playerInterface); err != nil {
		return closeOnError(fmt.Errorf("export MPRIS player interface: %w", err))
	}
	properties, err := prop.Export(conn, objectPath, propertyMap(controls))
	if err != nil {
		return closeOnError(fmt.Errorf("export MPRIS properties: %w", err))
	}
	s.props = properties
	if err := conn.Export(&mprisProperties{Properties: properties, session: s}, objectPath, "org.freedesktop.DBus.Properties"); err != nil {
		return closeOnError(fmt.Errorf("export MPRIS metadata properties: %w", err))
	}
	playerMethods := introspect.Methods(api)
	for i := range playerMethods {
		if playerMethods[i].Name == "SeekBy" {
			playerMethods[i].Name = "Seek"
		}
	}
	introspection := &introspect.Node{
		Name: string(objectPath),
		Interfaces: []introspect.Interface{
			introspect.IntrospectData,
			prop.IntrospectData,
			{
				Name:       rootInterface,
				Methods:    introspect.Methods(root),
				Properties: properties.Introspection(rootInterface),
			},
			{
				Name:       playerInterface,
				Methods:    playerMethods,
				Properties: properties.Introspection(playerInterface),
				Signals: []introspect.Signal{{
					Name: "Seeked", Args: []introspect.Arg{{Name: "Position", Type: "x"}},
				}},
			},
		},
	}
	if err := conn.Export(introspect.NewIntrospectable(introspection), objectPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		return closeOnError(fmt.Errorf("export MPRIS introspection: %w", err))
	}
	return s, nil
}

func propertyMap(controls Controls) prop.Map {
	return prop.Map{
		rootInterface: {
			"CanQuit":             {Value: false, Emit: prop.EmitConst},
			"CanRaise":            {Value: false, Emit: prop.EmitConst},
			"HasTrackList":        {Value: false, Emit: prop.EmitConst},
			"Identity":            {Value: "Meiro", Emit: prop.EmitConst},
			"DesktopEntry":        {Value: "meiro", Emit: prop.EmitConst},
			"SupportedUriSchemes": {Value: []string{}, Emit: prop.EmitConst},
			"SupportedMimeTypes":  {Value: []string{}, Emit: prop.EmitConst},
		},
		playerInterface: {
			"PlaybackStatus": {Value: string(Stopped), Emit: prop.EmitTrue},
			"LoopStatus": {
				Value: string(LoopNone), Writable: true, Emit: prop.EmitTrue,
				Callback: func(change *prop.Change) *dbus.Error {
					status, ok := change.Value.(string)
					if loop := Loop(status); !ok || !loop.valid() {
						return dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", nil)
					}
					if controls.SetLoop != nil {
						controls.SetLoop(Loop(status))
					}
					return nil
				},
			},
			"Rate": {
				Value: 1.0, Writable: true, Emit: prop.EmitConst,
				Callback: func(change *prop.Change) *dbus.Error {
					if rate, ok := change.Value.(float64); !ok || rate != 1 {
						return dbus.NewError("org.freedesktop.DBus.Error.NotSupported", nil)
					}
					return nil
				},
			},
			"Shuffle": {
				Value: false, Writable: true, Emit: prop.EmitTrue,
				Callback: func(change *prop.Change) *dbus.Error {
					if on, ok := change.Value.(bool); ok && controls.SetShuffle != nil {
						controls.SetShuffle(on)
					}
					return nil
				},
			},
			"Metadata": {Value: trackMetadata(State{}), Emit: prop.EmitTrue},
			"Volume": {
				Value: 0.5, Writable: true, Emit: prop.EmitTrue,
				Callback: func(change *prop.Change) *dbus.Error {
					if volume, ok := change.Value.(float64); ok && controls.SetVolume != nil {
						controls.SetVolume(volume)
					}
					return nil
				},
			},
			"Position":      {Value: int64(0), Emit: prop.EmitFalse},
			"MinimumRate":   {Value: 1.0, Emit: prop.EmitConst},
			"MaximumRate":   {Value: 1.0, Emit: prop.EmitConst},
			"CanGoNext":     {Value: false, Emit: prop.EmitTrue},
			"CanGoPrevious": {Value: false, Emit: prop.EmitTrue},
			"CanPlay":       {Value: false, Emit: prop.EmitTrue},
			"CanPause":      {Value: false, Emit: prop.EmitTrue},
			"CanSeek":       {Value: false, Emit: prop.EmitTrue},
			"CanControl":    {Value: true, Emit: prop.EmitConst},
		},
	}
}

func (s *linuxSession) Update(state State) error {
	if state.Status == "" {
		state.Status = Stopped
	}
	if !state.Loop.valid() {
		state.Loop = LoopNone
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if s.closed {
		return errors.New("MPRIS session is closed")
	}

	statusChanged := !s.hasState || state.Status != s.state.Status
	metadataChanged := !s.hasState || !state.sameMetadata(s.state)
	if statusChanged {
		if err := s.setProperty("PlaybackStatus", string(state.Status)); err != nil {
			return s.fail(err)
		}
	}
	if (!s.hasState || state.Loop != s.state.Loop) && string(state.Loop) != s.props.GetMust(playerInterface, "LoopStatus") {
		if err := s.setProperty("LoopStatus", string(state.Loop)); err != nil {
			return s.fail(err)
		}
	}
	if metadataChanged {
		// prop.Properties updates map values in place, preserving keys that a
		// subsequent track omits. Send the complete replacement explicitly;
		// mprisProperties serves the same snapshot to property readers.
		if err := s.conn.Emit(objectPath, "org.freedesktop.DBus.Properties.PropertiesChanged",
			playerInterface, map[string]dbus.Variant{"Metadata": dbus.MakeVariant(trackMetadata(state))}, []string{}); err != nil {
			return s.fail(fmt.Errorf("publish MPRIS metadata: %w", err))
		}
	}
	if !s.hasState {
		if state.Volume != s.props.GetMust(playerInterface, "Volume") {
			if err := s.setProperty("Volume", state.Volume); err != nil {
				return s.fail(err)
			}
		}
	} else if state.Volume != s.state.Volume && s.volumeTimer == nil {
		// Do not reset the timer during a drag: continuously changing volume
		// must still reach the OS within one interval, including its final value.
		s.volumeTimer = time.AfterFunc(volumeInterval, s.publishVolume)
	}
	if (!s.hasState || state.Shuffle != s.state.Shuffle) && state.Shuffle != s.props.GetMust(playerInterface, "Shuffle") {
		if err := s.setProperty("Shuffle", state.Shuffle); err != nil {
			return s.fail(err)
		}
	}
	for _, capability := range [...]struct {
		property string
		value    bool
		previous bool
	}{
		{"CanGoNext", state.CanNext, s.state.CanNext},
		{"CanGoPrevious", state.CanPrevious, s.state.CanPrevious},
		{"CanPlay", state.CanPlay, s.state.CanPlay},
		{"CanPause", state.CanPause, s.state.CanPause},
		{"CanSeek", state.CanSeek, s.state.CanSeek},
	} {
		if !s.hasState || capability.previous != capability.value {
			if err := s.setProperty(capability.property, capability.value); err != nil {
				return s.fail(err)
			}
		}
	}
	seeked := s.hasState && state.SeekVersion != s.state.SeekVersion
	positionChangedWhilePaused := state.Status != Playing && state.Position != s.state.Position
	if !s.hasState || statusChanged || seeked || positionChangedWhilePaused || durationDistance(state.Position, s.state.Position) > 3*time.Second || time.Since(s.lastPos) >= time.Second {
		if err := s.setProperty("Position", int64(state.Position/time.Microsecond)); err != nil {
			return s.fail(err)
		}
		s.lastPos = time.Now()
	}
	if seeked {
		if err := s.conn.Emit(objectPath, playerInterface+".Seeked", int64(state.Position/time.Microsecond)); err != nil {
			log.Printf("publish MPRIS seek: %v", err)
		}
	}
	s.state = state
	s.hasState = true
	return nil
}

// setProperty turns prop.Properties.SetMust's documented error panic into a
// normal update failure. Runtime and non-error panics still signal
// programming bugs.
func (s *linuxSession) setProperty(property string, value any) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			failure, ok := recovered.(error)
			var runtimeError runtime.Error
			if !ok || errors.As(failure, &runtimeError) {
				panic(recovered)
			}
			err = fmt.Errorf("set MPRIS property %s: %w", property, failure)
		}
	}()
	s.props.SetMust(playerInterface, property, value)
	return nil
}

func (s *linuxSession) fail(err error) error {
	s.err = err
	return err
}

func (s *linuxSession) publishVolume() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.volumeTimer = nil
	if !s.closed && s.state.Volume != s.props.GetMust(playerInterface, "Volume") {
		if err := s.setProperty("Volume", s.state.Volume); err != nil {
			// No caller is waiting on a timer; the next Update reports it.
			s.err = err
		}
	}
}

// mprisProperties delegates scalar properties to prop.Properties but returns
// freshly constructed metadata, so optional fields from an earlier track do
// not survive a later Get or GetAll request.
type mprisProperties struct {
	*prop.Properties
	session *linuxSession
}

func (p *mprisProperties) Get(iface, property string) (dbus.Variant, *dbus.Error) {
	if iface == playerInterface && property == "Metadata" {
		p.session.mu.Lock()
		defer p.session.mu.Unlock()
		return dbus.MakeVariant(trackMetadata(p.session.state)), nil
	}
	return p.Properties.Get(iface, property)
}

func (p *mprisProperties) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	values, err := p.Properties.GetAll(iface)
	if err == nil && iface == playerInterface {
		p.session.mu.Lock()
		values["Metadata"] = dbus.MakeVariant(trackMetadata(p.session.state))
		p.session.mu.Unlock()
	}
	return values, err
}

func trackMetadata(state State) map[string]dbus.Variant {
	metadata := map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(trackPath(state)),
	}
	if state.Title != "" {
		metadata["xesam:title"] = dbus.MakeVariant(state.Title)
	}
	if state.Artist != "" {
		metadata["xesam:artist"] = dbus.MakeVariant([]string{state.Artist})
	}
	if state.Duration > 0 {
		metadata["mpris:length"] = dbus.MakeVariant(int64(state.Duration / time.Microsecond))
	}
	if state.ArtworkURL != "" {
		metadata["mpris:artUrl"] = dbus.MakeVariant(state.ArtworkURL)
	}
	return metadata
}

func trackPath(state State) dbus.ObjectPath {
	if state.VideoID == "" {
		return dbus.ObjectPath("/org/meiro/track/none")
	}
	return dbus.ObjectPath("/org/meiro/track/" + hex.EncodeToString([]byte(state.VideoID)))
}

func (s *linuxSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.volumeTimer != nil {
		s.volumeTimer.Stop()
	}
	if _, err := s.conn.ReleaseName(s.busName); err != nil {
		_ = s.conn.Close()
		return err
	}
	return s.conn.Close()
}

type mprisRoot struct{}

func (*mprisRoot) Raise() *dbus.Error { return nil }
func (*mprisRoot) Quit() *dbus.Error  { return nil }

type mprisPlayer struct {
	controls Controls
	trackID  func() dbus.ObjectPath
}

func (api *mprisPlayer) Next() *dbus.Error      { call(api.controls.Next); return nil }
func (api *mprisPlayer) Previous() *dbus.Error  { call(api.controls.Previous); return nil }
func (api *mprisPlayer) Pause() *dbus.Error     { call(api.controls.Pause); return nil }
func (api *mprisPlayer) PlayPause() *dbus.Error { call(api.controls.Toggle); return nil }
func (api *mprisPlayer) Stop() *dbus.Error      { call(api.controls.Stop); return nil }
func (api *mprisPlayer) Play() *dbus.Error      { call(api.controls.Play); return nil }
func (api *mprisPlayer) SeekBy(offset int64) *dbus.Error {
	if api.controls.Seek != nil {
		api.controls.Seek(time.Duration(offset) * time.Microsecond)
	}
	return nil
}

func (api *mprisPlayer) SetPosition(trackID dbus.ObjectPath, position int64) *dbus.Error {
	if trackID == api.trackID() && api.controls.Seek != nil {
		api.controls.Seek(time.Duration(position) * time.Microsecond)
	}
	return nil
}

func (*mprisPlayer) OpenUri(string) *dbus.Error {
	return dbus.NewError("org.freedesktop.DBus.Error.NotSupported", nil)
}

func call(fn func()) {
	if fn != nil {
		fn()
	}
}
