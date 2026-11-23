package appbrowser

import (
	"context"
	"testing"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// fitSession implements only what Drive fit and Emulate touch: a tracked
// device the UpdateDevice callback edits, the lease, and the Page and
// Screenshot an Emulate result reports.
type fitSession struct {
	dservice.BrowserSession
	lease   dservice.BrowserLease
	device  dservice.BrowserDevice
	applied []dservice.BrowserDevice
}

func (s *fitSession) Lease() dservice.BrowserLease   { return s.lease }
func (s *fitSession) Device() dservice.BrowserDevice { return s.device }
func (s *fitSession) Page() dservice.BrowserPage     { return dservice.BrowserPage{} }
func (s *fitSession) Screenshot(context.Context) ([]byte, error) {
	return []byte{1}, nil
}

func (s *fitSession) Watch(context.Context) <-chan dservice.BrowserFrame {
	return make(chan dservice.BrowserFrame)
}

func (s *fitSession) UpdateDevice(_ context.Context, change func(dservice.BrowserDevice) (dservice.BrowserDevice, error)) error {
	d, err := change(s.device)
	if err != nil {
		return err
	}
	if d != s.device {
		s.device = d
		s.applied = append(s.applied, d)
	}
	return nil
}

type fitBrowser struct {
	dservice.Browser
	session *fitSession
}

func (b *fitBrowser) Session(string) (dservice.BrowserSession, bool) { return b.session, true }
func (b *fitBrowser) Open(context.Context, string) (dservice.BrowserSession, error) {
	return b.session, nil
}

func TestDriveFit(t *testing.T) {
	ctx := context.Background()
	responsive := dservice.BrowserDevice{Preset: "responsive", Width: 1280, Height: 800, DPR: 1}
	newApp := func(device dservice.BrowserDevice, lease dservice.BrowserLease) (*App, *fitSession) {
		s := &fitSession{lease: lease, device: device}
		return &App{browser: &fitBrowser{session: s}}, s
	}

	t.Run("a passive viewer fits a responsive tab, and so does its driver", func(t *testing.T) {
		for name, lease := range map[string]dservice.BrowserLease{
			"nobody took over":   {},
			"this viewer drives": {Controller: "v1"},
		} {
			app, s := newApp(responsive, lease)
			if _, err := app.drive(ctx, "c_1", s, "v1", Input{Type: "fit", Width: 900, Height: 640}); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if s.device.Preset != "responsive" || s.device.Width != 900 || s.device.Height != 640 {
				t.Fatalf("%s: device = %+v, want responsive 900x640", name, s.device)
			}
		}
	})

	t.Run("another viewer's driver owns the size", func(t *testing.T) {
		app, s := newApp(responsive, dservice.BrowserLease{Controller: "v2"})
		if _, err := app.drive(ctx, "c_1", s, "v1", Input{Type: "fit", Width: 900, Height: 640}); err != nil {
			t.Fatal(err)
		}
		if s.device.Width != 1280 || len(s.applied) != 0 {
			t.Fatalf("device = %+v, applied = %v; a stale pane must not resize", s.device, s.applied)
		}
	})

	t.Run("a fixed preset ignores a stale fit", func(t *testing.T) {
		for _, preset := range []string{"desktop", "mobile", "custom"} {
			d := dservice.BrowserDevice{Preset: preset, Width: 1280, Height: 800, DPR: 1}
			app, s := newApp(d, dservice.BrowserLease{})
			if _, err := app.drive(ctx, "c_1", s, "v1", Input{Type: "fit", Width: 900, Height: 640}); err != nil {
				t.Fatalf("%s: %v", preset, err)
			}
			if s.device != d {
				t.Fatalf("%s: device = %+v, want unchanged %+v", preset, s.device, d)
			}
		}
	})

	t.Run("sides outside the range are refused", func(t *testing.T) {
		for _, in := range []Input{
			{Type: "fit", Width: 0, Height: 600},
			{Type: "fit", Width: 199, Height: 600},
			{Type: "fit", Width: 600, Height: 4097},
			{Type: "fit"},
		} {
			app, s := newApp(responsive, dservice.BrowserLease{})
			_, err := app.drive(ctx, "c_1", s, "v1", in)
			if got := apperror.As(err).StatusCode(); got != 400 || len(s.applied) != 0 {
				t.Errorf("%+v: status = %d, applied = %v", in, got, s.applied)
			}
		}
	})
}

func TestEmulatePresets(t *testing.T) {
	ctx := context.Background()
	newApp := func(device dservice.BrowserDevice) (*App, *fitSession) {
		s := &fitSession{device: device}
		return &App{browser: &fitBrowser{session: s}}, s
	}
	t.Run("mobile then responsive keeps the size and drops the device traits", func(t *testing.T) {
		app, s := newApp(dservice.BrowserDevice{Preset: "desktop", Width: 1280, Height: 800, DPR: 1})
		if _, err := app.Emulate(ctx, "c_1", DevicePatch{Preset: "mobile"}); err != nil {
			t.Fatal(err)
		}
		if s.device.Preset != "mobile" || !s.device.Mobile || !s.device.Touch || s.device.UserAgent == "" {
			t.Fatalf("after mobile: %+v", s.device)
		}
		if _, err := app.Emulate(ctx, "c_1", DevicePatch{Preset: "responsive"}); err != nil {
			t.Fatal(err)
		}
		want := dservice.BrowserDevice{Preset: "responsive", Width: 390, Height: 844, DPR: 1}
		if s.device != want {
			t.Fatalf("after responsive: %+v, want %+v", s.device, want)
		}
	})

	t.Run("responsive seeds its first viewport from width and height only", func(t *testing.T) {
		app, s := newApp(dservice.BrowserDevice{Preset: "mobile", Width: 390, Height: 844, DPR: 3, Mobile: true, Touch: true})
		w, h := 1024, 700
		if _, err := app.Emulate(ctx, "c_1", DevicePatch{Preset: "responsive", Width: &w, Height: &h}); err != nil {
			t.Fatal(err)
		}
		want := dservice.BrowserDevice{Preset: "responsive", Width: 1024, Height: 700, DPR: 1}
		if s.device != want {
			t.Fatalf("device = %+v, want %+v", s.device, want)
		}
		mobile := true
		if _, err := app.Emulate(ctx, "c_1", DevicePatch{Preset: "responsive", Mobile: &mobile}); apperror.As(err).StatusCode() != 400 {
			t.Fatalf("responsive with device traits: err = %v", err)
		}
	})

	t.Run("a resize and a partial patch are custom", func(t *testing.T) {
		app, s := newApp(dservice.BrowserDevice{Preset: "desktop", Width: 1280, Height: 800, DPR: 1})
		if _, err := app.Resize(ctx, "c_1", 1000, 700); err != nil {
			t.Fatal(err)
		}
		if s.device.Preset != "custom" || s.device.Width != 1000 || s.device.Height != 700 {
			t.Fatalf("after resize: %+v", s.device)
		}
		w := 900
		if _, err := app.Emulate(ctx, "c_1", DevicePatch{Width: &w}); err != nil {
			t.Fatal(err)
		}
		if s.device.Preset != "custom" || s.device.Width != 900 {
			t.Fatalf("after partial patch: %+v", s.device)
		}
	})

	t.Run("an override off a preset marks the result custom", func(t *testing.T) {
		app, s := newApp(dservice.BrowserDevice{Preset: "desktop", Width: 1280, Height: 800, DPR: 1})
		h := 600
		if _, err := app.Emulate(ctx, "c_1", DevicePatch{Preset: "desktop", Height: &h}); err != nil {
			t.Fatal(err)
		}
		if s.device.Preset != "custom" || s.device.Height != 600 {
			t.Fatalf("device = %+v, want custom at 1280x600", s.device)
		}
	})
}

// The drive Watch returns acts on the session the frames come from: a fit
// queued on the old watch must not touch the session that replaced it.
func TestWatchDriveStaysOnTheWatchedSession(t *testing.T) {
	ctx := context.Background()
	old := &fitSession{device: dservice.BrowserDevice{Preset: "responsive", Width: 1280, Height: 800, DPR: 1}}
	b := &fitBrowser{session: old}
	app := &App{browser: b}
	_, drive, err := app.Watch(ctx, "c_1")
	if err != nil {
		t.Fatal(err)
	}
	replacement := &fitSession{device: dservice.BrowserDevice{Preset: "responsive", Width: 1024, Height: 768, DPR: 1}}
	b.session = replacement
	if _, err := drive(ctx, "v1", Input{Type: "fit", Width: 900, Height: 640}); err != nil {
		t.Fatal(err)
	}
	if replacement.device.Width != 1024 {
		t.Fatalf("replacement device = %+v, want untouched", replacement.device)
	}
	if old.device.Width != 900 || old.device.Preset != "responsive" {
		t.Fatalf("watched device = %+v, want the fit", old.device)
	}
}
