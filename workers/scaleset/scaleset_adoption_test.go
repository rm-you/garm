// Copyright 2026 Cloudbase Solutions SRL
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
// WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
// License for the specific language governing permissions and limitations
// under the License.

//go:build testing

package scaleset

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/go-github/v84/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	runnerErrors "github.com/cloudbase/garm-provider-common/errors"
	"github.com/cloudbase/garm/cache"
	storeMocks "github.com/cloudbase/garm/database/common/mocks"
	"github.com/cloudbase/garm/params"
	runnerMocks "github.com/cloudbase/garm/runner/common/mocks"
)

const testActionsToken = "eyJhbGciOiJub25lIn0.eyJleHAiOjQxNDk5MzYwMDB9."

func newScaleSetWorkerForTest(
	t *testing.T,
	store *storeMocks.Store,
	scaleSet params.ScaleSet,
	actionsHandler http.HandlerFunc,
) *Worker {
	t.Helper()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/actions/runner-registration" {
			assert.Equal(t, http.MethodPost, r.Method)
			_, _ = fmt.Fprintf(w, `{"url":%q,"token":%q}`, server.URL, testActionsToken)
			return
		}
		actionsHandler(w, r)
	}))
	t.Cleanup(server.Close)

	baseURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	entity := params.ForgeEntity{
		ID:         scaleSet.RepoID,
		EntityType: params.ForgeEntityTypeRepository,
		Owner:      "owner",
		Name:       "repo",
		Credentials: params.ForgeCredentials{
			BaseURL: server.URL,
		},
	}
	githubClient := runnerMocks.NewGithubClient(t)
	githubClient.EXPECT().
		CreateEntityRegistrationToken(mock.Anything).
		Return(&github.RegistrationToken{
			Token:     github.Ptr("registration-token"),
			ExpiresAt: &github.Timestamp{Time: time.Now().Add(time.Hour)},
		}, nil, nil).
		Once()
	githubClient.EXPECT().GithubBaseURL().Return(baseURL).Once()
	githubClient.EXPECT().GetEntityRunnerGroupIDByName(mock.Anything, scaleSet.GitHubRunnerGroup).Return(int64(1), nil).Once()
	githubClient.EXPECT().GetEntity().Return(entity).Maybe()
	cache.SetGithubClient(scaleSet.RepoID, githubClient)
	t.Cleanup(func() { cache.DeleteGithubClient(scaleSet.RepoID) })

	return &Worker{
		ctx:      context.Background(),
		store:    store,
		scaleSet: scaleSet,
	}
}

func testScaleSet(t *testing.T) params.ScaleSet {
	t.Helper()
	return params.ScaleSet{
		ID:                4,
		Name:              "example-garm-123",
		RepoID:            t.Name(),
		GitHubRunnerGroup: "default",
	}
}

func expectScaleSetIDUpdate(t *testing.T, store *storeMocks.Store, scaleSet params.ScaleSet, githubID int) {
	t.Helper()

	entity := params.ForgeEntity{ID: scaleSet.RepoID, EntityType: params.ForgeEntityTypeRepository}
	store.EXPECT().
		UpdateEntityScaleSet(
			mock.Anything,
			entity,
			scaleSet.ID,
			mock.MatchedBy(func(update params.UpdateScaleSetParams) bool {
				return update.ScaleSetID == githubID
			}),
			mock.Anything,
		).
		Return(params.ScaleSet{}, nil).
		Once()
	store.EXPECT().SetScaleSetLastMessageID(mock.Anything, scaleSet.ID, int64(0)).Return(nil).Once()
}

func TestEnsureScaleSetInGitHubAdoptsExistingScaleSet(t *testing.T) {
	scaleSet := testScaleSet(t)
	store := storeMocks.NewStore(t)
	expectScaleSetIDUpdate(t, store, scaleSet, 42)

	w := newScaleSetWorkerForTest(t, store, scaleSet, func(rw http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "1", r.URL.Query().Get("runnerGroupId"))
		assert.Equal(t, scaleSet.Name, r.URL.Query().Get("name"))
		_, _ = fmt.Fprintf(rw, `{"count":1,"value":[{"id":42,"name":%q,"runnerGroupId":1}]}`, scaleSet.Name)
	})

	require.NoError(t, w.ensureScaleSetInGitHub())
	assert.Equal(t, 42, w.scaleSet.ScaleSetID)
}

func TestEnsureScaleSetInGitHubEscapesLookupName(t *testing.T) {
	scaleSet := testScaleSet(t)
	scaleSet.Name = "example+garm&123"
	store := storeMocks.NewStore(t)
	expectScaleSetIDUpdate(t, store, scaleSet, 42)

	w := newScaleSetWorkerForTest(t, store, scaleSet, func(rw http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, scaleSet.Name, r.URL.Query().Get("name"))
		_, _ = fmt.Fprintf(rw, `{"count":1,"value":[{"id":42,"name":%q,"runnerGroupId":1}]}`, scaleSet.Name)
	})

	require.NoError(t, w.ensureScaleSetInGitHub())
}

func TestEnsureScaleSetInGitHubFallsBackToRunnerGroupListWithoutRunnerGroupID(t *testing.T) {
	scaleSet := testScaleSet(t)
	store := storeMocks.NewStore(t)
	expectScaleSetIDUpdate(t, store, scaleSet, 42)

	w := newScaleSetWorkerForTest(t, store, scaleSet, func(rw http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "1", r.URL.Query().Get("runnerGroupId"))
		if r.URL.Query().Get("name") != "" {
			_, _ = rw.Write([]byte(`{"count":0,"value":[]}`))
			return
		}
		_, _ = fmt.Fprintf(rw, `{"count":1,"value":[{"id":42,"name":%q}]}`, scaleSet.Name)
	})

	require.NoError(t, w.ensureScaleSetInGitHub())
	assert.Equal(t, 42, w.scaleSet.ScaleSetID)
}

func TestEnsureScaleSetInGitHubRecoversCreateConflict(t *testing.T) {
	scaleSet := testScaleSet(t)
	store := storeMocks.NewStore(t)
	expectScaleSetIDUpdate(t, store, scaleSet, 42)

	createAttempted := false
	w := newScaleSetWorkerForTest(t, store, scaleSet, func(rw http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if createAttempted && r.URL.Query().Get("name") == "" {
				_, _ = fmt.Fprintf(rw, `{"count":1,"value":[{"id":42,"name":%q,"runnerGroupId":1}]}`, scaleSet.Name)
				return
			}
			_, _ = rw.Write([]byte(`{"count":0,"value":[]}`))
		case http.MethodPost:
			createAttempted = true
			rw.WriteHeader(http.StatusBadRequest)
			_, _ = rw.Write([]byte(`{"typeName":"RunnerScaleSetExistsException"}`))
		default:
			t.Errorf("unexpected Actions request: %s %s", r.Method, r.URL.String())
		}
	})

	require.NoError(t, w.ensureScaleSetInGitHub())
	assert.True(t, createAttempted)
	assert.Equal(t, 42, w.scaleSet.ScaleSetID)
}

func TestEnsureScaleSetInGitHubPreservesUnrelatedBadRequest(t *testing.T) {
	scaleSet := testScaleSet(t)
	store := storeMocks.NewStore(t)
	createAttempted := false
	w := newScaleSetWorkerForTest(t, store, scaleSet, func(rw http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			createAttempted = true
			rw.WriteHeader(http.StatusBadRequest)
			_, _ = rw.Write([]byte(`{"typeName":"InvalidRunnerScaleSetException"}`))
			return
		}
		if createAttempted {
			_, _ = fmt.Fprintf(rw, `{"count":1,"value":[{"id":42,"name":%q,"runnerGroupId":1}]}`, scaleSet.Name)
			return
		}
		_, _ = rw.Write([]byte(`{"count":0,"value":[]}`))
	})

	err := w.ensureScaleSetInGitHub()
	require.Error(t, err)
	assert.ErrorIs(t, err, runnerErrors.ErrBadRequest)
	assert.Zero(t, w.scaleSet.ScaleSetID)
}

func TestEnsureScaleSetInGitHubCreatesMissingScaleSet(t *testing.T) {
	scaleSet := testScaleSet(t)
	store := storeMocks.NewStore(t)
	expectScaleSetIDUpdate(t, store, scaleSet, 42)

	createCount := 0
	w := newScaleSetWorkerForTest(t, store, scaleSet, func(rw http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			createCount++
			_, _ = fmt.Fprintf(rw, `{"id":42,"name":%q,"runnerGroupId":1}`, scaleSet.Name)
			return
		}
		_, _ = rw.Write([]byte(`{"count":0,"value":[]}`))
	})

	require.NoError(t, w.ensureScaleSetInGitHub())
	assert.Equal(t, 1, createCount)
	assert.Equal(t, 42, w.scaleSet.ScaleSetID)
}

func TestEnsureScaleSetInGitHubPreservesExistingIDMismatch(t *testing.T) {
	scaleSet := testScaleSet(t)
	scaleSet.ScaleSetID = 7
	store := storeMocks.NewStore(t)
	w := newScaleSetWorkerForTest(t, store, scaleSet, func(rw http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(rw, `{"count":1,"value":[{"id":42,"name":%q,"runnerGroupId":1}]}`, scaleSet.Name)
	})

	err := w.ensureScaleSetInGitHub()
	require.Error(t, err)
	var conflict *runnerErrors.ConflictError
	assert.ErrorAs(t, err, &conflict)
}
