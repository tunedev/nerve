package devfs_test

import (
	"bytes"
	"errors"
	"io/fs"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/outbound/devfs"
	"github.com/tunedev/nerve/internal/core/vfs"
)

func TestStatDescribesEveryDevice(t *testing.T) {
	fsys := devfs.New(16)

	sizes := map[string]int64{"/null": 0, "/zero": 16, "/random": 16}
	for name, want := range sizes {
		info, err := fsys.Stat(t.Context(), name)
		if err != nil {
			t.Fatalf("Stat(%q) = %v", name, err)
		}
		if info.Size != want {
			t.Errorf("Stat(%q).Size = %d, want %d", name, info.Size, want)
		}
	}
}

func TestZeroHandsBackZeroBytes(t *testing.T) {
	content, err := devfs.New(16).Read(t.Context(), "/zero")
	if err != nil {
		t.Fatalf("Read(/zero) = %v", err)
	}
	if !bytes.Equal(content, make([]byte, 16)) {
		t.Errorf("Read(/zero) = %v, want sixteen zero bytes", content)
	}
}

func TestRandomHandsBackSomethingElseEachTime(t *testing.T) {
	fsys := devfs.New(32)

	first, err := fsys.Read(t.Context(), "/random")
	if err != nil {
		t.Fatalf("Read(/random) = %v", err)
	}
	second, err := fsys.Read(t.Context(), "/random")
	if err != nil {
		t.Fatalf("the second Read(/random) = %v", err)
	}
	if bytes.Equal(first, second) {
		t.Error("Read(/random) handed back the same 32 bytes twice, want unpredictable")
	}
	if len(first) != 32 {
		t.Errorf("Read(/random) = %d bytes, want 32", len(first))
	}
}

// The sink takes a write, keeps nothing, and reads back as nothing.
func TestNullSwallowsWhatIsWrittenToIt(t *testing.T) {
	fsys := devfs.New(16)

	if err := fsys.Write(t.Context(), "/null", []byte("gone")); err != nil {
		t.Fatalf("Write(/null) = %v", err)
	}

	content, err := fsys.Read(t.Context(), "/null")
	if err != nil {
		t.Fatalf("Read(/null) = %v", err)
	}
	if len(content) != 0 {
		t.Errorf("Read(/null) after a write = %q, want nothing", content)
	}
}

// Capability varies inside this one mount, which is the whole reason Caps is
// resolved per path.
func TestOnlyTheSinkIsWritable(t *testing.T) {
	fsys := devfs.New(16)

	if caps := fsys.Capabilities("/null"); caps.ReadOnly {
		t.Error("Capabilities(/null).ReadOnly = true, want the sink to take writes")
	}
	for _, name := range []string{"/", "/zero", "/random", "/absent"} {
		if caps := fsys.Capabilities(name); !caps.ReadOnly {
			t.Errorf("Capabilities(%q) = %+v, want read-only", name, caps)
		}
	}
}

func TestASourceRefusesAWrite(t *testing.T) {
	fsys := devfs.New(16)

	for _, name := range []string{"/zero", "/random", "/absent"} {
		if err := fsys.Write(t.Context(), name, []byte("x")); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("Write(%q) = %v, want fs.ErrPermission", name, err)
		}
	}
}

// The sink is one name at one level, not any path that happens to end in it.
func TestADeeperPathIsNotTheSink(t *testing.T) {
	fsys := devfs.New(16)

	if err := fsys.Write(t.Context(), "/elsewhere/null", []byte("x")); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Write(/elsewhere/null) = %v, want fs.ErrPermission", err)
	}
	if caps := fsys.Capabilities("/elsewhere/null"); !caps.ReadOnly {
		t.Errorf("Capabilities(/elsewhere/null) = %+v, want read-only", caps)
	}
}

// The mount is reached through the dispatcher, so the override has to survive
// being held as the port rather than as the concrete type.
func TestTheSinkStillSwallowsThroughThePort(t *testing.T) {
	var fsys vfs.FileSystem = devfs.New(16)

	if err := fsys.Write(t.Context(), "/null", []byte("gone")); err != nil {
		t.Errorf("Write(/null) through the port = %v, want it swallowed", err)
	}
}

func TestFactoryRefusesASizeThatIsNotASource(t *testing.T) {
	for _, spelled := range []string{"0", "-1", "plenty"} {
		if _, err := devfs.Factory(map[string]string{"size": spelled}); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Factory(size=%q) = %v, want fs.ErrInvalid", spelled, err)
		}
	}
}

func TestFactoryRefusesASettingItCannotHonour(t *testing.T) {
	if _, err := devfs.Factory(map[string]string{"root": "/dev"}); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Factory(root) = %v, want fs.ErrInvalid", err)
	}
}
