package streamfs_test

import (
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/outbound/streamfs"
	"github.com/tunedev/nerve/internal/core/stability"
	"github.com/tunedev/nerve/internal/core/vfs"
)

func TestFactoryHonoursTheCapacityAMountNames(t *testing.T) {
	fsys, err := streamfs.Factory(map[string]string{"capacity": "4"})
	if err != nil {
		t.Fatalf("Factory(capacity=4) = %v", err)
	}

	if err := fsys.Write(t.Context(), "/log", []byte("abcdef")); err != nil {
		t.Fatalf("Write(/log) = %v", err)
	}
	content, err := fsys.Read(t.Context(), "/log")
	if err != nil {
		t.Fatalf("Read(/log) = %v", err)
	}
	if string(content) != "cdef" {
		t.Errorf("Read(/log) = %q, want %q", content, "cdef")
	}
}

// A ring that keeps nothing drops every byte as it is written, so a mount that
// asks for one is refused at boot rather than handed a stream nothing can be
// read from.
func TestFactoryRefusesACapacityThatKeepsNothing(t *testing.T) {
	for _, spelled := range []string{"0", "-1", "plenty"} {
		_, err := streamfs.Factory(map[string]string{"capacity": spelled})

		if !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Factory(capacity=%s) = %v, want fs.ErrInvalid", spelled, err)
		}
	}
}

func TestFactoryHonoursTheFollowAMountNames(t *testing.T) {
	fsys, err := streamfs.Factory(map[string]string{"follow": "20ms"})
	if err != nil {
		t.Fatalf("Factory(follow=20ms) = %v", err)
	}
	if err := fsys.Write(t.Context(), "/log", []byte("written")); err != nil {
		t.Fatalf("Write(/log) = %v", err)
	}

	reader, err := fsys.AsStreamer().OpenRead(t.Context(), "/log")
	if err != nil {
		t.Fatalf("OpenRead(/log) = %v", err)
	}
	defer reader.Close()

	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(/log) = %v", err)
	}
	if string(content) != "written" {
		t.Errorf("ReadAll(/log) = %q, want %q", content, "written")
	}
}

func TestFactoryRefusesAFollowItCannotHonour(t *testing.T) {
	for _, spelled := range []string{"-1s", "a while"} {
		_, err := streamfs.Factory(map[string]string{"follow": spelled})

		if !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Factory(follow=%s) = %v, want fs.ErrInvalid", spelled, err)
		}
	}
}

// A setting this kind cannot honour is a misspelling of one meant for another,
// and boot is the last place an operator can still act on it.
func TestFactoryRefusesSettingsItCannotHonour(t *testing.T) {
	_, err := streamfs.Factory(map[string]string{"root": "/tmp/nerve"})

	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Factory(root) = %v, want fs.ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "root") {
		t.Errorf("Factory(root) = %v, want the setting named", err)
	}
}

// The registry is the only route into a mount, and the streaming verbs are
// reached through the dispatcher rather than by asserting on the plugin.
func TestStreamfsMountsFromAMountSpec(t *testing.T) {
	registry := vfs.NewRegistry(time.Second, stability.Retry{Attempts: 1}, stability.Breaker{Threshold: 1, Cooldown: time.Second})
	if err := registry.Register(streamfs.Kind, streamfs.Factory, streamfs.Public...); err != nil {
		t.Fatalf("Register(streamfs) = %v", err)
	}

	table, err := registry.Table([]vfs.MountSpec{{Prefix: "/streams", Kind: streamfs.Kind, Settings: map[string]string{"follow": "0s"}}})
	if err != nil {
		t.Fatalf("Table(/streams) = %v", err)
	}

	if err := table.Write(t.Context(), "/streams/log", []byte("first ")); err != nil {
		t.Fatalf("Write(/streams/log) = %v", err)
	}
	stream := table.AsStreamer()
	if stream == nil {
		t.Fatal("AsStreamer() through the table = nil, want the dispatcher")
	}

	writer, err := stream.OpenWrite(t.Context(), "/streams/log")
	if err != nil {
		t.Fatalf("OpenWrite(/streams/log) = %v", err)
	}
	if _, err := io.WriteString(writer, "second"); err != nil {
		t.Fatalf("Write to the stream = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close(/streams/log) = %v", err)
	}

	content, err := table.Read(t.Context(), "/streams/log")
	if err != nil {
		t.Fatalf("Read(/streams/log) = %v", err)
	}
	if string(content) != "first second" {
		t.Errorf("Read(/streams/log) = %q, want %q", content, "first second")
	}
}
