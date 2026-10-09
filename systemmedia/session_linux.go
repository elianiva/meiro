//go:build linux

package systemmedia

import (
	"encoding/hex"
	"errors"
	"fmt"
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
)

type linuxSession struct {
	conn     *dbus.Conn
	busName  string
	props    *prop.Properties
	mu       sync.Mutex
	state    State
	hasState bool
	lastPos  time.Time
	err      error
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
				Value: "None", Writable: true, Emit: prop.EmitTrue,
				Callback: func(change *prop.Change) *dbus.Error {
					status := change.Value.(string)
					if status != "None" && status != "Track" && status != "Playlist" {
						return dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", nil)
					}
					if controls.SetRepeat != nil {
						controls.SetRepeat(status)
					}
					return nil
				},
			},
			"Rate": {
				Value: 1.0, Writable: true, Emit: prop.EmitConst,
				Callback: func(change *prop.Change) *dbus.Error {
					if change.Value.(float64) != 1 {
						return dbus.NewError("org.freedesktop.DBus.Error.NotSupported", nil)
					}
					return nil
				},
			},
			"Shuffle": {
				Value: false, Writable: true, Emit: prop.EmitTrue,
				Callback: func(change *prop.Change) *dbus.Error {
					if controls.SetShuffle != nil {
						controls.SetShuffle(change.Value.(bool))
					}
					return nil
				},
			},
			"Metadata": {Value: trackMetadata(State{}), Emit: prop.EmitTrue},
			"Volume": {
				Value: 0.5, Writable: true, Emit: prop.EmitTrue,
				Callback: func(change *prop.Change) *dbus.Error {
					if controls.SetVolume != nil {
						controls.SetVolume(change.Value.(float64))
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
	if state.LoopStatus == "" {
		state.LoopStatus = "None"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}

	statusChanged := !s.hasState || state.Status != s.state.Status
	metadataChanged := !s.hasState || state.VideoID != s.state.VideoID || state.Title != s.state.Title || state.Artist != s.state.Artist || state.ArtworkURL != s.state.ArtworkURL || state.Duration != s.state.Duration
	if statusChanged {
		if err := s.setProperty("PlaybackStatus", string(state.Status)); err != nil {
			return s.fail(err)
		}
	}
	if !s.hasState || state.LoopStatus != s.state.LoopStatus {
		if err := s.setProperty("LoopStatus", state.LoopStatus); err != nil {
			return s.fail(err)
		}
	}
	if metadataChanged {
		if err := s.setProperty("Metadata", trackMetadata(state)); err != nil {
			return s.fail(err)
		}
	}
	if !s.hasState || state.Volume != s.state.Volume {
		if err := s.setProperty("Volume", state.Volume); err != nil {
			return s.fail(err)
		}
	}
	if !s.hasState || state.Shuffle != s.state.Shuffle {
		if err := s.setProperty("Shuffle", state.Shuffle); err != nil {
			return s.fail(err)
		}
	}
	for property, value := range map[string]bool{
		"CanGoNext":     state.CanNext,
		"CanGoPrevious": state.CanPrevious,
		"CanPlay":       state.CanPlay,
		"CanPause":      state.CanPause,
		"CanSeek":       state.CanSeek,
	} {
		if !s.hasState || propertyValue(s.state, property) != value {
			if err := s.setProperty(property, value); err != nil {
				return s.fail(err)
			}
		}
	}
	positionChangedWhilePaused := state.Status != Playing && state.Position != s.state.Position
	if !s.hasState || statusChanged || positionChangedWhilePaused || durationDistance(state.Position, s.state.Position) > 3*time.Second || time.Since(s.lastPos) >= time.Second {
		if err := s.setProperty("Position", int64(state.Position/time.Microsecond)); err != nil {
			return s.fail(err)
		}
		s.lastPos = time.Now()
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
			if _, isRuntimeError := failure.(runtime.Error); !ok || isRuntimeError {
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

func propertyValue(state State, property string) bool {
	switch property {
	case "CanGoNext":
		return state.CanNext
	case "CanGoPrevious":
		return state.CanPrevious
	case "CanPlay":
		return state.CanPlay
	case "CanPause":
		return state.CanPause
	case "CanSeek":
		return state.CanSeek
	default:
		return false
	}
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

func durationDistance(a, b time.Duration) time.Duration {
	if a > b {
		return a - b
	}
	return b - a
}

func (s *linuxSession) Close() error {
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
