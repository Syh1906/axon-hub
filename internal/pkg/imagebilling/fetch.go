package imagebilling

import (
	"context"
	"fmt"
	"github.com/looplj/axonhub/internal/pkg/publicnet"
	"image"
	"io"
	"net/http"
	"time"
)

// A dedicated unauthenticated transport checks the actual dial address, including
// redirect targets, so provider URLs cannot access local services or credentials.
var imageClient = &http.Client{
	Timeout: 3 * time.Second,
	Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext:       publicnet.DialContext,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return fmt.Errorf("too many image redirects")
		}
		if req.URL.User != nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
			return fmt.Errorf("unsupported image URL")
		}
		return nil
	},
}

func Fetch(ctx context.Context, url string) (image.Config, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return image.Config{}, err
	}
	if req.URL.User != nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
		return image.Config{}, fmt.Errorf("unsupported image URL")
	}
	response, err := imageClient.Do(req)
	if err != nil {
		return image.Config{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		return image.Config{}, fmt.Errorf("image HTTP status %d", response.StatusCode)
	}
	config, _, err := image.DecodeConfig(io.LimitReader(response.Body, 1<<20))
	return config, err
}
