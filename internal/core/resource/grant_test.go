package resource_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/core/resource"
	"github.com/tunedev/nerve/internal/core/workspace"
)

var (
	here  = workspace.Scope{User: "ada", Workspace: "hunt"}
	other = workspace.Scope{User: "grace", Workspace: "hunt"}
)

// clock is a time a test moves by hand, so that expiry is asserted rather than
// waited for.
type clock struct{ at time.Time }

func (c *clock) now() time.Time { return c.at }

func newSigner(t *testing.T, key string) (*resource.Signer, *clock) {
	t.Helper()

	tick := &clock{at: time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)}
	signer, err := resource.NewSigner([]byte(key), tick.now)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return signer, tick
}

// A token is the whole of what a holder needs: it says which scope's resource
// it is for, so nothing else has to be presented alongside it.
func TestTokenVerifiesBackToWhatWasSigned(t *testing.T) {
	signer, tick := newSigner(t, "a key nobody else has")

	granted, err := signer.Sign(here, "transcript.txt", time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	got, err := signer.Verify(granted.Token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Scope != here || got.Name != "transcript.txt" {
		t.Errorf("Verify = %+v %q, want %+v %q", got.Scope, got.Name, here, "transcript.txt")
	}
	if want := tick.at.Add(time.Hour); !got.Expires.Equal(want) {
		t.Errorf("Expires = %s, want %s", got.Expires, want)
	}
}

// The signature is over the payload, so editing the payload to name another
// user's resource is what it refuses.
func TestTokenNamingAnotherScopeIsRefused(t *testing.T) {
	signer, _ := newSigner(t, "a key nobody else has")

	mine, err := signer.Sign(here, "transcript.txt", time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	theirs, err := signer.Sign(other, "transcript.txt", time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	payload, signature, _ := strings.Cut(theirs.Token, ".")
	_, mySignature, _ := strings.Cut(mine.Token, ".")
	if signature == mySignature {
		t.Fatal("two scopes signed to the same signature")
	}

	if _, err := signer.Verify(payload + "." + mySignature); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Verify of a re-signed payload = %v, want %v", err, fs.ErrPermission)
	}
}

func TestTokenSignedWithAnotherKeyIsRefused(t *testing.T) {
	signer, _ := newSigner(t, "a key nobody else has")
	forger, _ := newSigner(t, "a key somebody else guessed")

	forged, err := forger.Sign(here, "transcript.txt", time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if _, err := signer.Verify(forged.Token); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Verify of another key's token = %v, want %v", err, fs.ErrPermission)
	}
}

// Expiry is what makes handing the token on safe. It is checked at redemption
// and not at signing, because the moment that matters is the later one.
func TestTokenIsRefusedOnceItExpires(t *testing.T) {
	signer, tick := newSigner(t, "a key nobody else has")

	granted, err := signer.Sign(here, "transcript.txt", time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := signer.Verify(granted.Token); err != nil {
		t.Fatalf("Verify before expiry: %v", err)
	}

	tick.at = tick.at.Add(time.Minute + time.Second)

	if _, err := signer.Verify(granted.Token); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Verify after expiry = %v, want %v", err, fs.ErrPermission)
	}
}

func TestMalformedTokenIsRefusedAsInvalid(t *testing.T) {
	signer, _ := newSigner(t, "a key nobody else has")

	for _, token := range []string{"", "no-full-stop", "not base64.也不是"} {
		if _, err := signer.Verify(token); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Verify(%q) = %v, want %v", token, err, fs.ErrInvalid)
		}
	}
}

// A signer with no key signs nothing, and a grant that has already expired
// grants nothing. Both are refused where they are said rather than where they
// would first be noticed.
func TestASignerWithNoKeyIsRefused(t *testing.T) {
	if _, err := resource.NewSigner(nil, time.Now); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("NewSigner with no key = %v, want %v", err, fs.ErrInvalid)
	}
}

func TestGrantWithNoLifeIsRefused(t *testing.T) {
	signer, _ := newSigner(t, "a key nobody else has")

	if _, err := signer.Sign(here, "transcript.txt", 0); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Sign with no ttl = %v, want %v", err, fs.ErrInvalid)
	}
}
