//go:build darwin || (windows && (amd64 || arm64))

package systemmedia

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"time"
)

// loadArtwork bounds compressed bytes and image dimensions before the system
// decodes the image. DecodeConfig avoids decoding the full pixel payload here.
func loadArtwork(ctx context.Context, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artwork HTTP status %d", response.StatusCode)
	}

	const limit = 8 << 20
	if response.ContentLength > limit {
		return nil, fmt.Errorf("artwork exceeds %d bytes", limit)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("artwork exceeds %d bytes", limit)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode artwork config: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > 2048 || config.Height > 2048 {
		return nil, fmt.Errorf("artwork dimensions %dx%d exceed 2048 pixels", config.Width, config.Height)
	}
	return data, nil
}
