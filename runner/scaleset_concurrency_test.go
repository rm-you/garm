//go:build testing

package runner

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	storeMocks "github.com/cloudbase/garm/database/common/mocks"
)

func TestCreateEntityScaleSetPreservesConcurrentAdoption(t *testing.T) {
	api := newScaleSetAPI(t)
	creator, ctx := newScaleSetRunner(t, api, errors.New("local insert failed after competing adoption"), true)
	adopter, adopterCtx := newScaleSetRunner(t, api, nil, true)
	atInsert := make(chan struct{})
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	for _, call := range creator.store.(*storeMocks.Store).ExpectedCalls {
		if call.Method == "CreateEntityScaleSet" {
			call.RunFn = func(mock.Arguments) { close(atInsert); <-release }
		}
	}
	done := make(chan error, 1)
	go func() { _, err := createExistingScaleSet(t, creator, ctx); done <- err }()
	select {
	case <-atInsert:
	case <-time.After(10 * time.Second):
		t.Fatal("creator did not reach local insert")
	}
	adopted, err := createExistingScaleSet(t, adopter, adopterCtx)
	require.NoError(t, err)
	require.Equal(t, 42, adopted.ScaleSetID)
	close(release)
	released = true
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("creator did not finish")
	}
	require.ErrorContains(t, err, "local insert failed")
	require.Equal(t, 1, api.createRequests)
	require.Zero(t, api.deleteRequests, "creator deleted the remote set after the competing request adopted it")
}
