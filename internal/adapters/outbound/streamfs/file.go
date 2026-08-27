package streamfs

import (
	"context"
	"fmt"
)

// Create makes name exist as an empty stream. A stream already there is left as
// it is rather than truncated: the mount is append-only, and what has been
// written to a stream is never shortened.
func (f *FS) Create(_ context.Context, name string) error {
	if _, err := f.open(name); err != nil {
		return fmt.Errorf("create %w", err)
	}
	return nil
}

// Read hands back the window of name: the most recent bytes the stream holds,
// which is less than everything written to it once the ring has wrapped.
//
// It is a copy, and it is one moment of a stream that is still moving. A caller
// that wants what arrives next follows the stream through OpenRead rather than
// reading it again.
func (f *FS) Read(_ context.Context, name string) ([]byte, error) {
	stream, err := f.find(name)
	if err != nil {
		return nil, fmt.Errorf("read %w", err)
	}
	return stream.read(), nil
}

// Write appends data to the end of name, making the stream if it is not there.
func (f *FS) Write(_ context.Context, name string, data []byte) error {
	stream, err := f.open(name)
	if err != nil {
		return fmt.Errorf("write %w", err)
	}
	stream.append(data)
	return nil
}
