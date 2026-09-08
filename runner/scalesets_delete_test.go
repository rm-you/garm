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

	"github.com/stretchr/testify/require"

	"github.com/cloudbase/garm/auth"
	"github.com/cloudbase/garm/cache"
	storeMocks "github.com/cloudbase/garm/database/common/mocks"
	"github.com/cloudbase/garm/params"
)

func TestDeleteScaleSetHandlesRemoteResponse(t *testing.T) {
	databaseErr := errors.New("database unavailable")
	tests := []struct {
		name        string
		status      int
		deleteLocal bool
		localError  error
		wantError   bool
	}{
		{"deleted", http.StatusNoContent, true, nil, false},
		{"already missing", http.StatusNotFound, true, nil, false},
		{"unauthorized", http.StatusUnauthorized, false, nil, true},
		{"forbidden", http.StatusForbidden, false, nil, true},
		{"server error", http.StatusInternalServerError, false, nil, true},
		{"local cleanup error", http.StatusNotFound, true, databaseErr, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/rate_limit":
					fmt.Fprint(w, `{"resources":{"core":{"limit":5000,"remaining":5000}}}`)
				case strings.HasSuffix(r.URL.Path, "/registration-token"):
					fmt.Fprintf(w, `{"token":"registration-token","expires_at":%q}`, time.Now().Add(time.Hour).Format(time.RFC3339))
				case r.URL.Path == "/actions/runner-registration":
					fmt.Fprintf(w, `{"url":%q,"token":"eyJhbGciOiJub25lIn0.eyJleHAiOjQxNDk5MzYwMDB9."}`, server.URL)
				case r.Method == http.MethodDelete && r.URL.Path == "/_apis/runtime/runnerscalesets/42":
					w.WriteHeader(tt.status)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			ctx := auth.GetAdminContext(context.Background())
			payload, err := json.Marshal(params.GithubPAT{OAuth2Token: "token"})
			require.NoError(t, err)
			entity := params.ForgeEntity{
				ID: t.Name(), Owner: "owner", Name: "repo", EntityType: params.ForgeEntityTypeRepository,
				Credentials: params.ForgeCredentials{
					APIBaseURL: server.URL + "/", UploadBaseURL: server.URL + "/", BaseURL: server.URL,
					AuthType: params.ForgeAuthTypePAT, ForgeType: params.GithubEndpointType, CredentialsPayload: payload,
				},
			}
			defer cache.DeleteGithubClient(entity.ID)
			store := storeMocks.NewStore(t)
			store.EXPECT().GetScaleSetByID(ctx, uint(4)).Return(params.ScaleSet{ID: 4, RepoID: entity.ID, ScaleSetID: 42}, nil).Once()
			store.EXPECT().GetForgeEntity(ctx, params.ForgeEntityTypeRepository, entity.ID).Return(entity, nil).Once()
			if tt.deleteLocal {
				store.EXPECT().DeleteScaleSetByID(ctx, uint(4)).Return(tt.localError).Once()
			}

			runner := &Runner{store: store}
			err = runner.DeleteScaleSetByID(ctx, 4)
			if tt.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if tt.localError != nil {
				require.ErrorIs(t, err, tt.localError)
			}
		})
	}
}
