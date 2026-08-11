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

package scalesets

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	runnerErrors "github.com/cloudbase/garm-provider-common/errors"
)

func TestDoRecognizesNestedRunnerScaleSetExistsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"Bad Request","details":"error creating runner scale set: failed: \"{\\\"typeName\\\":\\\"GitHub.Actions.Runtime.WebApi.RunnerScaleSetExistsException, GitHub.Actions.Runtime.WebApi\\\"}\""}`))
	}))
	t.Cleanup(server.Close)

	client := &ScaleSetClient{httpClient: server.Client()}
	req, err := http.NewRequest(http.MethodPost, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.Do(req)
	if !errors.Is(err, ErrRunnerScaleSetExists) {
		t.Fatalf("expected ErrRunnerScaleSetExists, got %v", err)
	}
	if !errors.Is(err, runnerErrors.ErrBadRequest) {
		t.Fatalf("expected ErrBadRequest, got %v", err)
	}
}

func TestRunnerScaleSetExistsTypeRequiresExactName(t *testing.T) {
	if isRunnerScaleSetExistsType("NotRunnerScaleSetExistsException") {
		t.Fatal("unexpected duplicate classification")
	}
}
