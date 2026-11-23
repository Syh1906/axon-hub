package httpclient

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPeerDictionaryRoundTripAndRecovery(t *testing.T) {
	receiver := new(PeerDictionaries)
	var mu sync.Mutex
	var wires []int64
	var encodings []string
	var delivered [][]byte
	var ordinaryConflict bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		wires = append(wires, r.ContentLength)
		encodings = append(encodings, r.Header.Get("Content-Encoding"))
		err := receiver.Receive(r, w.Header(), r.Header.Get("Authorization"), nil)
		if errors.Is(err, ErrPeerDictionaryMiss) {
			w.WriteHeader(409)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		body, err := ReadHTTPRequest(r)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if r.Header.Get(PeerOffer) != "" || r.Header.Get(PeerDigest) != "" {
			t.Error("peer headers leaked")
		}
		delivered = append(delivered, body.Body)
		if ordinaryConflict {
			w.WriteHeader(409)
		}
	}))
	t.Cleanup(server.Close)
	base := server.Client().Transport.(*http.Transport).Clone()
	hc := NewHttpClientWithClient(&http.Client{Transport: newRecoveringTransport(base)})
	defer hc.CloseIdleConnections()
	path := server.URL + "/v1/chat/completions"
	cache := new(PeerDictionaries)
	request := func(scope string, n int) ([]byte, int) {
		body := []byte(fmt.Sprintf(`{"history":"%s","turn":%d}`, strings.Repeat("long conversation ", 4000), n))
		r := &Request{URL: path, Method: "POST", Headers: http.Header{"Content-Type": {"application/json"}, "Authorization": {"synthetic"}}, Body: body, PeerDictionary: &PeerRequest{cache, []string{path}, scope}}
		r.Headers.Set("Expect", "100-continue")
		res, err := hc.Do(t.Context(), r)
		mu.Lock()
		mu.Unlock() // the handler has finished appending its observations
		if err != nil {
			var upstream *Error
			require.ErrorAs(t, err, &upstream)
			return body, upstream.StatusCode
		}
		require.Empty(t, res.Headers.Get(PeerAck))
		require.True(t, r.TransportTrace.UploadedBody())
		return body, res.StatusCode
	}
	body, code := request("one", 1)
	require.Equal(t, 200, code)
	require.Equal(t, body, delivered[0])
	body, code = request("one", 2)
	require.Equal(t, 200, code)
	require.Equal(t, body, delivered[1])
	require.Equal(t, []string{"", PeerEncoding}, encodings)
	require.Less(t, wires[1], wires[0]/10)
	_, _ = request("two", 3)
	require.Empty(t, encodings[2])
	// The receiving gateway restarted. It rejects the delta before inference.
	mu.Lock()
	receiver = new(PeerDictionaries)
	mu.Unlock()
	body, code = request("one", 4)
	require.Equal(t, 200, code)
	require.Equal(t, body, delivered[3])
	require.Equal(t, []string{PeerEncoding, ""}, encodings[3:])
	mu.Lock()
	ordinaryConflict = true
	mu.Unlock()
	_, code = request("one", 5)
	require.Equal(t, 409, code)
	require.Len(t, encodings, 6)
	mu.Lock()
	ordinaryConflict = false
	receiver = nil
	mu.Unlock()
	_, code = request("one", 6)
	require.Equal(t, 200, code)
	_, code = request("one", 7)
	require.Equal(t, 200, code)
	require.Empty(t, encodings[len(encodings)-1])
	require.Len(t, delivered, 7)
	t.Logf("body bytes, first full %d and next dictionary-coded %d", wires[0], wires[1])
}

func TestPeerDictionaryAuthScopeAndDigest(t *testing.T) {
	cache := new(PeerDictionaries)
	scope := strings.Repeat("a", 64)
	data := bytes.Repeat([]byte("x"), peerMin)
	req := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(data))
	req.Header.Set(PeerOffer, scope)
	h := make(http.Header)
	require.NoError(t, cache.Receive(req, h, "key-a", nil))
	base := h.Get(PeerAck)
	wire, err := encodePeerBody(data, data)
	require.NoError(t, err)
	for _, key := range []string{"key-b", "key-a"} {
		r := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(wire))
		r.Header.Set(PeerOffer, scope)
		r.Header.Set(PeerBase, base)
		r.Header.Set("Content-Encoding", PeerEncoding)
		r.Header.Set(PeerDigest, "wrong")
		err = cache.Receive(r, make(http.Header), key, nil)
		if key == "key-b" {
			require.ErrorIs(t, err, ErrPeerDictionaryMiss)
		} else {
			require.ErrorContains(t, err, "digest mismatch")
		}
	}
}

func TestDictionaryRetentionAndUnsupportedEndpoint(t *testing.T) {
	p := new(PeerDictionaries)
	body := bytes.Repeat([]byte("a"), peerSample+123)
	for i := range peerSlots + 1 {
		var k [32]byte
		k[0] = byte(i)
		p.remember(k, body, fmt.Sprint(i))
		if i == 0 {
			p.mu.Lock()
			e := p.entries[k]
			e.expires = e.expires.Add(-time.Minute)
			p.entries[k] = e
			p.mu.Unlock()
		}
	}
	require.Empty(t, p.get([32]byte{}).id)
	require.Len(t, p.entries, peerSlots)
	last := [32]byte{peerSlots}
	e := p.get(last)
	require.Len(t, e.data, peerSample)
	body[0] = 'z'
	require.Equal(t, byte('a'), e.data[0])
	p.mu.Lock()
	p.timer.Stop()
	for k, e := range p.entries {
		e.expires = time.Now().Add(-time.Second)
		p.entries[k] = e
	}
	p.mu.Unlock()
	p.expire()
	require.Empty(t, p.entries)
	require.Nil(t, p.timer)
	request := &Request{Body: body, PeerDictionary: &PeerRequest{p, []string{"https://peer.invalid/v1/responses"}, "one"}}
	raw := httptest.NewRequest("POST", "https://different.invalid/v1/responses", nil)
	raw.Header.Set("Content-Type", "application/json")
	require.Nil(t, request.PeerDictionary.bind(t.Context(), raw, request).Value(peerRequestKey{}))

	raw = httptest.NewRequest("POST", "https://peer.invalid/v1/responses", bytes.NewReader(body))
	raw.Header.Set("Content-Type", "application/json")
	peer := request.PeerDictionary.bind(t.Context(), raw, request).Value(peerRequestKey{}).(*peerUpload)
	var count int
	for range 2 {
		_, err := peer.roundTrip(raw, func(r *http.Request) (*http.Response, error) {
			count++
			require.Empty(t, r.Header.Get("Content-Encoding"))
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: http.NoBody}, nil
		})
		require.NoError(t, err)
	}
	require.Equal(t, 2, count)
	require.Empty(t, p.entries)
}
