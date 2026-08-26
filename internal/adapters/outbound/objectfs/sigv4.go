package objectfs

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// SigV4 is the only authentication an S3-compatible store accepts, so it is
// implemented here rather than taken as a dependency: the whole of it is an
// HMAC chain over a canonical form of the request, and a bucket client that
// carries five verbs does not need an SDK to reach them.
//
// The scheme is fixed, the service is always "s3", and every request is signed
// with the payload hash rather than UNSIGNED-PAYLOAD, which a store is free to
// refuse over a plain connection.
const (
	scheme  = "AWS4-HMAC-SHA256"
	service = "s3"

	// stampFormat and dayFormat are the two spellings of the signing time: the
	// header carries the instant, the credential scope carries only the day.
	stampFormat = "20060102T150405Z"
	dayFormat   = "20060102"
)

// credentials are what a store checks a signature against.
type credentials struct {
	keyID  string
	secret string
	region string
}

// sign puts the headers a store needs onto req and adds the Authorization it
// checks them with. payload is the body being sent, hashed whole, so a store
// that verifies the hash agrees with what arrives.
//
// Every header already on the request is signed, which is what binds the
// metadata and the copy source to the signature rather than leaving them free
// for anything in the middle to rewrite.
func sign(req *http.Request, creds credentials, payload []byte, now time.Time) {
	stamp := now.UTC().Format(stampFormat)
	day := now.UTC().Format(dayFormat)
	digest := hexHash(payload)

	req.Header.Set("X-Amz-Date", stamp)
	req.Header.Set("X-Amz-Content-Sha256", digest)

	signed, canonicalHeaders := canonicalHeaders(req)
	canonical := strings.Join([]string{
		req.Method,
		escapePath(req.URL.Path),
		req.URL.RawQuery,
		canonicalHeaders,
		strings.Join(signed, ";"),
		digest,
	}, "\n")

	scope := strings.Join([]string{day, creds.region, service, "aws4_request"}, "/")
	toSign := strings.Join([]string{scheme, stamp, scope, hexHash([]byte(canonical))}, "\n")

	signature := hex.EncodeToString(hmacSHA256(signingKey(creds, day), toSign))
	req.Header.Set("Authorization", fmt.Sprintf(
		"%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		scheme, creds.keyID, scope, strings.Join(signed, ";"), signature,
	))
}

// canonicalHeaders is the signed header list and the block the signature covers.
// Host is included by name because it lives on the request rather than in its
// header map.
func canonicalHeaders(req *http.Request) ([]string, string) {
	values := map[string]string{"host": req.URL.Host}
	for name, header := range req.Header {
		values[strings.ToLower(name)] = strings.TrimSpace(strings.Join(header, ","))
	}

	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names)

	var block strings.Builder
	for _, name := range names {
		fmt.Fprintf(&block, "%s:%s\n", name, values[name])
	}
	return names, block.String()
}

// signingKey is the HMAC chain that ties a signature to one day, one region and
// one service, so a signature lifted from a request cannot be reused elsewhere.
func signingKey(creds credentials, day string) []byte {
	key := hmacSHA256([]byte("AWS4"+creds.secret), day)
	key = hmacSHA256(key, creds.region)
	key = hmacSHA256(key, service)
	return hmacSHA256(key, "aws4_request")
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

func hexHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// escapePath encodes a path the way the signature expects it, which is not what
// url.PathEscape does: separators stay separators, and everything outside the
// unreserved set becomes an uppercase percent escape.
func escapePath(path string) string {
	var escaped strings.Builder
	for _, b := range []byte(path) {
		if unreserved(b) || b == '/' {
			escaped.WriteByte(b)
			continue
		}
		fmt.Fprintf(&escaped, "%%%02X", b)
	}
	return escaped.String()
}

// escape encodes one query or header value, where a separator is data like
// anything else and is escaped with it.
func escape(value string) string {
	var escaped strings.Builder
	for _, b := range []byte(value) {
		if unreserved(b) {
			escaped.WriteByte(b)
			continue
		}
		fmt.Fprintf(&escaped, "%%%02X", b)
	}
	return escaped.String()
}

// unreserved is the set RFC 3986 leaves alone, which is the set the signature
// leaves alone.
func unreserved(b byte) bool {
	switch {
	case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z', b >= '0' && b <= '9':
		return true
	case b == '-', b == '_', b == '.', b == '~':
		return true
	default:
		return false
	}
}
