package datatransfer

import (
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
)

func TestSharedDirectoryInitializesOneCompleteIdentity(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	identities := make(chan string, 12)
	errors := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("directory initialization panic: %v", r)
				}
			}()
			s := &Service{config: Config{WorkDir: dir, MaxFileGiB: 1, TimeoutSeconds: 60, RetentionHours: 24}}
			err := s.initializeFiles()
			errors <- err
			identities <- s.instance
		}()
	}
	wg.Wait()
	close(errors)
	close(identities)
	for err := range errors {
		require.NoError(t, err)
	}
	var first string
	for identity := range identities {
		require.NotEmpty(t, identity)
		if first == "" {
			first = identity
		}
		require.Equal(t, first, identity)
	}
}
