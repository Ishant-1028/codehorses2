package cf

import (
	"crypto/rand"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// signer holds Codeforces API credentials and can produce the `apiSig`
// signature required by authenticated endpoints. Unauthenticated Clients
// pass a zero signer and skip signing.
type signer struct {
	key    string
	secret string
}

func (s signer) enabled() bool { return s.key != "" && s.secret != "" }

// sign returns the query string with apiKey, time, and apiSig appended.
//
// The Codeforces scheme is:
//
//	apiSig = <6 random>/methodName?<sorted-params>#<secret>
//	         → sha512 hex, prefixed with the 6 random chars
//
// See https://codeforces.com/apiHelp
func (s signer) sign(method string, params url.Values) (url.Values, error) {
	if !s.enabled() {
		return params, nil
	}
	out := cloneValues(params)
	out.Set("apiKey", s.key)
	out.Set("time", fmt.Sprintf("%d", time.Now().Unix()))

	rnd, err := randHex(3)
	if err != nil {
		return nil, err
	}
	toHash := fmt.Sprintf("%s/%s?%s#%s", rnd, method, encodeSorted(out), s.secret)
	sum := sha512.Sum512([]byte(toHash))
	out.Set("apiSig", rnd+hex.EncodeToString(sum[:]))
	return out, nil
}

func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vs := range v {
		cp := make([]string, len(vs))
		copy(cp, vs)
		out[k] = cp
	}
	return out
}

// encodeSorted produces the canonical sorted `k=v&k=v` form CF expects.
func encodeSorted(v url.Values) string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		for j, val := range v[k] {
			if i > 0 || j > 0 {
				b.WriteByte('&')
			}
			b.WriteString(url.QueryEscape(k))
			b.WriteByte('=')
			b.WriteString(url.QueryEscape(val))
		}
	}
	return b.String()
}

func randHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
