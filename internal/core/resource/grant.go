package resource

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/tunedev/nerve/internal/core/vfs"
	"github.com/tunedev/nerve/internal/core/workspace"
)

// Grant is permission to download one resource until one moment, and the token
// that carries it.
//
// The token is the whole of what a holder presents. It names the scope, so
// redeeming it needs nothing else alongside — which is the point of handing one
// out, and the reason the signature has to cover every field below.
type Grant struct {
	Scope   workspace.Scope
	Name    string
	Expires time.Time
	Token   string
}

// Signer issues and redeems grants. The key is what separates a grant this
// process made from one somebody wrote, and the clock is what decides when a
// grant stops being one.
type Signer struct {
	key []byte
	now func() time.Time
}

// NewSigner returns a signer holding key and reading the time from now.
//
// A signer with no key is refused rather than allowed to become a Sign that
// hands out unforgeable-looking tokens anybody can make, which is the startup
// failure a missing secret is entitled to.
func NewSigner(key []byte, now func() time.Time) (*Signer, error) {
	if len(key) == 0 {
		return nil, fmt.Errorf("signer with no key: %w", fs.ErrInvalid)
	}
	if now == nil {
		return nil, fmt.Errorf("signer with no clock: %w", fs.ErrInvalid)
	}
	return &Signer{key: key, now: now}, nil
}

// Sign returns the grant letting its holder download name in s for ttl.
//
// A ttl that is not positive is refused: a grant expiring at or before the
// moment it was made is a token that cannot be redeemed, and handing one out is
// a failure the caller should see here rather than at the download.
func (s *Signer) Sign(scope workspace.Scope, name string, ttl time.Duration) (Grant, error) {
	if err := vfs.ValidSegment("resource name", name); err != nil {
		return Grant{}, fmt.Errorf("sign: %w", err)
	}
	if ttl <= 0 {
		return Grant{}, fmt.Errorf("sign %q for %s: a grant must outlive its signing: %w", name, ttl, fs.ErrInvalid)
	}

	expires := s.now().Add(ttl).UTC()
	payload, err := json.Marshal(claims{
		User:      scope.User,
		Workspace: scope.Workspace,
		Name:      name,
		Expires:   expires.Unix(),
	})
	if err != nil {
		return Grant{}, fmt.Errorf("sign %q: %w", name, err)
	}

	encoded := encoding.EncodeToString(payload)
	token := encoded + "." + encoding.EncodeToString(s.mac(encoded))
	return Grant{Scope: scope, Name: name, Expires: expires, Token: token}, nil
}

// Verify returns what token grants, or an error wrapping fs.ErrPermission if it
// grants nothing: a signature this signer did not make, or a moment that has
// passed. A token that is not a token at all is fs.ErrInvalid — the caller
// spelled something wrong rather than presented something it should not have.
func (s *Signer) Verify(token string) (Grant, error) {
	encoded, signature, found := strings.Cut(token, ".")
	if !found {
		return Grant{}, fmt.Errorf("verify token: want %q: %w", "<payload>.<signature>", fs.ErrInvalid)
	}
	payload, err := encoding.DecodeString(encoded)
	if err != nil {
		return Grant{}, fmt.Errorf("verify token: payload is not base64: %w", fs.ErrInvalid)
	}
	presented, err := encoding.DecodeString(signature)
	if err != nil {
		return Grant{}, fmt.Errorf("verify token: signature is not base64: %w", fs.ErrInvalid)
	}

	// Compared in constant time, so that a token guessed one byte at a time
	// cannot be told it is getting warmer.
	if !hmac.Equal(presented, s.mac(encoded)) {
		return Grant{}, fmt.Errorf("verify token: this signer did not sign it: %w", fs.ErrPermission)
	}

	var held claims
	if err := json.Unmarshal(payload, &held); err != nil {
		return Grant{}, fmt.Errorf("verify token: %w", fs.ErrInvalid)
	}
	expires := time.Unix(held.Expires, 0).UTC()
	if !s.now().Before(expires) {
		return Grant{}, fmt.Errorf("verify token for %q: it expired at %s: %w", held.Name, expires, fs.ErrPermission)
	}

	return Grant{
		Scope:   workspace.Scope{User: held.User, Workspace: held.Workspace},
		Name:    held.Name,
		Expires: expires,
		Token:   token,
	}, nil
}

// mac is the signature over the encoded payload.
func (s *Signer) mac(encoded string) []byte {
	sum := hmac.New(sha256.New, s.key)
	sum.Write([]byte(encoded))
	return sum.Sum(nil)
}

// claims is what a token says, and every field of it is signed. It is JSON so
// that no field's content can be read as the separator between two of them.
type claims struct {
	User      string `json:"user"`
	Workspace string `json:"workspace"`
	Name      string `json:"name"`
	Expires   int64  `json:"expires"`
}

// encoding is URL-safe and unpadded, so a token stands unescaped in a path or a
// query string.
var encoding = base64.RawURLEncoding
