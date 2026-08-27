package streamrotatefs

import (
	"fmt"
	"time"
)

// append adds data to the end of the log at name and seals it if that left it
// at or over the mount's segment size. The caller holds the lock.
//
// The whole write lands before anything is sealed, so a segment is cut between
// writes and never inside one. A log line that arrived in one call is read back
// from one segment, which is what makes a rotated log worth reading; a write
// larger than the segment size is a segment larger than the size rather than a
// record in two pieces.
func (f *FS) append(at string, data []byte) {
	live := f.open(at)
	live.content = append(live.content, data...)
	live.modTime = time.Now()

	if len(live.content) >= f.size {
		f.rotate(at, live)
	}
}

// rotate seals what the live segment holds under a numbered sibling, empties
// the live segment, and drops the sealed segments past the mount's keep.
//
// The sealed segment is an ordinary entry of the mount from here on. Nothing
// marks it as history except this log's own record of having sealed it, which
// is what retention reads.
func (f *FS) rotate(at string, live *stream) {
	sealed := &stream{content: live.content, mode: live.mode, modTime: live.modTime}
	name := f.nextSegment(at, live)
	f.logs[name] = sealed

	live.content = nil
	live.sealed = append(live.sealed, name)

	for len(live.sealed) > f.keep {
		delete(f.logs, live.sealed[0])
		live.sealed = live.sealed[1:]
	}
}

// nextSegment is the name this log's next segment is sealed under: its own name
// and the number of the rotation. The number passes over anything already in
// the mount, because a caller may have written to a name that looks like a
// segment and an append-only mount replaces nothing.
func (f *FS) nextSegment(at string, live *stream) string {
	for {
		live.seq++
		name := fmt.Sprintf("%s.%d", at, live.seq)
		if _, taken := f.logs[name]; !taken {
			return name
		}
	}
}
