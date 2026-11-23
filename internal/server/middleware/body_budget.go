package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm/httpclient"
)

// WithInferenceBody admits request bodies after authentication and before any
// trace hook inspects them.
func WithInferenceBody(payload *biz.RequestPayloadService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if payload == nil {
			c.Next()
			return
		}
		r := c.Request
		if r.Method == http.MethodGet && websocket.IsWebSocketUpgrade(r) {
			c.Request = r.WithContext(payload.BodyBudget.WithContext(r.Context()))
			c.Next()
			return
		}
		if r.Body == nil || r.Body == http.NoBody || r.Method == http.MethodGet {
			c.Next()
			return
		}
		encoding := r.Header.Get("Content-Encoding")
		peer := r.Header.Get(httpclient.PeerOffer) != "" || strings.EqualFold(encoding, httpclient.PeerEncoding)
		if !peer && payload.BodyBudget == nil {
			c.Next()
			return
		}
		if !peer && r.ContentLength >= 0 && r.ContentLength < httpclient.LargeBodyThreshold && encoding == "" {
			c.Next()
			return
		}
		lease, err := payload.BodyBudget.Admit(r.Context(), r.ContentLength)
		defer lease.Release()
		if err == nil && peer {
			err = payload.ReceiveDictionaries.Receive(r, c.Writer.Header(), biz.PeerDictionaryScope(r.Context()), lease)
		}
		if err == nil && lease != nil && (r.ContentLength < 0 || r.Header.Get("Content-Encoding") != "") {
			// The cached read error belongs to the protocol handler. Only local
			// admission failure needs a response here.
			if _, readErr := httpclient.InspectBudgetedBody(r, lease); errors.Is(readErr, httpclient.ErrBodyBudget) {
				err = readErr
			}
		}
		if err != nil {
			status := http.StatusBadRequest
			switch {
			case errors.Is(err, httpclient.ErrBodyBudget):
				status = http.StatusServiceUnavailable
			case errors.Is(err, httpclient.ErrPeerDictionaryMiss):
				status = http.StatusConflict
			case errors.Is(err, httpclient.ErrRequestBodyTooLarge):
				status = http.StatusRequestEntityTooLarge
			}
			AbortWithError(c, status, err)
			return
		}
		c.Next()
	}
}
