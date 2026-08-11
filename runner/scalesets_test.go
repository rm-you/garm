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

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	commonParams "github.com/cloudbase/garm-provider-common/params"
	"github.com/cloudbase/garm/auth"
	storeMocks "github.com/cloudbase/garm/database/common/mocks"
	"github.com/cloudbase/garm/params"
)

const testScaleSetActionsToken = "eyJhbGciOiJub25lIn0.eyJleHAiOjQxNDk5MzYwMDB9."

type scaleSetAPI struct {
	server         *httptest.Server
	existing       bool
	createConflict bool
	createRequests int
	deleteRequests int
}

func newScaleSetAPI(t *testing.T) *scaleSetAPI {
	t.Helper()

	api := new(scaleSetAPI)
	api.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rate_limit":
			_, _ = w.Write([]byte(`{"resources":{"core":{"limit":5000,"remaining":5000}}}`))
		case strings.HasSuffix(r.URL.Path, "/repos/owner/repo/actions/runners/registration-token"):
			_, _ = fmt.Fprintf(w, `{"token":"registration-token","expires_at":%q}`, time.Now().Add(time.Hour).Format(time.RFC3339))
		case r.URL.Path == "/actions/runner-registration":
			_, _ = fmt.Fprintf(w, `{"url":%q,"token":%q}`, api.server.URL, testScaleSetActionsToken)
		case strings.HasPrefix(r.URL.Path, "/_apis/runtime/runnerscalesets"):
			api.handleScaleSets(t, w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(api.server.Close)
	return api
}

func (a *scaleSetAPI) handleScaleSets(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()

	switch r.Method {
	case http.MethodGet:
		if a.existing {
			_, _ = w.Write([]byte(`{"count":1,"value":[{"id":42,"name":"existing","runnerGroupId":1}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"count":0,"value":[]}`))
	case http.MethodPost:
		a.createRequests++
		if a.createConflict {
			a.existing = true
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"Bad Request","details":"failed: \"{\\\"typeName\\\":\\\"GitHub.Actions.Runtime.WebApi.RunnerScaleSetExistsException, GitHub.Actions.Runtime.WebApi\\\"}\""}`))
			return
		}
		a.existing = true
		_, _ = w.Write([]byte(`{"id":42,"name":"existing","runnerGroupId":1}`))
	case http.MethodDelete:
		a.deleteRequests++
		w.WriteHeader(http.StatusNoContent)
	default:
		t.Errorf("unexpected scale-set request: %s", r.Method)
	}
}

func newScaleSetRunner(t *testing.T, api *scaleSetAPI, createErr error) (*Runner, context.Context) {
	t.Helper()

	ctx := auth.GetAdminContext(context.Background())
	store := storeMocks.NewStore(t)
	templateID := uint(1)
	credentials, err := json.Marshal(params.GithubPAT{OAuth2Token: "token"})
	require.NoError(t, err)
	entity := params.ForgeEntity{
		ID:         "entity-id",
		Owner:      "owner",
		Name:       "repo",
		EntityType: params.ForgeEntityTypeRepository,
		Credentials: params.ForgeCredentials{
			APIBaseURL:         api.server.URL + "/",
			UploadBaseURL:      api.server.URL + "/",
			BaseURL:            api.server.URL,
			AuthType:           params.ForgeAuthTypePAT,
			ForgeType:          params.GithubEndpointType,
			CredentialsPayload: credentials,
		},
	}
	store.EXPECT().GetForgeEntity(ctx, params.ForgeEntityTypeRepository, entity.ID).Return(entity, nil).Once()
	store.EXPECT().GetTemplate(ctx, templateID).Return(params.Template{
		ID:        templateID,
		OSType:    commonParams.Linux,
		ForgeType: params.GithubEndpointType,
	}, nil).Once()
	store.EXPECT().CreateEntityScaleSet(ctx, entity, mock.MatchedBy(func(param params.CreateScaleSetParams) bool {
		return param.ScaleSetID == 42
	})).Return(params.ScaleSet{ScaleSetID: 42}, createErr).Once()

	return &Runner{store: store}, ctx
}

func createExistingScaleSet(t *testing.T, runner *Runner, ctx context.Context) (params.ScaleSet, error) {
	t.Helper()
	templateID := uint(1)
	return runner.CreateEntityScaleSet(ctx, params.ForgeEntityTypeRepository, "entity-id", params.CreateScaleSetParams{
		Name:       "existing",
		OSType:     commonParams.Linux,
		TemplateID: &templateID,
	})
}

func TestCreateEntityScaleSetAdoptsExistingScaleSet(t *testing.T) {
	api := newScaleSetAPI(t)
	api.existing = true
	runner, ctx := newScaleSetRunner(t, api, nil)

	scaleSet, err := createExistingScaleSet(t, runner, ctx)
	require.NoError(t, err)
	require.Equal(t, 42, scaleSet.ScaleSetID)
	require.Zero(t, api.createRequests)
}

func TestCreateEntityScaleSetRecoversCreateConflict(t *testing.T) {
	api := newScaleSetAPI(t)
	api.createConflict = true
	runner, ctx := newScaleSetRunner(t, api, nil)

	scaleSet, err := createExistingScaleSet(t, runner, ctx)
	require.NoError(t, err)
	require.Equal(t, 42, scaleSet.ScaleSetID)
	require.Equal(t, 1, api.createRequests)
}

func TestCreateEntityScaleSetDoesNotDeleteAdoptedScaleSet(t *testing.T) {
	api := newScaleSetAPI(t)
	api.existing = true
	runner, ctx := newScaleSetRunner(t, api, errors.New("database unavailable"))

	_, err := createExistingScaleSet(t, runner, ctx)
	require.Error(t, err)
	require.Zero(t, api.deleteRequests)
}

func TestCreateEntityScaleSetDeletesCreatedScaleSetOnDatabaseFailure(t *testing.T) {
	api := newScaleSetAPI(t)
	runner, ctx := newScaleSetRunner(t, api, errors.New("database unavailable"))

	_, err := createExistingScaleSet(t, runner, ctx)
	require.Error(t, err)
	require.Equal(t, 1, api.createRequests)
	require.Equal(t, 1, api.deleteRequests)
}
